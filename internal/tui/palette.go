package tui

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// paletteWindow caps how many command rows the palette draws at once. A cursor
// that walks past the window boundary scrolls a following window so the selected
// row stays visible — the same windowing the session picker uses, keeping the
// two pickers visually consistent.
const paletteWindow = 12

// paletteEntry is one row in the command palette: the slash command name and
// its terse one-line description.
type paletteEntry struct {
	name string // "/help", "/clear", "/diff", etc.
	desc string // "list commands", "clear visible chat", etc.
}

// paletteBuiltinOrder defines the display order of built-in slash commands in
// the palette — the same sequence as /help so the two listings agree and a user
// who reads one can predict the other.
var paletteBuiltinOrder = []string{
	"/help", "/keys", "/clear", "/sessions",
	"/tab", "/tabs", "/compact", "/fork",
	"/diff", "/revert", "/export", "/copy",
	"/search", "/status", "/mcp", "/plan",
	"/approve", "/model", "/agent", "/goal",
	"/permissions", "/budget", "/theme", "/yolo",
	"/save", "/quit",
}

// paletteWindowBounds returns the half-open [start, end) range of palette rows
// the picker draws for a list of total rows with the cursor at cursor, scrolling
// a window of paletteWindow rows so the selected row stays visible. It mirrors
// the sessionWindowBounds algorithm with the palette-specific constant.
func paletteWindowBounds(cursor, total int) (start, end int) {
	if total <= paletteWindow {
		return 0, total
	}
	start = cursor - paletteWindow/2
	if start < 0 {
		start = 0
	}
	end = start + paletteWindow
	if end > total {
		end = total
		start = end - paletteWindow
	}
	return start, end
}

// allPaletteEntries returns every slash command available in the palette:
// built-in commands in canonical /help order first, then dynamic recipes and
// custom prompts in registration order. The two sources agree with what /help
// lists so the palette is a superset of nothing — every entry is executable.
func (m *model) allPaletteEntries() []paletteEntry {
	out := make([]paletteEntry, 0, len(paletteBuiltinOrder)+8)
	for _, name := range paletteBuiltinOrder {
		out = append(out, paletteEntry{name: name, desc: slashCommandDescriptions[name]})
	}
	// Dynamic recipes and custom prompts keyed with their leading slash.
	for name, desc := range m.inputHistory.dynamicDescriptions {
		out = append(out, paletteEntry{name: name, desc: desc})
	}
	return out
}

// visiblePaletteEntries returns the palette entries that match m.paletteFilter,
// ranked in three tiers — command-name prefix first, then substring anywhere in
// the name-or-description haystack, then scattered subsequence — the same
// ranking the session picker and @-file picker use, so filtering feels
// consistent across all interactive pickers in the TUI. An empty filter returns
// every entry in canonical order.
func (m *model) visiblePaletteEntries() []paletteEntry {
	all := m.allPaletteEntries()
	if m.paletteFilter == "" {
		return all
	}
	q := strings.ToLower(m.paletteFilter)
	var prefix, substr, subseq []paletteEntry
	for _, e := range all {
		name := strings.ToLower(e.name)
		hay := name + " " + strings.ToLower(e.desc)
		nameCore := strings.TrimPrefix(name, "/") // match "help" without "/"
		switch {
		case strings.HasPrefix(nameCore, q) || strings.HasPrefix(name, "/"+q):
			prefix = append(prefix, e)
		case strings.Contains(hay, q):
			substr = append(substr, e)
		case isSubsequence(q, hay):
			subseq = append(subseq, e)
		}
	}
	out := make([]paletteEntry, 0, len(prefix)+len(substr)+len(subseq))
	out = append(out, prefix...)
	out = append(out, substr...)
	return append(out, subseq...)
}

// openCommandPalette opens the interactive command palette dialog. The palette
// shows all slash commands with descriptions, lets the user type-to-filter and
// navigate with arrow keys, and executes the selected command on Enter —
// matching the command-palette UX in Claude Code and opencode.
func (m *model) openCommandPalette() (tea.Model, tea.Cmd) {
	m.paletteFilter = ""
	m.paletteCursor = 0
	m.dialogs.Push(&dialog.Text{
		DialogID: "palette",
		Title:    "Commands  (type to filter · ↑/↓ select · enter run · esc cancel)",
		Body:     m.paletteBody(),
		Theme:    m.theme,
	})
	return m, nil
}

