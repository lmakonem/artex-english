package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel is the adapter for one notification channel. Implementations must be
// **stateless**: the same instance is reused concurrently by multiple channel configs,
// and credentials are always passed in via the cfg parameter.
type Channel interface {
	// Kind returns the channel type identifier, which must match the registry key.
	Kind() string
	// Validate is called when saving config, checking required fields and format. The
	// returned error is shown directly to the configurer, so the wording should say
	// "which field is missing" rather than a vague "invalid config".
	Validate(cfg map[string]any) error
	// Send delivers one message, returning the **number of items actually delivered**
	// and an error.
	//
	// Why return the count: every platform has a message length limit, and a digest
	// message that does not fit the whole batch gets truncated. If the caller
	// unconditionally marks the whole batch as delivered, the truncated items vanish
	// -- invisible in the message, shown as success in the delivery history, and
	// nowhere can the finding being never-sent be discovered. After returning kept,
	// the caller marks only the first kept items, leaving the rest for the next batch.
	//
	// A returned error means delivery failed, and a *PermanentError among them means
	// it should not be retried.
	// On failure kept is meaningless and the caller should ignore it.
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin returns the channel's officially recommended per-minute
	// delivery cap, as the default rate limit when creating a channel instance.
	// Returning 0 means no known limit.
	DefaultRatePerMin() int
	// SecretKeys returns the key names in this channel's config that are credentials.
	// On API echo the values of these keys are masked, and on update a masked value
	// means keep the stored value. Only the implementation itself knows which fields
	// count as credentials (WeCom's whole Webhook address is the credential, while
	// DingTalk's is only the secret within it), so this knowledge must be provided by
	// the channel and not guessed by the upper layer.
	SecretKeys() []string
	// DestinationKeys returns the key names in this channel's config that decide
	// "where the message is sent".
	//
	// Like SecretKeys, it is security-relevant: the destination and the credential are
	// two independent sets of fields, and if "change the address only, keep the
	// credential as-is" were allowed, anyone who can change channel config could send
	// the real stored credential to a server they control, and the channel config's
	// mask would lose all meaning.
	// See PrepareConfigUpdate for details.
	DestinationKeys() []string
}

// registry is the channel registry. An explicit literal is used deliberately rather
// than init() self-registration: this way "which channels exist" can be seen in one
// place, and adding a channel exposes an omission at compile time rather than via a
// runtime side effect.
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindFeishu:   feishuChannel{},
	KindWeCom:    weComChannel{},
	KindWebhook:  webhookChannel{},
	KindTelegram: telegramChannel{},
	KindEmail:    emailChannel{},
}

// Get fetches the channel implementation by type.
func Get(kind string) (Channel, bool) {
	c, ok := registry[kind]
	return c, ok
}

// ValidKind reports whether kind is a supported channel type.
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds returns all supported channel types, in lexical order (for a stable UI
// dropdown).
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError marks a delivery failure that should not be retried: credential
// error, destination rejection, illegal request body, etc.
// Retrying is only meaningful for transient faults (network jitter, rate limiting,
// peer 5xx); for a permanent failure, repeated backoff retries will not succeed and
// will bury the real error in retry logs.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marks err as a permanent failure. It returns nil when err is nil, which is
// convenient for writing `return Permanent(someCheck())`.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether the error chain carries a permanent-failure marker.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// ---- config-reading helpers ----
//
// Channel config comes from the database's JSONB column, and after encoding/json
// deserialization it is a map[string]any, where numbers are always float64 and arrays
// are []any. The helpers below unify this conversion layer and tolerate the type
// deviations caused by users leaving fields blank in the UI (such as filling the port
// as a string).

// cfgString reads a string config item, trimming leading/trailing whitespace -- easy
// to pick up when copy-pasting from a web form.
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt reads an integer config item, supporting both float64 (JSON default) and
// string sources.
func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// cfgBool reads a boolean config item, supporting the strings "true"/"1".
func cfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

// cfgStrings reads a string-array config item, auto-trimming whitespace and dropping
// empty strings.
func cfgStrings(cfg map[string]any, key string) []string {
	raw, ok := cfg[key].([]any)
	if !ok {
		// Also accept a single string, convenient for form submission when there is
		// only one value.
		if s := cfgString(cfg, key); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap reads a string-map config item (such as custom HTTP headers), trimming both
// keys and values and dropping empty keys.
func cfgMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
