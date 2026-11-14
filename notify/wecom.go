package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit is the hard upper bound (in bytes, not characters) for the WeCom
// group bot markdown content. This is the tightest limit of all six channels, and the
// main reason TruncateBytes exists.
const weComMarkdownLimit = 4096

// weComChannel implements the WeCom (WeChat Work) group bot.
//
// Platform characteristics:
//   - The only one authenticated solely by the key in the URL, with no signing
//     support -- so the webhook address itself is the entire credential.
//   - markdown content limit is 4096 **bytes**; an over-length message is rejected
//     whole (not truncated). CJK is 3 bytes/char, which means the body can hold only
//     a bit over a thousand characters, so client-side truncation is mandatory.
//   - Rate limit of 20/minute, also handled by client-side rate limiting.
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// WeCom has only one credential, the Webhook (the key in the URL), and it does not
// support signing -- the whole address is the entire credential, so there is no other
// field to mask.
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// WeCom has only the one Webhook field, which is both destination and credential, so
// there is no "credential left over after changing the address".
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("missing Webhook address")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("invalid Webhook address: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// A digest batch can be very long (50 items x one line each + prefix), and 4096
	// bytes is easily exceeded.
	// Truncation is done here rather than relying on the platform's error: a rejection
	// means the whole batch is lost, while truncation at least delivers the first few
	// items.
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("failed to parse WeCom response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 means "API call exceeded the limit" -- the platform's rate-limit
		// window rolls, so backing off and retrying is effective, and it is explicitly
		// classified as retryable. Reaching here means the client rate_per_min is set
		// too aggressively; retrying is only a safety net, and the real fix is to lower
		// this channel's rate limit.
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("WeCom rate limited %d: %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 means the webhook key is invalid -- a permanent failure that retrying
		// will not heal.
		return 0, Permanent(fmt.Errorf("WeCom returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
