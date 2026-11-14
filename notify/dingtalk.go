package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel implements the DingTalk custom bot.
//
// Platform characteristics (which drive the implementation trade-offs here):
//   - A single bot is rate-limited to 20/minute; over-sending is silently dropped
//     (HTTP may still return 200), so rate limiting must be done client-side, see
//     DefaultRatePerMin.
//   - Security settings are one of three: signing / custom keyword / IP allow-list.
//     Signing is the only option that does not depend on message content, so only
//     signing is supported (a bare webhook with none of the three enabled also
//     works).
//   - Both success and failure return HTTP 200; they are distinguished by the errcode
//     in the body -- not checking errcode would record a failed delivery as a
//     success.
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// DingTalk's Webhook address carries the access_token and is itself a credential, so
// it is masked as a whole.
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// The destination is the DingTalk Webhook address itself; changing it requires
// re-attesting the signing secret for the new address.
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("missing Webhook address")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("invalid Webhook address: %w", err)
	}
	return nil
}

// Send delivers one message. With a back-link and a single item it uses an ActionCard
// (with a button); otherwise it uses markdown.
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// DingTalk markdown has no explicit byte limit, but we still cap it to guard
	// against abnormal growth of evidence fields.
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "View details",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// DingTalk hides business errors inside a 200 response.
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("failed to parse DingTalk response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 is signature verification failure, 310000 is keyword mismatch -- both
		// are config errors that retrying will not heal.
		return 0, Permanent(fmt.Errorf("DingTalk returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL appends the timestamp and sign parameters to the webhook per the
// official signing rules.
//
// Rule: the string to sign = timestamp + "\n" + secret, and the HMAC-SHA256 **key is
// also the secret**; the result is base64-encoded and then URL-encoded. The timestamp
// is in milliseconds. When secret is empty it returns the address unchanged, to
// support bots without signing enabled.
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// Do not pass err through: url.Parse's error text contains the full address
		// (including access_token).
		return "", fmt.Errorf("failed to parse Webhook address: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL checks that the address is usable, the scheme is supported, and
// performs an internal-network check on literal IP targets.
//
// Two points of care:
//
//  1. **The error message must be redacted**. url.Parse returns a *url.Error whose
//     Error() carries the **full original address**, and the addresses of these
//     channels have credentials embedded right in them (DingTalk access_token, WeCom
//     key, Telegram bot token, Feishu hook id). This used to be a bare `return err`,
//     so the "malformed address" error carried the credential out into the 400
//     response of the test endpoint, the last_error persisted on every delivery, the
//     server logs, and the delivery-history endpoint.
//
//  2. **Literal IPs are judged as internal immediately**, while domains are left to
//     the dial phase (blockInternalDial is the final enforcement point, which also
//     covers DNS rebinding). Doing it once here is so the user gets a hint when saving
//     the config, instead of waiting for the first delivery to fail.
//
// Restricting the scheme is defensive: things like file:///gopher:// could make the
// http.Client behave unexpectedly (already blocked by the scheme check, but there is
// no reason to open up this surface).
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("address cannot be parsed (%s)", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https is supported, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("refusing to deliver to loopback/link-local address %s (to deliver to a local service, set %s=1)", ip, AllowLocalTargetsEnv)
	}
	return nil
}
