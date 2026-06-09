// Package statusbar renders the TUI status line.
package statusbar

import (
	"strconv"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

// Bar stores status fields shown at the bottom of the screen.
type Bar struct {
	Theme     styles.Theme
	Model     string
	Agent     string
	SessionID string
	StartedAt time.Time
	Now       time.Time
	Yolo      bool
	// Working shows live turn progress while the agent is running (e.g.
	// "⠙ working 3s"). Empty hides the segment, so the bar is unchanged once
	// the turn finishes and the prompt is idle.
	Working string
	// Mode is the live permission approval mode (e.g. "read-only", "auto",
	// "full"). Empty hides the segment.
	Mode string
	// Goal shows autonomous goal-loop progress (e.g. "goal 3/10"). Empty
	// hides the segment.
	Goal string
	// Search shows scrollback-search progress (e.g. "search 2/7"). Empty
	// hides the segment, so the bar is unchanged when no search is active.
	Search string
	// Scroll shows scrollback position when the chat view is scrolled up from
	// the newest output (e.g. "↓ 12 below"). Empty hides the segment, so the
	// bar is unchanged while the view is anchored to the bottom.
	Scroll string
	// TurnTokens shows the token counts from the last completed turn (e.g.
	// "1.2k in · 234 out"). Empty hides the segment; it is cleared when a new
	// turn starts and set once the turn finishes.
	TurnTokens string
	// ContextPct is the context-window fill percentage for the last completed
	// turn (1–100). Zero hides the segment, so the bar is unchanged until
	// real usage data arrives. Values are shown as "ctx N%" and styled by
	// threshold: muted below 50%, warning from 50–79%, error at 80%+, so a
	// user approaching the limit gets a clear visual signal before the
	// window fills completely — matching how Claude Code and opencode surface
	// context-window headroom.
	ContextPct int
	// InputTokens shows the estimated token count of the current input buffer
	// (e.g. "~128 tok"). Empty hides the segment; it is set while the user
	// is composing a prompt and cleared once the turn is submitted, so a
	// user writing a long message can see their approximate token footprint
	// before sending — without opening a dialog.
	InputTokens string

	// lastRendered caches the most recent Render output keyed by the width it
	// was produced for, so RenderIfChanged can report whether a redraw would
	// emit a byte-identical line. Used to drop unchanged status frames from
	// captured PTYs (where every per-second uptime tick would otherwise re-emit
	// the whole bar). It is unexported so the cache stays an internal detail of
	// the bar; callers that always want a fresh line keep calling Render.
	lastRendered string
	lastWidth    int
	rendered     bool
}

// segment is one " · "-joined field of the status line paired with the priority
// that decides which fields survive when the bar is too wide for the window. A
// higher prio is kept longer; the lowest-prio present segment is dropped first.
type segment struct {
	text string
	prio int
}

// Segment priorities. Higher survives a narrow window longer. The live,
// turn-scoped fields (the working spinner, search/scroll position) outrank the
// static identity fields (agent, session id, uptime) so a user watching a
// running turn keeps the progress readout even when the terminal is too narrow
// to show everything — rather than losing the spinner first because it sits at
// the tail of the line. The model is the anchor: it is never dropped, only
// ellipsis-clipped as a last resort.
const (
	prioModel       = 1000 // anchor: never dropped
	prioWorking     = 100
	prioSearch      = 90
	prioScroll      = 80
	prioGoal        = 70
	prioMode        = 60
	prioYolo        = 50
	prioContextPct  = 45 // outranks turn tokens so context headroom is shed last
	prioInputTokens = 40 // input-length estimate: outranks idle turn stats while typing
	prioTurnTokens  = 35
	prioAgent       = 30
	prioSession     = 25
	prioUptime      = 20
)

// Render returns one status line. The model name is rendered as a saffron brand
// pill (styles.ModelBadge) so it reads as the primary identity anchor; all
// remaining segments stay as plain text so the priority-drop and truncation
// logic operates on printable widths without ANSI interference. The dim styled
// separator is applied at the final join step so the bar reads as a segmented
// strip; the overall status background is applied last.
func (b Bar) Render(width int) string {
	now := b.Now
	if now.IsZero() {
		now = time.Now()
	}
	started := b.StartedAt
	if started.IsZero() {
		started = now
	}

	// Build the segments in display order using PLAIN text so fitSegmentsSlice
	// can measure and drop using simple rune counting (the existing invariant that
	// the priority-drop tests were written against).
	segs := []segment{
		{b.Model, prioModel},
		{b.Agent, prioAgent},
		{"session " + shortID(b.SessionID), prioSession},
		{"up " + util.HumanDuration(now.Sub(started)), prioUptime},
	}
	if b.Working != "" {
		segs = append(segs, segment{b.Working, prioWorking})
	}
	if b.Mode != "" {
		segs = append(segs, segment{b.Mode, prioMode})
	}
	if b.Yolo {
		segs = append(segs, segment{"yolo", prioYolo})
	}
	if b.Goal != "" {
		segs = append(segs, segment{b.Goal, prioGoal})
	}
	if b.Search != "" {
		segs = append(segs, segment{b.Search, prioSearch})
	}
	if b.Scroll != "" {
		segs = append(segs, segment{b.Scroll, prioScroll})
	}
	if b.TurnTokens != "" {
		segs = append(segs, segment{b.TurnTokens, prioTurnTokens})
	}
	if b.ContextPct > 0 {
		segs = append(segs, segment{"ctx " + strconv.Itoa(b.ContextPct) + "%", prioContextPct})
	}
	if b.InputTokens != "" {
		segs = append(segs, segment{b.InputTokens, prioInputTokens})
	}

	// Fit using plain widths — this is the invariant the priority-drop tests rely
	// on. fitSegmentsSlice returns the surviving segment slice so we can work
	// with the individual texts without re-splitting the joined string (which
	// would incorrectly split segments whose own text contains " · ", e.g.
	// TurnTokens = "1.2k in · 234 out").
	survived := fitSegmentsSlice(segs, width)

	badge := styles.ModelBadge(b.Model, "")
	if badge == "" {
		badge = styles.Muted.Render(b.Model)
	}
	styledSep := styles.Separator.Render(" · ")

	// Build the styled segment list, substituting the badge for the model anchor.
	styledParts := make([]string, len(survived))
	for i, s := range survived {
		if i == 0 {
			styledParts[i] = badge
		} else {
			styledParts[i] = s.text
		}
	}

	// Check whether the PLAIN joined line exceeds width. If it does, apply
	// truncateLine (which adds "…") to the plain form, then return the plain
	// truncated line wrapped in the status style (no badge, but the "…" marker
	// is correct). This path is only hit when even the anchor alone is too long —
	// a normal bar fits after priority-dropping, so this is the rare fallback.
	plainParts := make([]string, len(survived))
	for i, s := range survived {
		plainParts[i] = s.text
	}
	plainLine := strings.Join(plainParts, " · ")
	if width > 0 && len([]rune(plainLine)) > width {
		return b.Theme.Status.Render(truncateLine(plainLine, width))
	}

	return b.Theme.Status.Render(strings.Join(styledParts, styledSep))
}

// RenderIfChanged renders the bar and reports whether the result differs from
// the line produced by the previous RenderIfChanged call at the same width. A
// caller redrawing on a timer can use changed==false to skip re-emitting a
// byte-identical status line — the dominant source of redraw noise in a
// captured PTY, where each per-second uptime tick would otherwise repaint the
// whole bar even though nothing the user can act on moved. The first call (and
// any call at a new width, where the styled widths shift) always reports
// changed==true so the bar is drawn at least once. A real terminal's renderer
// already diffs frames, so this only trims output on the non-rendering/capture
// path; behavior on screen is identical either way.
func (b *Bar) RenderIfChanged(width int) (line string, changed bool) {
	line = b.Render(width)
	if b.rendered && b.lastWidth == width && b.lastRendered == line {
		return line, false
	}
	b.lastRendered = line
	b.lastWidth = width
	b.rendered = true
	return line, true
}

// fitSegments joins segs with " · " in their given (display) order, dropping the
// lowest-priority segment whenever the joined line is wider than width — so a
// narrow window sheds the least-important fields rather than blindly clipping the
// tail, which would always hide the live progress segments that sit at the end.
// A non-positive width is unbounded, so every segment is kept. The result may
// still exceed width when even the highest-priority survivor alone does not fit;
// the caller's ellipsis truncation handles that final case.
func fitSegments(segs []segment, width int) string {
	survived := fitSegmentsSlice(segs, width)
	parts := make([]string, len(survived))
	for i, s := range survived {
		parts[i] = s.text
	}
	return strings.Join(parts, " · ")
}

// fitSegmentsSlice applies the priority-drop logic and returns the surviving
// segment slice rather than a joined string, so callers that need to style
// individual segments (for example replacing the model with a branded badge)
// can do so without accidentally touching " · " inside segment text.
func fitSegmentsSlice(segs []segment, width int) []segment {
	join := func(s []segment) string {
		parts := make([]string, len(s))
		for i, seg := range s {
			parts[i] = seg.text
		}
		return strings.Join(parts, " · ")
	}

	if width <= 0 {
		out := make([]segment, len(segs))
		copy(out, segs)
		return out
	}

	// Drop on a copy so the caller's slice (and its backing array) is untouched —
	// the shrink uses append-shift, which would otherwise corrupt a reused input.
	segs = append([]segment(nil), segs...)
	for len([]rune(join(segs))) > width {
		// Find the lowest-priority segment to drop. Ties drop the later (more
		// rightward) segment so earlier identity fields outlive a duplicate-prio
		// trailing one, keeping the line's left edge stable as it shrinks.
		drop := -1
		for i, seg := range segs {
			if drop < 0 || seg.prio <= segs[drop].prio {
				drop = i
			}
		}
		if drop < 0 || segs[drop].prio == prioModel {
			break // only the anchor remains; let truncateLine clip it
		}
		segs = append(segs[:drop], segs[drop+1:]...)
	}
	return segs
}

// truncateLine clamps line to at most width runes. When a line is cut short an
// ellipsis replaces its final visible rune, so the reader can tell trailing
// segments (live progress, search position, scroll offset) were hidden rather
// than mistaking the clipped text for the whole bar — matching the ellipsis the
// diff viewer adds to clamped lines. A non-positive width leaves the line
// untouched (the caller treats width 0 as "unbounded"); at width 1 there is no
// room for both content and a marker, so the lone cell becomes the ellipsis.
func truncateLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func shortID(id string) string {
	if len(id) <= 8 {
		if id == "" {
			return "new"
		}
		return id
	}
	return id[:8]
}
