package dialog

import (
	"fmt"
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

// keyChar constructs a KeyPressMsg for a regular printable character key such
// as 'j', 'k', 'g', 'G'. Both Code and Text are set to match the encoding
// Bubble Tea uses for printable keys, so msg.String() returns the character.
func keyChar(c rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: c, Text: string(c)})
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

// TestScrollableText_VimJ_ScrollsDownOneLine asserts that pressing 'j' advances
// the visible window by one line, mirroring the Down-arrow binding. The vim
// alias lets power users navigate the diff and /keys overlays without reaching
// for arrow keys, matching the navigation style of Claude Code and opencode.
func TestScrollableText_VimJ_ScrollsDownOneLine(t *testing.T) {
	// Height 13 → visibleRows 6; body 9 lines.
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)

	handled, pop := d.HandleKey(keyChar('j'))
	if !handled {
		t.Fatal("j must be handled")
	}
	if pop {
		t.Fatal("j must not pop the dialog")
	}

	out := d.Render(200)
	if strings.Contains(out, "line1") {
		t.Fatalf("line1 must scroll off the top after j, got:\n%s", out)
	}
	if !strings.Contains(out, "line7") {
		t.Fatalf("line7 must appear after j, got:\n%s", out)
	}
}

// TestScrollableText_VimK_ScrollsUpOneLine asserts that pressing 'k' steps the
// view back by one line, mirroring the Up-arrow binding.
func TestScrollableText_VimK_ScrollsUpOneLine(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	d.HandleKey(keyChar('j')) // scroll down first
	d.HandleKey(keyChar('k')) // then back up

	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must reappear after j+k, got:\n%s", out)
	}
}

// TestScrollableText_VimK_AtTopIsNoop asserts that pressing 'k' when already
// at the top does not change the view or pop the dialog.
func TestScrollableText_VimK_AtTopIsNoop(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)

	handled, pop := d.HandleKey(keyChar('k'))
	if !handled || pop {
		t.Fatalf("k at top must be handled without popping; handled=%v pop=%v", handled, pop)
	}
	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must still be visible after k at top, got:\n%s", out)
	}
}

// TestScrollableText_VimG_JumpsToTop asserts that pressing 'g' resets scroll to
// zero, mirroring the Home-key binding and the vim convention for going to the
// start of a buffer.
func TestScrollableText_VimG_JumpsToTop(t *testing.T) {
	// Height 13 → visibleRows 6; body 9 lines → maxScroll 3.
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)
	d.HandleKey(keyCode(tea.KeyEnd)) // jump to bottom first

	handled, pop := d.HandleKey(keyChar('g'))
	if !handled || pop {
		t.Fatalf("g must be handled without popping; handled=%v pop=%v", handled, pop)
	}
	out := d.Render(200)
	if !strings.Contains(out, "line1") {
		t.Fatalf("line1 must reappear after g (jump to top), got:\n%s", out)
	}
}

// TestScrollableText_VimG_Upper_JumpsToBottom asserts that pressing 'G' jumps
// to the maximum scroll offset, mirroring the End-key binding and the vim
// convention for going to the end of a buffer.
func TestScrollableText_VimG_Upper_JumpsToBottom(t *testing.T) {
	// Height 13 → visibleRows 6; body 9 lines → maxScroll 3 (lines 4–9 visible).
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(9), Theme: styles.Theme{}, Height: 13}
	d.Render(200)

	handled, pop := d.HandleKey(keyChar('G'))
	if !handled || pop {
		t.Fatalf("G must be handled without popping; handled=%v pop=%v", handled, pop)
	}
	out := d.Render(200)
	if !strings.Contains(out, "line9") {
		t.Fatalf("line9 must appear after G (jump to bottom), got:\n%s", out)
	}
	if strings.Contains(out, "line1") {
		t.Fatalf("line1 must not be visible after G, got:\n%s", out)
	}
}

// TestScrollableText_YKey_NoCopyFn asserts that pressing 'y' without a CopyFn
// is handled silently — the dialog stays open and no copy is attempted.
func TestScrollableText_YKey_NoCopyFn(t *testing.T) {
	d := &ScrollableText{DialogID: "x", Title: "T", Body: makeBody(3), Theme: styles.Theme{}, Height: 13}
	handled, pop := d.HandleKey(keyChar('y'))
	if !handled {
		t.Fatal("y must be handled even without CopyFn")
	}
	if pop {
		t.Fatal("y must not pop the dialog")
	}
	// No copyMsg should be set when CopyFn is nil.
	if d.copyMsg != "" {
		t.Fatalf("copyMsg must remain empty without CopyFn, got %q", d.copyMsg)
	}
}

