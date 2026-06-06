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

// TestTruncateMarksClip asserts a status line wider than the window is clipped
// to exactly width runes and ends in an ellipsis, signalling that trailing
// segments were hidden rather than silently dropped.
func TestTruncateMarksClip(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hello", truncateLine("hello", 10), "a line within width is unchanged")
	require.Equal(t, "hello", truncateLine("hello", 5), "a line exactly at width keeps its last rune")

	got := truncateLine("hello world", 8)
	require.Equal(t, 8, len([]rune(got)), "the clipped line occupies exactly width runes")
	require.Equal(t, "hello w…", got, "the final rune becomes an ellipsis")

	require.Equal(t, "…", truncateLine("hello", 1), "at width 1 the lone cell is the ellipsis")
	require.Equal(t, "hello", truncateLine("hello", 0), "a non-positive width is treated as unbounded")
}

// TestNarrowBarKeepsLiveProgress asserts that when the window is too narrow for
// the whole bar, the live working segment survives while the static identity
// fields (session id, uptime) are dropped first — so a user watching a running
// turn keeps the progress readout instead of losing it to a tail clip.
func TestNarrowBarKeepsLiveProgress(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{
		Theme:     styles.Default(),
		Model:     "m",
		Agent:     "a",
		SessionID: "id",
		StartedAt: start,
		Now:       start,
		Working:   "⠙ working 3s",
	}

	// Wide enough that everything fits: the bar is the plain joined form.
	full := bar.Render(160)
	require.Contains(t, full, "session id")
	require.Contains(t, full, "⠙ working 3s")

	// Narrow enough that not every field fits: the live working segment is kept
	// and the lower-priority identity fields are shed rather than the spinner.
	narrow := bar.Render(20)
	require.Contains(t, narrow, "⠙ working 3s", "the live working segment must survive a narrow window")
	require.NotContains(t, narrow, "session", "the session id is dropped before the live progress")
	require.NotContains(t, narrow, "up ", "the uptime is dropped before the live progress")
	require.NotContains(t, narrow, "…", "dropping whole segments avoids an ellipsis clip")
}

// TestNarrowBarRanksSegments asserts the drop order follows segment priority:
// search outranks the scroll position, which outranks the static identity
// fields, so the most useful field for the current moment is the last to go.
func TestNarrowBarRanksSegments(t *testing.T) {
	t.Parallel()

	segs := []segment{
		{"model", prioModel},
		{"agent", prioAgent},
		{"session abcd", prioSession},
		{"up 5s", prioUptime},
		{"search 2/7", prioSearch},
		{"↓ 9 below", prioScroll},
	}

	// Room for the model anchor plus exactly the highest-priority extra.
	got := fitSegments(segs, len([]rune("model · search 2/7")))
	require.Equal(t, "model · search 2/7", got, "search outranks scroll and the identity fields")

	// A non-positive width is unbounded: every segment is kept in order.
	require.Equal(t, "model · agent · session abcd · up 5s · search 2/7 · ↓ 9 below", fitSegments(segs, 0))

	// When only the anchor fits, it is returned whole for the caller to clip.
	require.Equal(t, "model", fitSegments(segs, 3))
}

// TestRenderClipsWideBar asserts Render routes through the ellipsis truncation,
// so a long line surfaces the marker and a non-positive width stays unbounded.
func TestRenderClipsWideBar(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "some-very-long-model-name", Agent: "coder", SessionID: "abcdef123456", StartedAt: start, Now: start, Scroll: "↓ 12 below"}

	require.Contains(t, bar.Render(20), "…", "a bar wider than the window is marked as clipped")
	require.NotContains(t, bar.Render(0), "…", "an unbounded render adds no marker")
}
