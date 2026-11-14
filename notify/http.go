package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets decides whether delivery to loopback / link-local addresses is
// allowed.
//
// Denied by default. These address ranges are not where an IM bot or public mail
// server would appear, and what they can reach is very sensitive: the admin port of
// another service on the same host, and the cloud environment's metadata endpoint
// (169.254.169.254, which can read out instance credentials). The delivery address is
// set by an admin, but an admin session borrowed via XSS/CSRF, or a second person
// sharing the same JWT, could change the config to read the response content back --
// doJSON writes the first 200 bytes of a 4xx/5xx response body into last_error, and
// the delivery-history endpoint echoes it out, which is a semi-blind read primitive.
//
// But a "local SMTP relay" (postfix on 127.0.0.1:25) is a common self-hosted email
// setup, and a blanket ban would block people. So leave an explicit escape hatch
// rather than hard-coding an allowance: setting ARTEX_NOTIFY_ALLOW_LOCAL=1 allows it.
//
// It is exported as AllowLocalTargetsEnv so tests can clearly turn it on -- this
// package's and the server package's cases heavily use httptest fake receivers on
// 127.0.0.1, which are all blocked by the guard unless it is turned on.
const AllowLocalTargetsEnv = "ARTEX_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP reports whether the target IP falls into an address range that is
// "not allowed for delivery by default".
//
// It only rejects loopback, link-local (including cloud metadata 169.254.169.254),
// unspecified, and multicast.
// It **does not reject** RFC1918 private networks: an internal self-hosted Mattermost
// / SMTP relay is a very common legitimate use, and blocking those too would make the
// feature outright unusable in real environments. This trade-off is deliberate -- the
// defense should block the truly sensitive targets while not killing normal
// deployments along with them.
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be restored to IPv4 before judging,
	// otherwise it is a bypass.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial is the Control hook of the http.Transport dialer, checking the
// target address **when the connection is established**.
//
// Why it is at the dial phase rather than only validating when saving config: this is
// the final enforcement point. It covers two scenarios that bypass config validation
// -- DNS rebinding (resolves to a public IP at validation, to an internal IP when
// actually connecting) and redirects (although we already reject cross-host hops, a
// same-host hop could still point the path elsewhere).
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("cannot resolve target address %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("refusing to deliver to loopback/link-local address %s (to deliver to a local service, set %s=1)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport adds only a dial guard on top of the default Transport.
// Clone is used to preserve all of the default tuning (connection pool, HTTP/2,
// timeouts, proxy, etc.), avoiding changes to other behavior just to add one check.
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient is the client shared by all channel deliveries.
//
// It deliberately does **not** reuse the project's global egress proxy (the server's
// GlobalProxy): that proxy is for pentest target traffic and is often an unstable
// tunnel, and notification availability should not be held hostage to the target
// network's jitter. IM push can connect directly. The timeout is 15 seconds -- a peer
// slower than that is effectively already in a failure state.
//
// Cross-host redirects are rejected: this feature's delivery addresses are all "a
// fixed endpoint" in shape and normally would not redirect to another host; and these
// channels' credentials (DingTalk access_token, WeCom key, Telegram bot token) **are
// in the URL**, so following a cross-host hop hands the credential to the redirect
// target. Same-host hops (such as a trailing slash) are still allowed.
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("refusing cross-host redirect (%s → %s)", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit limits the size of the response body read. A misbehaving peer may spit
// back a huge amount of content, while we only need the error code and a short error
// description to display in the delivery history.
const respBodyLimit = 8 << 10

// doJSON sends one request and returns the (length-limited) response body.
//
// When payload is nil, an empty body is sent (for GET or platforms that do not require
// a body).
// The key/value pairs in headers are appended as-is, for the generic Webhook's custom
// headers.
//
// Error classification is this function's core responsibility: network-layer failures
// and 5xx/408/429 are classified as "retryable", while the rest of the 4xx are
// classified as "permanent" -- retrying a 403 only logs the same error three times.
func doJSON(ctx context.Context, method, url string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// A serialization failure is a local bug (a config field has the wrong
			// type), and retrying will not help.
			return nil, Permanent(fmt.Errorf("failed to build request body: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		// Invalid URL -- most likely the user filled in the address wrong, a permanent
		// failure.
		// Here too err must not be passed through: url.Parse's error text contains the
		// full address.
		return nil, Permanent(fmt.Errorf("invalid request URL: %s", redactRequestTarget(url)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout -- mostly transient faults, handed
		// to backoff retry.
		//
		// The error text must be redacted before being passed outward. Reason:
		// http.Client.Do returns a *url.Error whose Error() is `Op "full URL":
		// underlying error`, and these channels' credentials **are in the URL**
		// (DingTalk access_token, WeCom key, Feishu hook id, Telegram /bot<token>/).
		// Without redaction, the credential flows along this error string to four
		// places: notification_deliveries' last_error (persisted in plaintext), the
		// delivery-history endpoint's response (**bypassing the channel config's
		// mask**), the server logs, and the 502 text the test-send endpoint returns to
		// the frontend.
		return nil, fmt.Errorf("request failed: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// 429 (rate limit) and 408 (timeout) are worth retrying; the rest of the 4xx are
	// config or permission problems where retrying is pointless.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("peer rate-limited or timed out (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("peer service error (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("peer rejected the request (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet compresses the response body to a short one-line text, for error messages.
// A response may carry newlines and lots of whitespace, and stuffing it directly into
// last_error would break the layout of the delivery-history page.
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget compresses a delivery address to "scheme://host/…", for error
// messages.
//
// This is the only address-redaction standard in this package, deliberately made
// **blunt enough**: everything except scheme and host is discarded. The reason is that
// there is no "universal and safe" way to tell which segment of a URL is the
// credential:
//
//	DingTalk   credential in query      /robot/send?access_token=xxx
//	WeCom      credential in query      /cgi-bin/webhook/send?key=xxx
//	Feishu     credential in the **last path segment** /open-apis/bot/v2/hook/<hook_id>
//	Telegram   credential in the **middle path segment** /bot<token>/sendMessage
//
// Trying to "keep only the useful part" would require patching per channel, and
// missing any one is a credential leak. Keeping the host is already enough for
// troubleshooting (DNS not resolving, cannot connect, wrong certificate can all be
// located), and which bot it is can be recognized from the masked tail digits in the
// channel config.
//
// On a parse failure, a fixed placeholder is returned -- the original string is never
// echoed out.
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unparseable address)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError strips the address out of a transport-layer error, keeping only
// the underlying cause.
//
// A *url.Error's structure is {Op, URL, Err}, and Error() prints the URL along with
// it. Here we explicitly take the Err field, bypassing its Error() -- more reliable
// than doing a string replacement afterward, because a replacement would have to
// correctly handle the various URL-encoded/escaped variants of the address, which is
// easy to miss.
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: unknown error", uerr.Op, host)
	}
	// A non-*url.Error (such as an error returned by the redirect policy) may also
	// carry an address, so it goes through redaction uniformly.
	return redactURLsInText(err.Error())
}

// redactURLsInText replaces http(s) addresses appearing in a piece of text with the
// redacted form.
//
// Used as a fallback for errors that cannot be obtained as structured fields (redirect
// policy errors, third-party libraries' custom errors). It only recognizes the
// http/https prefix, splitting on whitespace and quotes -- an address does not contain
// either of those.
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