// TestScrollableText_YKey_CallsCopyFn asserts that pressing 'y' invokes CopyFn
// and that a "Copied!" status message is written to copyMsg on success.
func TestScrollableText_YKey_CallsCopyFn(t *testing.T) {
	var got string
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     makeBody(3),
		Theme:    styles.Theme{},
		Height:   13,
		CopyFn:   func() error { got = "called"; return nil },
	}
	handled, pop := d.HandleKey(keyChar('y'))
	if !handled {
		t.Fatal("y must be handled when CopyFn is set")
	}
	if pop {
		t.Fatal("y must not pop the dialog")
	}
	if got != "called" {
		t.Fatal("CopyFn must be called on y press")
	}
	if d.copyMsg != "Copied!" {
		t.Fatalf("copyMsg must be \"Copied!\" on success, got %q", d.copyMsg)
	}
}

// TestScrollableText_YKey_CopyFnError asserts that a failing CopyFn stores an
// error description in copyMsg rather than silently succeeding.
func TestScrollableText_YKey_CopyFnError(t *testing.T) {
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     makeBody(3),
		Theme:    styles.Theme{},
		Height:   13,
		CopyFn:   func() error { return fmt.Errorf("no clipboard") },
	}
	d.HandleKey(keyChar('y'))
	if !strings.Contains(d.copyMsg, "no clipboard") {
		t.Fatalf("copyMsg must contain the error, got %q", d.copyMsg)
	}
}

// TestScrollableText_CopyMsgClearedOnRender asserts that copyMsg is shown once
// in the render output and then cleared so subsequent renders show the normal
// scroll hint rather than a stale copy status.
func TestScrollableText_CopyMsgClearedOnRender(t *testing.T) {
	var copied string
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     makeBody(20),
		Theme:    styles.Theme{},
		Height:   13,
		CopyFn:   func() error { copied = "yes"; return nil },
	}
	d.HandleKey(keyChar('y'))
	if copied != "yes" {
		t.Fatal("CopyFn must have been called")
	}
	first := d.Render(200)
	if !strings.Contains(first, "Copied!") {
		t.Fatalf("first render must show Copied!, got:\n%s", first)
	}
	second := d.Render(200)
	if strings.Contains(second, "Copied!") {
		t.Fatalf("second render must not show Copied! after it was cleared, got:\n%s", second)
	}
}

// TestScrollableText_CopyHintInScrollFooter asserts that the scroll footer
// includes "y copy" when CopyFn is set and the body overflows the viewport.
func TestScrollableText_CopyHintInScrollFooter(t *testing.T) {
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     makeBody(20),
		Theme:    styles.Theme{},
		Height:   13,
		CopyFn:   func() error { return nil },
	}
	out := d.Render(200)
	if !strings.Contains(out, "y copy") {
		t.Fatalf("scroll footer must include 'y copy' when CopyFn is set, got:\n%s", out)
	}
}

// TestScrollableText_NoCopyHintWithoutCopyFn asserts that the scroll footer
// does NOT include "y copy" when CopyFn is nil.
func TestScrollableText_NoCopyHintWithoutCopyFn(t *testing.T) {
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     makeBody(20),
		Theme:    styles.Theme{},
		Height:   13,
	}
	out := d.Render(200)
	if strings.Contains(out, "y copy") {
		t.Fatalf("scroll footer must not include 'y copy' without CopyFn, got:\n%s", out)
	}
}

// TestScrollableText_CopyHintWhenBodyFits asserts that the "y copy · Esc close"
// hint is appended even when the body fits entirely on screen (no scroll needed).
func TestScrollableText_CopyHintWhenBodyFits(t *testing.T) {
	d := &ScrollableText{
		DialogID: "x",
		Title:    "T",
		Body:     "short",
		Theme:    styles.Theme{},
		Height:   30,
		CopyFn:   func() error { return nil },
	}
	out := d.Render(200)
	if !strings.Contains(out, "y copy") {
		t.Fatalf("short body with CopyFn must still show 'y copy' hint, got:\n%s", out)
	}
}
