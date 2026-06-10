package sidebar

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

func testInfo() Info {
	return Info{
		Theme:    styles.Default(),
		Model:    "kimi-k2",
		Provider: "moonshot",
		Cwd:      "~/code/app",
		Yolo:     true,
		Changed:  3,
	}
}

// TestInfo_RendersAllSegments proves every populated field reaches the strip:
// model, provider, cwd, the yolo marker, and the changed-file count.
func TestInfo_RendersAllSegments(t *testing.T) {
	t.Parallel()

	out := testInfo().Render(120)
	require.Contains(t, out, "kimi-k2")
	require.Contains(t, out, "moonshot")
	require.Contains(t, out, "~/code/app")
	require.Contains(t, out, "yolo")
	require.Contains(t, out, "3 changed")
}

// TestInfo_OmitsEmptyAndZeroFields proves the strip drops segments with no data
// — an unset provider/cwd, yolo off, and a zero change count all vanish rather
// than rendering empty separators.
func TestInfo_OmitsEmptyAndZeroFields(t *testing.T) {
	t.Parallel()

	out := Info{Theme: styles.Default(), Model: "kimi-k2"}.Render(120)
	require.Contains(t, out, "kimi-k2")
	require.NotContains(t, out, "yolo", "yolo off hides the marker")
	require.NotContains(t, out, "changed", "a zero change count hides the segment")
	require.NotContains(t, out, " · ", "a single segment has no separators")
}

// TestInfo_EmptyWhenNothingSet proves a zero-value Info renders an empty line
// rather than a row of separators.
func TestInfo_EmptyWhenNothingSet(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", Info{Theme: styles.Default()}.Render(120))
}

// TestInfo_ClampsToWidth proves an over-wide strip is clamped to the terminal
// width with a trailing ellipsis, so it never wraps onto a second row and breaks
// the rigid header budget. The visible (ANSI-stripped) line is measured.
func TestInfo_ClampsToWidth(t *testing.T) {
	t.Parallel()

	out := testInfo().Render(12)
	plain := stripANSI(out)
	require.LessOrEqual(t, len([]rune(plain)), 12, "the clamped line fits the width")
	require.Contains(t, plain, "…", "truncation is marked with an ellipsis")
}

// stripANSI removes SGR escape sequences so a test can measure printable width.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
			// skip escape body
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
