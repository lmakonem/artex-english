// Package notify implements the IM / email push channel adapter layer for finding
// notifications.
//
// Layering: this package is a **leaf package** depending only on the standard
// library. It knows nothing about the database or the server. Channel config is
// passed in as map[string]any (corresponding to the notification_channels.config
// JSONB column), and the content to push is passed in as a Message. The benefit of
// this split is that the genuinely error-prone parts -- signature computation, UTF-8
// truncation, filter matching -- can be unit-tested without PostgreSQL, while the
// host only needs to do the orchestration on the server side.
//
// Concurrency contract: Channel implementations must be **stateless**. The same
// Channel instance is reused concurrently by multiple channel configs (even multiple
// bot instances of the same channel), so all credentials are passed in via the cfg
// parameter; caching things like a webhook URL into the implementation's own fields
// is not allowed.
package notify

// Channel type identifiers. The values are also the valid set for
// notification_channels.kind, allow-listed on the server side (same as
// findings.status: no DB CHECK, which makes adding channels later easier).
const (
	KindDingTalk = "dingtalk" // DingTalk custom bot
	KindFeishu   = "feishu"   // Feishu (including Lark) custom bot
	KindWeCom    = "wecom"    // WeCom group bot
	KindWebhook  = "webhook"  // Generic Webhook: custom method/headers/JSON template
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP email
)

// Event kinds, corresponding to notification_events.kind.
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind is the fallback value for an empty kind in config.
const InitKind = KindDingTalk

// severityRank maps finding severity to a comparable ordinal. Unknown severities
// return 0, so any min_severity setting will keep unknown severities out -- when in
// doubt, do not push, to avoid flooding with false positives.
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank returns the ordinal for the severity; unknown severities return 0.
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel returns the severity name with an emoji, for message titles and card
// coloring. Unknown severities are echoed back as-is, not invented.
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 Critical"
	case "high":
		return "🟠 High"
	case "medium":
		return "🟡 Medium"
	case "low":
		return "🔵 Low"
	default:
		return severity
	}
}

// StatusLabel translates a disposition status into a readable label, for
// status-change messages.
func StatusLabel(status string) string {
	switch status {
	case "pending":
		return "Pending"
	case "in_progress":
		return "In Progress"
	case "confirmed":
		return "Confirmed"
	case "resolved":
		return "Resolved"
	case "fixed":
		return "Fixed"
	case "false_positive":
		return "False Positive"
	case "ignored":
		return "Ignored"
	case "duplicate":
		return "Duplicate"
	case "risk_accepted":
		return "Risk Accepted"
	default:
		return status
	}
}

// AtLeast reports whether severity meets the min threshold. An empty min means no
// threshold, so everything passes. Note that an unknown severity has ordinal 0 and
// will be rejected by any non-empty min (see the severityRank comment).
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}
