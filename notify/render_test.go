package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// This is the most important invariant in the package. WeCom limits by **bytes**,
	// and a multi-byte character (3 bytes for CJK, up to 4 for emoji) is easily cut in
	// half by any naive byte-slicing implementation, producing invalid UTF-8 that the
	// platform rejects.
	// Hit every possible cut point with mixed-width ASCII/multi-byte inputs of
	// coprime lengths.
	inputs := []string{
		"café résumé naïve",
		"mixed café content ✓",
		"aßbécàdëeø",
		"🔴🟠🟡🔵", // 4-byte emoji, where a wrong cut is more obvious
		strings.Repeat("✓", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("input %q max=%d: produced invalid UTF-8 %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("input %q max=%d: result %d bytes exceeds the limit", in, max, len(got))
			}
			// When not truncated, the content must not be changed.
			if len(in) <= max && got != in {
				t.Fatalf("input %q max=%d: content changed despite being under the limit -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 should mean no limit")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 should mean no limit")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// When max is smaller than the ellipsis itself, appending the ellipsis must not
	// make the result exceed the limit.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1: result %q length %d exceeds the limit", got, len(got))
	}
	// Normally it should carry the ellipsis.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("expected the ellipsis, got %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// The difference in unit from TruncateBytes must be preserved: Telegram limits by
	// character, and the byte unit would cut a CJK message to a third.
	s := strings.Repeat("✓", 10) // 10 runes, 3 bytes each
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("expected 5 characters, got %d (%q)", n, got)
	}
	// The same string under the byte unit should be noticeably shorter.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("the byte unit should not produce the same character count as the rune unit")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("line one\n\nline two\twith tabs   multiple spaces", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("should collapse all whitespace, got %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("should not keep consecutive spaces, got %q", got)
	}
	// It must stay readable and valid after truncation.
	got = OneLine(strings.Repeat("✓", 10), 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("expected 4 characters, got %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Truncating HTML directly could cut out `<a href="htt`, a fragment that makes the
	// platform reject the whole message.
	s := `<b>Title</b>body body body<a href="https://example.com/very/long/path">View details</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: result %d chars exceeds the limit", max, n)
		}
		// The tail must not have an unclosed `<` (i.e. a `<` in the last segment with
		// no `>`).
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: tail tag was cut off -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("no assets should return an empty string, got %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("under the limit all should be listed, got %q", got)
	}
	// When over the limit, the total must be annotated, or the reader does not know how
	// many assets were not listed.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "5 total") {
		t.Fatalf("should annotate the total 5, got %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("an empty severity has ordinal 0 and should be blocked by any threshold")
	}
	if !AtLeast("critical", "") {
		t.Fatal("an empty threshold should let everything pass")
	}
	if got := StatusLabel("fixed"); got != "Fixed" {
		t.Fatalf("wrong status mapping, got %q", got)
	}
	// An unknown status is echoed back as-is, with no invented label.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("an unknown status should be echoed back as-is, got %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity covers an omission the audit pointed out: truncation
// must avoid not only a half tag, but also a cut-off HTML entity.
//
// After `&amp;` is cut to `&amp`, a strict entity-only parser may reject the **whole**
// message -- and an over-length digest message is common, so the cost is too high.
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// The tail must not leave an entity fragment with "a & but no matching ;".
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: tail left an entity fragment %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: a malformed entity appeared", max)
		}
	}
}
