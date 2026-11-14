package notify

import (
	"fmt"
	"strings"
)

// This file is the shared message rendering for the "markdown family" channels
// (DingTalk, WeCom). Feishu uses card JSON, Telegram uses HTML, and email uses HTML,
// each rendered in its own adapter.

// maxAssetsShown is the maximum number of assets listed in a message. A single
// finding may anchor dozens of assets; listing them all overflows the message with no
// informational value -- nobody reads the domains past the 4th one in an IM.
const maxAssetsShown = 3

// maxSummaryRunes is how many characters the summary is compressed to. An IM message
// is a "prompt to go look at the details", not the report body; the full content
// lives on the platform.
const maxSummaryRunes = 120

// markdownReservedBytes reserves space for the message header (digest line + severity
// distribution + possible truncation notice) and footer (platform link). When packing
// whole items, this amount is subtracted from the budget so the header and footer are
// never truncated -- once they are cut, the reader cannot even tell "which batch this
// is, or how many items are not shown".
const markdownReservedBytes = 320

// markdownEscape escapes markdown metacharacters.
//
// Why it is mandatory: finding titles, summaries, types, and asset display names all
// come from **untrusted sources** -- the title and summary come from model output
// (the model reads the target under test's responses), and the asset URL is the full
// scanned URL (including the target-controllable query string). Without escaping, a
// finding whose title is
//
//	Login SQL injection\n[Urgent: click here to verify your account](http://attacker.tld)
//
// would render in a security engineer's DingTalk/Feishu as a **clickable external
// link**; while `![](http://attacker.tld/beacon)` would be fetched by the client at
// render time, effectively announcing "this finding has been viewed" and leaking the
// reader's IP. Even with benign content, injected bold or blockquote can push a
// serious finding below the fold.
//
// The escape set covers the title/link/emphasis/list/quote/strikethrough characters
// that would change structure or produce clickable elements. `\` must be handled
// first, otherwise it would re-escape the backslashes added afterward.
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText compresses untrusted text to a single line and escapes it, for use in
// markdown bodies.
// Single-lining is the other half of escaping: a newline alone can forge a new list
// item or blockquote, and escaping characters will not stop it.
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle returns the message title (the title bar / card title of the IM
// platform); the content is the **unescaped original text**.
//
// Escaping is deliberately not done here: this title is shared by four rendering
// contexts -- the markdown body, Telegram's HTML, the Feishu card's plain_text, and
// the generic Webhook's JSON and the email subject. Each context has different
// escaping rules (markdown escapes stuffed into HTML leave visible backslashes, and
// stuffed into JSON corrupt the data), so escaping must be the responsibility of each
// output side, see writeItem / feishuItemLines / telegramEscape. Markdown escaping
// was once added in the shared function, and the result was visible backslashes like
// `\(1\)` appearing in Telegram messages.
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("Finding digest · %d total", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "Finding notification"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody renders the message body, returning the body and the **number of items
// actually written**.
//
// The return value kept is the number of items truly delivered by this send, from
// which the caller marks only the first kept items as delivered -- items kept out by
// the channel's length limit must wait for the next batch rather than being marked
// successful along with the rest. This is exactly the source of "silent loss": the
// message was truncated, but the delivery record shows everything delivered, with no
// place showing that the second half was never sent.
//
// maxBytes<=0 means no limit.
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		// A single message is sent even when over-length (the final truncation is the
		// safety net): partial information about one finding is better than sending
		// nothing.
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[View all on platform](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro renders the start of a digest message: time window, item count,
// and severity distribution. With these, the recipient of a digest can decide whether
// this batch needs immediate attention without clicking into the platform.
//
// items are the items **actually packed**, and total is the batch's intended total.
// When the two differ, it must state "how many more items are in the next message" --
// otherwise the reader would assume the number in the header is the whole set, while
// those never-sent items simply do not exist in the UI.
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**In the last %d minutes: %d new findings**", m.WindowMinutes, total)
	} else {
		fmt.Fprintf(&b, "**%d new findings**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, " (showing first %d, %d remaining in the next message)", len(items), extra)
	}
	// Give the distribution by severity, so the reader sees at a glance whether there
	// are any critical items. Only count the items **actually included in this
	// message**, so "Critical 3" matches the items the reader can count below.
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem renders a single finding item.
//
// prefix is used for the index in a digest list; when single=true it renders the full
// version (with summary and back-link), while a digest list renders only a one-line
// summary -- otherwise a 50-item digest would become a long document.
//
// All externally-sourced content (title/type/asset/summary) goes through
// markdownText: single-line + escape. The back-link is assembled from the
// admin-configured public_base_url, is not untrusted content, and must be a clickable
// link, so it is output as-is.
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		// Digest mode: one-line presentation, with assets and summary compressed and
		// appended.
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " - " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**Status change**: %s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**Type**: %s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**Assets**: %s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**Summary**: %s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[View details](%s)\n", it.DetailURL)
	}
}
