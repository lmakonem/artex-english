package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter is the contract for the notification_channels.filter JSONB column: the
// filtering conditions of a channel instance. All fields are optional, and a missing
// field means "do not filter" -- which is exactly the fallback semantics for
// malformed config, see ParseFilter.
type Filter struct {
	// MinSeverity is the minimum severity threshold (low/medium/high/critical); empty
	// = no threshold.
	MinSeverity string `json:"min_severity"`
	// TaskIDs / AssetIDs: an empty array means no restriction; a non-empty one
	// requires the event to intersect with it.
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// VulnClassInclude empty means accept all; non-empty requires vulnclass to match
	// at least one keyword.
	// VulnClassExclude: matching any keyword excludes the event (exclude takes
	// precedence over include).
	// Matching is case-insensitive substring -- safer than regex: a misconfigured
	// regex will not silently break the channel.
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange decides whether this channel receives finding status-change
	// events (only meaningful in realtime mode).
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter parses a channel's filter config.
//
// **It never returns an error.** This is a deliberate design choice: when the filter
// config is malformed it always degrades to a zero-value Filter (= no filtering =
// matches everything), because for a finding notification system **pushing one extra
// is far better than silently dropping a high-severity one**. Turning a parse failure
// into "no push" would hand the user a channel that looks configured but actually
// pushes nothing -- the worst possible failure mode.
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// On parse failure f stays at its zero value, i.e. no filtering.
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity reports whether s is a valid severity threshold (an empty string
// means no threshold).
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate checks the **value-restricted** fields of the filter config, to be called
// when saving a channel.
//
// Why it must be blocked at write time: Match's decision for an unknown threshold is
// `rank >= 0`, which is always true -- meaning a single typo in min_severity
// ("hgih") makes the filter **silently fail** and turn into "push everything". This
// is consistent with this package's "better over-push than under-push" trade-off (it
// will not under-push), but the consequence is that the user believes they are doing
// tiered pushes while actually dumping every finding into the group, with no
// indication at all that it is misconfigured. This kind of "silent degradation"
// should be blocked at the entry point.
//
// Note that Validate is only used on the **write** path. The read path still uses
// ParseFilter's lenient semantics, so bad values already present in historical data
// will not make the channel fail to load entirely.
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("invalid minimum severity %q; options: low / medium / high / critical, or leave empty for no threshold", f.MinSeverity)
	}
	return nil
}

// Match decides whether an event should be delivered to a channel carrying the given
// filter conditions.
//
// **It never returns an error**, for the same reason as ParseFilter: any internal
// anomaly is treated as a match.
// Decision order: event kind -> severity threshold -> task/asset scope -> vuln class
// keywords.
func Match(f Filter, s Snapshot) bool {
	// Status-change events are only received by channels that explicitly opt in. Off
	// by default, because the vast majority of users expect "push" to mean "a new
	// finding was discovered", not a running log tracking every status transition.
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// Exclude takes precedence: matching any exclude keyword knocks it out, even if
	// it also matches the include list.
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// A linear scan is fine for small sets; both sides are "a few dozen hand-picked
	// entries", so building a map costs more than it saves.
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold reports whether s contains any of the keywords (case-insensitive).
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
