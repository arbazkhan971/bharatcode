// Package statusbar renders the TUI status line.
package statusbar

import (
	"fmt"
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
}

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
	yolo := ""
	if b.Yolo {
		yolo = " · yolo"
	}
	mode := ""
	if b.Mode != "" {
		mode = " · " + b.Mode
	}
	goal := ""
	if b.Goal != "" {
		goal = " · " + b.Goal
	}
	search := ""
	if b.Search != "" {
		search = " · " + b.Search
	}
	scroll := ""
	if b.Scroll != "" {
		scroll = " · " + b.Scroll
	}
	line := fmt.Sprintf("%s · %s · session %s · up %s%s%s%s%s%s", b.Model, b.Agent, shortID(b.SessionID), util.HumanDuration(now.Sub(started)), mode, yolo, goal, search, scroll)
	if len([]rune(line)) > width && width > 0 {
		line = string([]rune(line)[:width])
	}
	return b.Theme.Status.Render(line)
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
