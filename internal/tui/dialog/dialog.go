// Package dialog provides a small modal stack for the TUI.
package dialog

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// Dialog is a modal rendered above the main interface.
type Dialog interface {
	ID() string
	Render(width int) string
	HandleKey(tea.KeyPressMsg) (handled bool, pop bool)
}

// Stack stores active dialogs with the topmost modal last.
type Stack struct {
	items []Dialog
}

// Push adds a dialog to the stack.
func (s *Stack) Push(d Dialog) {
	if d != nil {
		s.items = append(s.items, d)
	}
}

// Pop removes the topmost dialog.
func (s *Stack) Pop() Dialog {
	if len(s.items) == 0 {
		return nil
	}
	d := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return d
}

// Top returns the topmost dialog.
func (s *Stack) Top() Dialog {
	if len(s.items) == 0 {
		return nil
	}
	return s.items[len(s.items)-1]
}

// Contains reports whether a dialog id is active.
func (s *Stack) Contains(id string) bool {
	for _, d := range s.items {
		if d.ID() == id {
			return true
		}
	}
	return false
}

// Len returns the number of active dialogs.
func (s *Stack) Len() int {
	return len(s.items)
}

// Render renders the topmost dialog.
func (s *Stack) Render(width int) string {
	top := s.Top()
	if top == nil {
		return ""
	}
	return top.Render(width)
}

// Permission is a tool permission prompt.
type Permission struct {
	Theme styles.Theme
	Req   pubsub.PermissionRequest
}

// ID returns the stable dialog id.
func (p *Permission) ID() string {
	return "permission"
}

// Render returns the permission prompt body.
func (p *Permission) Render(width int) string {
	body := fmt.Sprintf("Permission required\n\n%s\n\nTool: %s\n\n[y] allow  [n] deny", p.Req.Reason, p.Req.Tool)
	if width > 8 {
		body = clampLines(body, width-4)
	}
	return p.Theme.Modal.Render(body)
}

// HandleKey processes permission prompt keys.
func (p *Permission) HandleKey(msg tea.KeyPressMsg) (bool, bool) {
	switch strings.ToLower(msg.String()) {
	case "y":
		p.Req.Reply <- pubsub.PermissionDecision{Approved: true}
		return true, true
	case "n", "esc":
		p.Req.Reply <- pubsub.PermissionDecision{Approved: false}
		return true, true
	default:
		return true, false
	}
}

// Text is a generic informational dialog.
type Text struct {
	DialogID string
	Title    string
	Body     string
	Theme    styles.Theme
}

// ID returns the stable dialog id.
func (t *Text) ID() string {
	return t.DialogID
}

// Render returns the dialog body.
func (t *Text) Render(width int) string {
	body := strings.TrimSpace(t.Title + "\n\n" + t.Body)
	if width > 8 {
		body = clampLines(body, width-4)
	}
	return t.Theme.Modal.Render(body)
}

// HandleKey dismisses generic dialogs on escape or enter.
func (t *Text) HandleKey(msg tea.KeyPressMsg) (bool, bool) {
	switch msg.String() {
	case "esc", "enter":
		return true, true
	default:
		return true, false
	}
}

// clampLines clamps every line of s to at most width runes. When a line is cut
// short an ellipsis replaces its final visible rune, so the reader can tell the
// text was truncated rather than mistaking the clipped line for the whole
// content — matching the ellipsis the status bar and diff viewer add to clamped
// lines. At width 1 there is no room for both content and a marker, so the lone
// cell becomes the ellipsis; a non-positive width leaves lines untouched.
func clampLines(s string, width int) string {
	if width <= 0 {
		return s
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		r := []rune(line)
		if len(r) > width {
			if width == 1 {
				line = "…"
			} else {
				line = string(r[:width-1]) + "…"
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
