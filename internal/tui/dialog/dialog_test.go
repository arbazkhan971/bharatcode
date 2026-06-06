package dialog

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
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

// makeBody returns a multi-line body with the given number of "lineN" entries.
func makeBody(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line" + string(rune('0'+i+1)) // line1…line9 (works for n≤9)
		if i >= 9 {
			lines[i] = "lineX" // fallback for larger bodies
		}
	}
	return strings.Join(lines, "\n")
}

// keyCode constructs a KeyPressMsg for a special key code so tests can drive
// HandleKey without going through the full Bubble Tea update loop.
func keyCode(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

// TestScrollableText_RendersFirstWindowOnFirstRender asserts that the first
// Render call shows the top of the body and hides lines beyond visibleRows.
func TestScrollableText_RendersFirstWindowOnFirstRender(t *testing.T) {
	// Height 13 → visibleRows = 13-7 = 6.
	body := makeBody(9) // 9 lines: line1…line9
	d := &ScrollableText{DialogID: "x", Title: "T", Body: body, Theme: styles.Theme{}, Height: 13}
	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("first body line must appear in initial render, got:\n%s", out)
	}
	if !strings.Contains(out, "line6") {
		t.Fatalf("line6 (last of first window) must appear in initial render")
	}
	if strings.Contains(out, "line7") {
		t.Fatalf("line7 must not appear in initial render (beyond visible window)")
	}
}

// TestScrollableText_ShowsScrollHintWhenBodyExceedsViewport asserts that a
// "below" scroll hint appears when the body is taller than visibleRows.
func TestScrollableText_ShowsScrollHintWhenBodyExceedsViewport(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	out := d.Render(200)
	if !strings.Contains(out, "↓") {
		t.Fatalf("scroll hint must include ↓ when body extends below visible window, got:\n%s", out)
	}
}

// TestScrollableText_NoScrollHintWhenBodyFits asserts that no scroll hint is
// shown when the body fits entirely within the visible window.
func TestScrollableText_NoScrollHintWhenBodyFits(t *testing.T) {
	// Height 13 → visibleRows = 6; body has only 3 lines.
	d := &ScrollableText{DialogID: "x", Title: "T", Body: "a\nb\nc", Theme: styles.Theme{}, Height: 13}
	out := d.Render(200)
	if strings.Contains(out, "↓") || strings.Contains(out, "↑") {
		t.Fatalf("scroll hint must not appear when body fits within the window, got:\n%s", out)
	}
}

// TestScrollableText_DownKeyScrollsBody asserts that pressing Down advances the
// visible window by one line so the next line appears and the first disappears.
func TestScrollableText_DownKeyScrollsBody(t *testing.T) {
	// Height 13 → visibleRows = 6; body has 9 lines (line1…line9).
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200) // initialise

	handled, pop := d.HandleKey(keyCode(tea.KeyDown))
	if !handled {
		t.Fatal("Down key must be handled")
	}
	if pop {
		t.Fatal("Down key must not pop the dialog")
	}

	out := d.Render(200)
	if strings.Contains(out, "line1") {
		t.Fatalf("line1 must scroll off the top after one Down, got:\n%s", out)
	}
	if !strings.Contains(out, "line7") {
		t.Fatalf("line7 must appear after one Down, got:\n%s", out)
	}
}

// TestScrollableText_UpKeyScrollsBodyBack asserts that pressing Up after Down
// brings the view back to the starting position.
func TestScrollableText_UpKeyScrollsBodyBack(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	d.HandleKey(keyCode(tea.KeyDown))
	d.HandleKey(keyCode(tea.KeyUp))

	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must reappear after Down+Up, got:\n%s", out)
	}
}

// TestScrollableText_PgDownScrollsByVisibleRows asserts that PgDown advances
// the view by visibleRows lines at once, clamped to the maximum scroll offset.
func TestScrollableText_PgDownScrollsByVisibleRows(t *testing.T) {
	// Height 13 → visibleRows 6; body 9 lines → maxScroll = 9-6 = 3.
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	handled, pop := d.HandleKey(keyCode(tea.KeyPgDown))
	if !handled || pop {
		t.Fatalf("PgDown must be handled without popping; handled=%v pop=%v", handled, pop)
	}
	// visibleRows=6 > maxScroll=3, so scroll clamps to maxScroll=3.
	out := d.Render(200)
	if !strings.Contains(out, "line9") {
		t.Fatalf("line9 (last line) must appear after PgDown clamps to end, got:\n%s", out)
	}
}

// TestScrollableText_PgUpAtTopIsNoop asserts that pressing PgUp when already
// at the top does not change the view or pop the dialog.
func TestScrollableText_PgUpAtTopIsNoop(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	handled, pop := d.HandleKey(keyCode(tea.KeyPgUp))
	if !handled || pop {
		t.Fatalf("PgUp at top must be handled without popping; handled=%v pop=%v", handled, pop)
	}
	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must still be visible at top after PgUp noop, got:\n%s", out)
	}
}

// TestScrollableText_HomeEndJumpToExtremes asserts that Home resets scroll to 0
// and End jumps to the maximum offset so the last lines are visible.
func TestScrollableText_HomeEndJumpToExtremes(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)

	d.HandleKey(keyCode(tea.KeyEnd))
	out := d.Render(200)
	if !strings.Contains(out, "line9") {
		t.Fatalf("line9 must appear after End, got:\n%s", out)
	}
	// line1 should now be scrolled off (maxScroll=3, so lines 4–9 are visible).
	if strings.Contains(out, "line1") {
		t.Fatalf("line1 must not be visible after End, got:\n%s", out)
	}

	d.HandleKey(keyCode(tea.KeyHome))
	out = d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must reappear after Home, got:\n%s", out)
	}
}

// TestScrollableText_EscPopsDialog asserts that Esc signals the dialog stack
// to pop the dialog.
func TestScrollableText_EscPopsDialog(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: "hello", Theme: styles.Theme{}, Height: 13}
	handled, pop := d.HandleKey(keyCode(tea.KeyEsc))
	if !handled {
		t.Fatal("Esc must be handled")
	}
	if !pop {
		t.Fatal("Esc must signal pop=true")
	}
}

// TestScrollableText_EnterPopsDialog asserts that Enter also dismisses the dialog.
func TestScrollableText_EnterPopsDialog(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: "hello", Theme: styles.Theme{}, Height: 13}
	handled, pop := d.HandleKey(keyCode(tea.KeyEnter))
	if !handled || !pop {
		t.Fatalf("Enter must pop dialog; handled=%v pop=%v", handled, pop)
	}
}

// TestScrollableText_UpAboveSameScrollHintAppears asserts that once the view
// is scrolled down, both "↑ N above" and "↓ N below" hints are shown.
func TestScrollableText_UpAboveSameScrollHintAppears(t *testing.T) {
	// Height 13 → visibleRows 6; body 9 lines.
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	d.HandleKey(keyCode(tea.KeyDown)) // scroll=1

	out := d.Render(200)
	if !strings.Contains(out, "↑") {
		t.Fatalf("↑ hint must appear when content above the window exists, got:\n%s", out)
	}
	if !strings.Contains(out, "↓") {
		t.Fatalf("↓ hint must appear when content below the window exists, got:\n%s", out)
	}
}

// TestScrollableText_ZeroHeightFallback asserts that a zero Height still
// renders without panicking, falling back to the default visibleRows cap.
func TestScrollableText_ZeroHeightFallback(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 0}
	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must appear even with Height=0 fallback, got:\n%s", out)
	}
}
