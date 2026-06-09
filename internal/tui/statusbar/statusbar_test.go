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

// TestContextPctSegment asserts the context-window fill segment appears only
// when ContextPct > 0, is absent on a fresh bar (zero default), and surfaces
// as "ctx N%" once set — matching the idle/active toggle pattern of TurnTokens
// and Working so the bar is unchanged until real usage data arrives.
func TestContextPctSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), "ctx ", "a zero ContextPct must add no segment")

	bar.ContextPct = 35
	require.Contains(t, bar.Render(160), "ctx 35%", "a set ContextPct must show ctx N%")

	bar.ContextPct = 72
	require.Contains(t, bar.Render(160), "ctx 72%", "updating ContextPct must reflect the new value")

	bar.ContextPct = 0
	require.NotContains(t, bar.Render(160), "ctx ", "resetting ContextPct to zero must remove the segment")
}

// TestContextPctSegment_Priority asserts the context-pct segment outranks the
// turn-token segment (prioContextPct > prioTurnTokens), so a narrow window
// keeps the context indicator — which guides compaction decisions — and drops
// the turn counts first.
func TestContextPctSegment_Priority(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{
		Theme:      styles.Default(),
		Model:      "m",
		Agent:      "a",
		SessionID:  "id",
		StartedAt:  start,
		Now:        start,
		TurnTokens: "1.2k in · 234 out",
		ContextPct: 45,
	}

	// Wide enough for both: both must appear.
	full := bar.Render(160)
	require.Contains(t, full, "1.2k in · 234 out", "turn tokens must appear on a wide bar")
	require.Contains(t, full, "ctx 45%", "context pct must appear on a wide bar")

	// Size to just the model anchor plus the context segment: the lower-priority
	// turn-tokens segment must be shed to make room for ctx N%.
	narrow := bar.Render(len([]rune("m · ctx 45%")))
	require.Contains(t, narrow, "ctx 45%", "context pct must survive a narrow window")
	require.NotContains(t, narrow, " in · ", "turn tokens must be dropped before the context indicator")
}

// TestContextPctSegment asserts the turn-token segment appears only when set,
// so the bar is unchanged until a completed turn provides usage data — and
// disappears cleanly once cleared (e.g. when a new turn starts).
func TestTurnTokensSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), " in · ", "an empty TurnTokens must add no segment")

	bar.TurnTokens = "1.2k in · 234 out"
	require.Contains(t, bar.Render(160), "1.2k in · 234 out",
		"a set TurnTokens must surface its segment")

	bar.TurnTokens = ""
	require.NotContains(t, bar.Render(160), " in · ",
		"clearing TurnTokens must remove the segment")
}

// TestTurnTokensSegment_Priority asserts the turn-token segment is shed before
// the model anchor but after the working indicator, so a narrow window keeps
// the live spinner and drops the idle-turn token counts first.
func TestTurnTokensSegment_Priority(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{
		Theme:      styles.Default(),
		Model:      "m",
		Agent:      "a",
		SessionID:  "id",
		StartedAt:  start,
		Now:        start,
		Working:    "⠙ working 3s",
		TurnTokens: "1.2k in · 234 out",
	}

	// Wide enough for both: both must appear.
	full := bar.Render(160)
	require.Contains(t, full, "⠙ working 3s", "working must appear when bar is wide")
	require.Contains(t, full, "1.2k in · 234 out", "turn tokens must appear when bar is wide")

	// Narrow enough that only the anchor plus working fits: working must survive
	// and the lower-priority token segment must be shed.
	narrow := bar.Render(len([]rune("m · ⠙ working 3s")))
	require.Contains(t, narrow, "⠙ working 3s", "the working segment must outlast the token counts")
	require.NotContains(t, narrow, " in · ", "the token segment must be shed before the working indicator")
}

// TestInputTokensSegment asserts the input-token-count segment appears only when
// set, so the bar is byte-identical to its no-input form until the user starts
// typing — and surfaces as the supplied string once set.
func TestInputTokensSegment(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}
	require.NotContains(t, bar.Render(160), "tok", "an empty InputTokens must add no segment")

	bar.InputTokens = "~128 tok"
	require.Contains(t, bar.Render(160), "~128 tok", "a set InputTokens must surface its segment")

	bar.InputTokens = "~1 tok"
	require.Contains(t, bar.Render(160), "~1 tok", "updating InputTokens must reflect the new value")

	bar.InputTokens = ""
	require.NotContains(t, bar.Render(160), "tok", "clearing InputTokens must remove the segment")
}

