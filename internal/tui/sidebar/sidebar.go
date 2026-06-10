// Package sidebar renders the TUI's top info strip: the at-a-glance summary of
// the session's environment — active model, provider, working directory, the
// yolo (auto-approve) affordance, and how many files the turn has changed.
//
// It is the header counterpart to the statusbar at the foot of the screen: the
// status bar carries live, turn-scoped progress (the working spinner, token
// counts, scroll position) while this strip carries the stable context a user
// glances at to confirm "where am I and what is loaded". Keeping the two apart
// lets each render and shed segments independently as the terminal narrows.
package sidebar

import (
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
)

// Info holds the fields the header strip surfaces. Its zero value renders an
// empty line, so a caller that has not yet resolved any field shows nothing
// rather than a row of separators.
type Info struct {
	Theme styles.Theme
	// Model is the active model id (the same anchor the status bar badges).
	Model string
	// Provider is the provider backing the active model (e.g. "moonshot").
	// Empty hides the segment.
	Provider string
	// Cwd is the working directory the session is scoped to, already shortened
	// for display (home collapsed to "~"). Empty hides the segment.
	Cwd string
	// Yolo reports whether global auto-approval is on; when true the strip shows
	// a "yolo" marker so the user is never unaware that risky tools run unprompted.
	Yolo bool
	// Changed is the number of files the session has modified so far. Zero hides
	// the segment, so a session that has changed nothing shows no count.
	Changed int
}

// Render returns the one-line header strip, muted so it reads as context rather
// than competing with the transcript. Segments are joined with " · " in a fixed
// order — model, provider, cwd, yolo, changed — and the whole line is clamped to
// width with a trailing ellipsis when it does not fit, so the strip never wraps
// onto a second row and breaks the rigid layout budget. A non-positive width
// renders the unclamped line (the caller treats width 0 as "unbounded").
func (i Info) Render(width int) string {
	var segs []string
	if i.Model != "" {
		segs = append(segs, i.Model)
	}
	if i.Provider != "" {
		segs = append(segs, i.Provider)
	}
	if i.Cwd != "" {
		segs = append(segs, i.Cwd)
	}
	if i.Yolo {
		segs = append(segs, "yolo")
	}
	if i.Changed > 0 {
		segs = append(segs, "✎ "+strconv.Itoa(i.Changed)+" changed")
	}
	if len(segs) == 0 {
		return ""
	}
	line := strings.Join(segs, " · ")
	if width > 0 {
		line = clampLine(line, width)
	}
	return i.Theme.Muted.Render(line)
}

// clampLine clamps line to at most width runes, replacing the final visible rune
// with an ellipsis when it is cut short so the reader can tell the strip was
// truncated. At width 1 the lone cell becomes the ellipsis; a non-positive width
// leaves the line untouched.
func clampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	r := []rune(line)
	if len(r) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
