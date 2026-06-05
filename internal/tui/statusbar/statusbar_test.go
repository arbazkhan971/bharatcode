package statusbar

import (
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

func TestFieldsPresent(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{
		Theme:     styles.Default(),
		Model:     "kimi-k2",
		Agent:     "coder",
		SessionID: "abcdef123456",
		StartedAt: start,
		Now:       start.Add(time.Second),
	}
	first := bar.Render(160)
	bar.Now = start.Add(2 * time.Second)
	second := bar.Render(160)

	require.Contains(t, first, "kimi-k2")
	require.Contains(t, first, "coder")
	require.Contains(t, first, "abcdef12")
	require.Contains(t, first, "1s")
	require.Contains(t, second, "2s")
}

// TestSearchSegment asserts the search-progress segment appears only when set,
// so the bar is byte-identical to its no-search form until a search is active.
func TestSearchSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), "search", "an empty Search must add no segment")

	bar.Search = "search 2/7"
	require.Contains(t, bar.Render(160), "search 2/7", "a set Search must surface its segment")
}

// TestWorkingSegment asserts the working-progress segment appears only when
// set, so the bar is byte-identical to its idle form until a turn is running.
func TestWorkingSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), "working", "an empty Working must add no segment")

	bar.Working = "⠙ working 3s"
	require.Contains(t, bar.Render(160), "⠙ working 3s", "a set Working must surface its segment")
}

// TestScrollSegment asserts the scrollback-position segment appears only when
// set, so the bar is unchanged while the chat view is anchored to the bottom.
func TestScrollSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), "below", "an empty Scroll must add no segment")

	bar.Scroll = "↓ 12 below"
	require.Contains(t, bar.Render(160), "↓ 12 below", "a set Scroll must surface its segment")
}
