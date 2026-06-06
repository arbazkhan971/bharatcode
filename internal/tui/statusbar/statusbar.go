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

// Render returns one status line.
func (b Bar) Render(width int) string {
	now := b.Now
	if now.IsZero() {
		now = time.Now()
	}
	started := b.StartedAt
	if started.IsZero() {
		started = now
	}

	// Build the segments in display order. The first four are always present so
	// a bar that fits is byte-identical to the plain " · "-joined form; the rest
	// appear only when their field is set.
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

	line := fitSegments(segs, width)
	return b.Theme.Status.Render(truncateLine(line, width))
}

// fitSegments joins segs with " · " in their given (display) order, dropping the
// lowest-priority segment whenever the joined line is wider than width — so a
// narrow window sheds the least-important fields rather than blindly clipping the
// tail, which would always hide the live progress segments that sit at the end.
// A non-positive width is unbounded, so every segment is kept. The result may
// still exceed width when even the highest-priority survivor alone does not fit;
// the caller's ellipsis truncation handles that final case.
func fitSegments(segs []segment, width int) string {
	join := func(s []segment) string {
		parts := make([]string, len(s))
		for i, seg := range s {
			parts[i] = seg.text
		}
		return strings.Join(parts, " · ")
	}

	if width <= 0 {
		return join(segs)
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
	return join(segs)
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