// TestInputTokensSegment_Priority asserts the input-token segment outranks turn
// tokens (which show idle stats), so on a narrow terminal the user's in-progress
// message length outlasts the stale counts from the last completed turn.
func TestInputTokensSegment_Priority(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := Bar{
		Theme:       styles.Default(),
		Model:       "m",
		Agent:       "a",
		SessionID:   "id",
		StartedAt:   start,
		Now:         start,
		InputTokens: "~128 tok",
		TurnTokens:  "1.2k in · 234 out",
	}

	// Wide enough for both: both must appear.
	full := bar.Render(160)
	require.Contains(t, full, "~128 tok", "input tokens must appear on a wide bar")
	require.Contains(t, full, "1.2k in · 234 out", "turn tokens must appear on a wide bar")

	// Narrow: the lower-priority turn tokens are shed before the input count.
	narrow := bar.Render(len([]rune("m · ~128 tok")))
	require.Contains(t, narrow, "~128 tok", "input tokens must outlast turn tokens on a narrow bar")
	require.NotContains(t, narrow, " in · ", "turn tokens must be shed before input tokens")
}

// TestRenderIfChanged_UnchangedFrameNotReemitted asserts that re-rendering an
// unchanged bar at the same width reports changed==false, so a caller redrawing
// on a timer can skip re-emitting a byte-identical status line — the dominant
// source of redraw noise in a captured PTY, where each per-second uptime tick
// would otherwise repaint the whole bar. The first render always reports
// changed (so the bar is drawn at least once), and the returned line is always
// the same string Render would produce.
func TestRenderIfChanged_UnchangedFrameNotReemitted(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := &Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}

	first, changed := bar.RenderIfChanged(160)
	require.True(t, changed, "the first render must report changed so the bar draws once")
	require.Equal(t, bar.Render(160), first, "RenderIfChanged must return the same line Render would")

	// An identical frame (no field moved, same width) must report unchanged so
	// the caller can drop the redraw.
	_, changed = bar.RenderIfChanged(160)
	require.False(t, changed, "a byte-identical frame must report unchanged")

	// Repeated identical frames keep reporting unchanged — the per-second idle
	// tick must not re-emit a stable bar.
	for i := 0; i < 5; i++ {
		_, changed = bar.RenderIfChanged(160)
		require.False(t, changed, "a stable bar must keep reporting unchanged on every tick")
	}
}

// TestRenderIfChanged_ContentChangeReemits asserts that a real content change
// (a field moving, e.g. the working spinner appearing or the uptime advancing
// to a new whole-second readout) reports changed==true, so live tool progress
// is never suppressed even while the bar is being deduped on idle ticks.
func TestRenderIfChanged_ContentChangeReemits(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := &Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}

	_, changed := bar.RenderIfChanged(160)
	require.True(t, changed, "the first render reports changed")
	_, changed = bar.RenderIfChanged(160)
	require.False(t, changed, "an unchanged second render reports unchanged")

	// A turn starting surfaces the working segment — a genuine content change
	// that must re-emit so progress is visible.
	bar.Working = "⠙ working 3s"
	_, changed = bar.RenderIfChanged(160)
	require.True(t, changed, "a new working segment must report changed")
	_, changed = bar.RenderIfChanged(160)
	require.False(t, changed, "the bar is stable again once the segment is drawn")

	// The uptime advancing to a new readout is also a content change.
	bar.Now = start.Add(90 * time.Second)
	_, changed = bar.RenderIfChanged(160)
	require.True(t, changed, "an advanced uptime readout must report changed")
}

// TestRenderIfChanged_WidthChangeReemits asserts that the same fields rendered
// at a new width report changed==true, because the styled widths (and any
// priority-drop or truncation) shift with the window — so a resize is never
// suppressed as a no-op redraw.
func TestRenderIfChanged_WidthChangeReemits(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	bar := &Bar{Theme: styles.Default(), Model: "m", Agent: "a", SessionID: "id", StartedAt: start, Now: start}

	_, changed := bar.RenderIfChanged(160)
	require.True(t, changed, "the first render reports changed")
	_, changed = bar.RenderIfChanged(160)
	require.False(t, changed, "an unchanged render at the same width reports unchanged")

	// A new width must re-emit even though no field moved.
	_, changed = bar.RenderIfChanged(40)
	require.True(t, changed, "a width change must report changed")
}
