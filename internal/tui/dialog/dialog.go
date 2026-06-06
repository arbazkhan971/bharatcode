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

// ScrollableText is like Text but supports keyboard scrolling for long content
// such as diffs or keybinding listings that may exceed the terminal height.
// Set Height to m.height so the dialog knows how many body lines to show at
// once. A zero Height falls back to a generous fixed cap so the dialog is still
// usable when the caller does not supply a terminal height.
type ScrollableText struct {
	DialogID string
	Title    string
	Body     string
	Theme    styles.Theme
	// Height is the terminal's current row count. The dialog reserves a few
	// rows for the title, blank separator, scroll-hint footer, and modal
	// border, and fills the rest with body lines.
	Height int
	// CopyFn, when non-nil, is called when the user presses 'y'. It should
	// write the dialog's raw content to the system clipboard and return an
	// error on failure. The 'y copy' hint is included in the scroll footer
	// only when CopyFn is set, so dialogs that do not opt in are unchanged.
	CopyFn func() error
	// scroll is the current line offset into the body. It is updated in place
	// by HandleKey and read by Render, so the dialog scrolls without needing
	// external state in the model.
	scroll int
	// copyMsg is a transient status line set after a 'y' copy attempt: "Copied!"
	// on success or a short error description on failure. Render shows it in
	// the footer in place of the normal scroll hint for one render cycle.
	copyMsg string
}

// ID returns the stable dialog id.
func (t *ScrollableText) ID() string { return t.DialogID }

// visibleRows returns how many body lines the dialog can show at once, given
// the rows consumed by the title, blank separator, scroll hint footer, and
// modal border/padding. Falls back to 20 when Height is zero.
func (t *ScrollableText) visibleRows() int {
	if t.Height <= 0 {
		return 20
	}
	// Reserve: 1 title, 1 blank, 1 hint footer, 1 blank before hint, 2 border
	// rows (top+bottom), 1 padding margin — 7 rows total overhead.
	v := t.Height - 7
	if v < 3 {
		v = 3
	}
	return v
}

// maxScroll returns the highest valid scroll offset for the current body.
func (t *ScrollableText) maxScroll() int {
	lines := strings.Split(t.Body, "\n")
	m := len(lines) - t.visibleRows()
	if m < 0 {
		return 0
	}
	return m
}

// Render renders the dialog showing only the current scroll window and a
// one-line hint when the body extends beyond the visible area.
func (t *ScrollableText) Render(width int) string {
	lines := strings.Split(t.Body, "\n")
	rows := t.visibleRows()
	total := len(lines)
	maxS := total - rows
	if maxS < 0 {
		maxS = 0
	}
	// Clamp scroll defensively (HandleKey already clamps, but Render may be
	// called before any key arrives, or after a resize changes visibleRows).
	if t.scroll > maxS {
		t.scroll = maxS
	}
	if t.scroll < 0 {
		t.scroll = 0
	}

	end := t.scroll + rows
	if end > total {
		end = total
	}
	window := lines[t.scroll:end]

	var sb strings.Builder
	sb.WriteString(t.Title)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(window, "\n"))

	// When the body doesn't fit, append a one-line scroll indicator so the
	// reader knows there is more content and which keys navigate it. When a
	// copy was just attempted, show the outcome instead and clear it so
	// subsequent renders revert to the normal hint.
	if t.copyMsg != "" {
		sb.WriteString("\n\n")
		sb.WriteString(t.copyMsg)
		t.copyMsg = ""
	} else if total > rows {
		above := t.scroll
		below := total - end
		sb.WriteString("\n\n")
		copyHint := ""
		if t.CopyFn != nil {
			copyHint = "y copy · "
		}
		if above > 0 && below > 0 {
			sb.WriteString(fmt.Sprintf("↑ %d above · ↓ %d below · %sPgUp/PgDn/Home/End scroll · Esc close", above, below, copyHint))
		} else if above > 0 {
			sb.WriteString(fmt.Sprintf("↑ %d above · %sPgUp/Home scroll · Esc close", above, copyHint))
		} else {
			sb.WriteString(fmt.Sprintf("↓ %d below · %sPgDn/End scroll · Esc close", below, copyHint))
		}
	} else if t.CopyFn != nil {
		// Body fits on screen; still surface the copy hint so the user knows 'y' works.
		sb.WriteString("\n\ny copy · Esc close")
	}

	body := sb.String()
	if width > 8 {
		body = clampLines(body, width-4)
	}
	return t.Theme.Modal.Render(body)
}

// HandleKey handles scroll navigation (Up/j, Down/k, PgUp, PgDn, Home/g, End/G),
// clipboard copy (y, when CopyFn is set), and dismissal (Esc, Enter, q). The
// j/k/g/G aliases match vim-style navigation so users familiar with that
// convention do not have to reach for arrow keys in the diff or /keys overlay.
// Navigation keys do not pop the dialog; dismissal keys do.
func (t *ScrollableText) HandleKey(msg tea.KeyPressMsg) (bool, bool) {
	rows := t.visibleRows()
	maxS := t.maxScroll()

	switch msg.String() {
	case "esc", "enter", "q":
		return true, true
	case "y":
		if t.CopyFn != nil {
			if err := t.CopyFn(); err != nil {
				t.copyMsg = "Copy failed: " + err.Error()
			} else {
				t.copyMsg = "Copied!"
			}
		}
		return true, false
	case "up", "k":
		if t.scroll > 0 {
			t.scroll--
		}
		return true, false
	case "down", "j":
		if t.scroll < maxS {
			t.scroll++
		}
		return true, false
	case "pgup":
		t.scroll -= rows
		if t.scroll < 0 {
			t.scroll = 0
		}
		return true, false
	case "pgdown":
		t.scroll += rows
		if t.scroll > maxS {
			t.scroll = maxS
		}
		return true, false
	case "home", "g":
		t.scroll = 0
		return true, false
	case "end", "G":
		t.scroll = maxS
		return true, false
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