// paletteBody renders the dialog body for the command palette: an optional
// filter echo, a windowed cursor-marked list of matching entries, and a scroll
// indicator when the list is long enough to require windowing.
func (m *model) paletteBody() string {
	visible := m.visiblePaletteEntries()
	lines := make([]string, 0, paletteWindow+4)
	if m.paletteFilter != "" {
		count := m.theme.Muted.Render(fmt.Sprintf("· %d of %d", len(visible), len(m.allPaletteEntries())))
		lines = append(lines, "Filter: "+m.paletteFilter+" "+count, "")
	}
	if len(visible) == 0 {
		lines = append(lines, "(no commands match)")
		lines = append(lines, "", "type to filter · esc to cancel")
		return strings.Join(lines, "\n")
	}
	start, end := paletteWindowBounds(m.paletteCursor, len(visible))
	if start > 0 {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more above", start)))
	}
	q := strings.ToLower(m.paletteFilter)
	for i := start; i < end; i++ {
		e := visible[i]
		if i == m.paletteCursor {
			row := "> " + m.theme.Accent.Render(e.name)
			if e.desc != "" {
				row += " — " + e.desc
			}
			if key := slashCommandKeys[e.name]; key != "" {
				row += m.theme.Muted.Render("  (" + key + ")")
			}
			lines = append(lines, row)
		} else if q != "" {
			// Highlight the runes in the command name that matched the filter so
			// the user can see at a glance why each entry appeared — the same
			// per-character emphasis the slash-command and @-file menus use.
			core := strings.TrimPrefix(e.name, "/")
			namePart := "  " + m.theme.Muted.Render("/") + m.highlightMatch(core, q)
			suffix := ""
			if e.desc != "" {
				suffix += " — " + e.desc
			}
			if key := slashCommandKeys[e.name]; key != "" {
				suffix += "  (" + key + ")"
			}
			lines = append(lines, namePart+m.theme.Muted.Render(suffix))
		} else {
			row := "  " + e.name
			if e.desc != "" {
				row += " — " + e.desc
			}
			if key := slashCommandKeys[e.name]; key != "" {
				row += "  (" + key + ")"
			}
			lines = append(lines, m.theme.Muted.Render(row))
		}
	}
	if end < len(visible) {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more below", len(visible)-end)))
	}
	return strings.Join(lines, "\n")
}

// handlePaletteKey processes navigation and selection while the command palette
// is open. It returns whether the key was consumed so the caller knows whether
// to fall through to the dialog's own handler (for esc dismissal).
func (m *model) handlePaletteKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.paletteCursor > 0 {
			m.paletteCursor--
			m.refreshCommandPalette()
		}
		return true, nil
	case "down":
		if m.paletteCursor < len(m.visiblePaletteEntries())-1 {
			m.paletteCursor++
			m.refreshCommandPalette()
		}
		return true, nil
	case "home":
		if m.paletteCursor != 0 {
			m.paletteCursor = 0
			m.refreshCommandPalette()
		}
		return true, nil
	case "end":
		if last := len(m.visiblePaletteEntries()) - 1; last >= 0 && m.paletteCursor != last {
			m.paletteCursor = last
			m.refreshCommandPalette()
		}
		return true, nil
	case "pgup":
		if m.paletteCursor > 0 {
			m.paletteCursor -= paletteWindow
			if m.paletteCursor < 0 {
				m.paletteCursor = 0
			}
			m.refreshCommandPalette()
		}
		return true, nil
	case "pgdown":
		if last := len(m.visiblePaletteEntries()) - 1; last >= 0 && m.paletteCursor < last {
			m.paletteCursor += paletteWindow
			if m.paletteCursor > last {
				m.paletteCursor = last
			}
			m.refreshCommandPalette()
		}
		return true, nil
	case "backspace":
		if m.paletteFilter != "" {
			r := []rune(m.paletteFilter)
			m.paletteFilter = string(r[:len(r)-1])
			m.paletteCursor = 0
			m.refreshCommandPalette()
		}
		return true, nil
	case "enter":
		visible := m.visiblePaletteEntries()
		if len(visible) == 0 {
			return true, nil
		}
		chosen := visible[m.paletteCursor]
		m.dialogs.Pop()
		m.paletteFilter = ""
		// Execute the chosen command via the slash dispatcher, preserving the
		// current prompt so the user's in-progress text is untouched.
		_, execCmd := m.handleSlash(chosen.name)
		return true, execCmd
	default:
		if text := msg.Key().Text; text != "" {
			m.paletteFilter += text
			m.paletteCursor = 0
			m.refreshCommandPalette()
			return true, nil
		}
		return false, nil
	}
}

// refreshCommandPalette re-renders the open palette dialog so a cursor move or
// filter edit is immediately reflected. It replaces the top dialog in place,
// mirroring the refreshSessionPicker pattern.
func (m *model) refreshCommandPalette() {
	m.dialogs.Pop()
	m.dialogs.Push(&dialog.Text{
		DialogID: "palette",
		Title:    "Commands  (type to filter · ↑/↓ select · enter run · esc cancel)",
		Body:     m.paletteBody(),
		Theme:    m.theme,
	})
}
