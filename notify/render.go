package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes truncates s to no more than max bytes, guaranteeing the result is
// valid UTF-8 and does not cut a character in half.
//
// Why it must cut on a character boundary: the WeCom group bot markdown has a 4096
// **byte** hard limit (not character count), and a CJK character is 3 bytes. Slicing
// by byte directly would cut a character in half, producing invalid UTF-8 -- which
// the platform either rejects whole or displays as garbled squares. The approach here
// is to first back up from the budget position to the nearest rune start byte
// (utf8.RuneStart identifies continuation bytes 0b10xxxxxx).
//
// max<=0 means no limit. An ellipsis is appended after truncation, unless max is too
// small to fit the ellipsis.
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		// max is shorter than the ellipsis: drop the ellipsis and truncate purely, to
		// avoid the result exceeding max.
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine compresses multi-line text to a single line: it collapses all whitespace,
// then truncates by character count.
// Used for IM message title lines -- summaries often contain newlines, and stuffing
// them directly into a table/title breaks the layout.
// max<=0 means no length limit.
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes truncates s to no more than max characters (not bytes), appending an
// ellipsis when exceeded.
// max<=0 means no limit.
//
// The difference from TruncateBytes is the platform's unit: WeCom limits by bytes,
// Telegram by character count. Using the wrong unit does not error, it just cuts the
// message far shorter than expected (a CJK char = 3 bytes, so byte-cutting at 4096
// leaves only about 1365 chars), so both functions must be kept and chosen per
// channel.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML truncates an HTML fragment by character count, guaranteeing it does not
// produce a half tag.
//
// Truncating HTML by character directly could cut out `<a href="htt`, a broken tag,
// and the platform parser either rejects the whole message or swallows the following
// body as an attribute value. The approach here is: first truncate by character, then
// check whether the tail has an unclosed `<`, and if so back off to before it.
//
// Tag balancing (completing a `</b>` and the like) is not done: Telegram's HTML parser
// auto-closes unclosed tags, and implementing balancing oneself would need to handle
// quotes in attributes, comments, and self-closing tags -- a complexity disproportionate
// to the benefit.
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	// If the tail is a fragment starting with `<` (the last `<` has no `>` after it),
	// back off to before the `<`.
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	// If the tail is a cut-off HTML entity (such as `&amp;` cut to `&amp`), likewise
	// back off. An entity fragment can make a strict entity-only parser reject the
	// **whole message** -- a digest message exceeding the length limit is common
	// enough that it is not worth losing the whole notification over.
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount computes how many items can be **completely** fit within the budget,
// for packing digest messages whole.
//
// Why pack whole items rather than render the full document then truncate: truncation
// makes the second half of the items vanish, while their delivery records are still
// marked delivered -- invisible in the message and shown as success in the delivery
// history, and the findings are simply gone. Packing whole items leaves the ones that
// do not fit in the store as the next batch, and the kept the caller receives is the
// number truly delivered by this message.
//
// Parameters: maxSize<=0 means no limit; reserve is the amount reserved for the
// message header/footer;
// size handles measurement (platforms differ in unit: WeCom/DingTalk by bytes,
// Telegram by character count -- using the wrong unit does not error, it just
// compresses a CJK message far below the limit);
// render renders item idx to its actual text -- the length varies by content and
// cannot be estimated.
//
// Returns at least 1 (as long as there are items). A single extremely over-length item
// is still sent, with the caller's final truncation as the safety net, otherwise one
// over-length finding would permanently stall the whole batch.
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize are packItemCount's two measurement units, named out to avoid a
// bare func(s string) int closure at the call sites, which makes it hard to tell at a
// glance which unit is in use.
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine renders the asset list into a one-line display text, omitting the rest and
// annotating the total when there are more than limit.
// A single finding may anchor dozens of assets, and listing them all overflows the
// message.
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, ", ")
	}
	return strings.Join(assets[:limit], ", ") + " (" + itoa(len(assets)) + " total)"
}

// itoa is a short alias for strconv.Itoa, used only for assembling display text, to
// avoid importing strconv everywhere.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
