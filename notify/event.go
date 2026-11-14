package notify

// Snapshot is the contract for the notification_events.snapshot JSONB column. The
// writer is the finding-persisting transaction in the db layer; the readers are the
// delivery engine and filter matcher in the server layer. The definition lives in
// this package because it is the "notification domain" payload: db only serializes
// it and does not understand the field meanings.
//
// Why finding fields are stored redundantly instead of being looked up at render
// time: a finding may later be renamed, re-leveled, or have its status changed,
// while the pushed content should reflect the conclusion **at the time of the
// event**. A lookup would yield the dangerously misleading "later downgraded to
// low" state. It also means fan-out and rendering need not JOIN the findings/tasks/
// assets tables.
type Snapshot struct {
	// Event kind: finding_created / finding_status_changed
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// Non-empty only when kind=finding_status_changed.
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item is a single finding pending delivery, for channel rendering.
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets are the resolved asset display names (such as domain/IP). Populated by
	// the server layer -- this package does not touch the database and cannot obtain
	// the names.
	Assets []string
	// DetailURL is the back-link to the finding detail; empty means public_base_url
	// is not configured, and it is omitted when rendering.
	DetailURL string
	// Specific to status-change events; when both are non-empty they render as
	// "Pending -> Fixed".
	FromStatus string
	ToStatus   string
}

// IsStatusChange reports whether the item is a status-change event.
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title returns the item's display title: it prefers the human-assigned name, falls
// back to the finding type vulnclass, and uses a placeholder when both are empty --
// it never emits an empty title.
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(unnamed finding)"
}

// Message is the complete content of one channel send.
type Message struct {
	// Length 1 for a single push; a whole batch for a digest push.
	// An empty slice is invalid; the caller must guarantee at least one item.
	Items []Item
	// When Batch=true, render as a digest message (different title, with time window
	// and item count).
	Batch bool
	// WindowMinutes is the digest period (minutes), used only when Batch=true for the
	// "last N minutes" phrasing.
	// Deliberately passed in explicitly from config rather than computed as
	// time.Since at render time: rendering stays deterministic, which keeps it
	// testable.
	WindowMinutes int
	// HomeURL is the platform dashboard address (the global public_base_url); empty
	// means no dashboard entry point is included.
	HomeURL string
}
