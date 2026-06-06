package dialog

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
)

func TestText_TruncatesWideLineWithEllipsis(t *testing.T) {
	// A zero Theme renders without ANSI escapes, so the laid-out text can be
	// asserted on directly.
	d := &Text{
		DialogID: "x",
		Title:    "Title",
		Body:     strings.Repeat("a", 200),
		Theme:    styles.Theme{},
	}
	out := d.Render(20)
	var longest string
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > len([]rune(longest)) {
			longest = line
		}
	}
	if len([]rune(longest)) > 16 {
		t.Fatalf("line exceeded clamp width: %q (%d runes)", longest, len([]rune(longest)))
	}
	if !strings.HasSuffix(longest, "…") {
		t.Fatalf("truncated line should end in an ellipsis, got %q", longest)
	}
}

func TestClampLines_ShortLineUnchanged(t *testing.T) {
	in := "short\nlines"
	if got := clampLines(in, 40); got != in {
		t.Fatalf("clampLines altered a line that fit: %q", got)
	}
}

func TestClampLines_AddsEllipsisOnlyWhenCut(t *testing.T) {
	got := clampLines("ab\nabcdef", 4)
	lines := strings.Split(got, "\n")
	if lines[0] != "ab" {
		t.Fatalf("line that fit was changed: %q", lines[0])
	}
	if lines[1] != "abc…" {
		t.Fatalf("wide line not truncated with ellipsis: %q", lines[1])
	}
	if n := len([]rune(lines[1])); n != 4 {
		t.Fatalf("truncated line should be exactly width runes, got %d", n)
	}
}

func TestClampLines_WidthOne(t *testing.T) {
	if got := clampLines("abcdef", 1); got != "…" {
		t.Fatalf("at width 1 a wide line should become a lone ellipsis, got %q", got)
	}
}

func TestClampLines_NonPositiveWidthUntouched(t *testing.T) {
	in := "abcdef"
	if got := clampLines(in, 0); got != in {
		t.Fatalf("non-positive width should leave the line untouched, got %q", got)
	}
}
