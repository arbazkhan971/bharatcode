package tui

import "strings"

// slashHintCommands returns the slash commands to surface in the completion
// menu beneath the prompt for the current input buffer, plus the index of the
// command currently selected by an active Tab cycle (-1 when none is selected).
//
// The menu is shown whenever the buffer is a "/" prefix that still leaves more
// than one possibility open, so the user can see what Tab will cycle through
// without having to press it. A fully-typed, unambiguous command shows no menu.
// During an active Tab cycle the buffer equals the selected command — which on
// its own would match only itself — so the full cycle is returned with the
// active entry marked, keeping the menu stable as the user Tabs through it.
func slashHintCommands(buffer string, st *inputState) (cmds []string, active int) {
	if !strings.HasPrefix(buffer, "/") {
		return nil, -1
	}
	if len(st.completionMatches) > 0 && buffer == st.completionMatches[st.completionIndex] {
		return st.completionMatches, st.completionIndex
	}
	matches := matchSlash(buffer)
	if len(matches) == 0 {
		return nil, -1
	}
	// A single fully-typed command needs no menu; an in-progress prefix that
	// narrows to one command still shows it as a confirmation hint.
	if len(matches) == 1 && matches[0] == buffer {
		return nil, -1
	}
	return matches, -1
}

// renderSlashHint formats the slash-completion menu for the input region. It
// returns "" when there is nothing to show, so the default prompt is byte-for-
// byte unchanged. Command names are listed without their leading slash; the
// command selected by an active Tab cycle is accented and the rest are muted.
// The list is truncated token-by-token to fit width, appending an ellipsis when
// not every match fits, so the menu never spills past one row.
func (m *model) renderSlashHint(width int) string {
	cmds, active := slashHintCommands(m.input.String(), &m.inputHistory)
	if len(cmds) == 0 || width <= 0 {
		return ""
	}

	const sep = "  "
	const indent = "  "

	var parts []string
	used := len([]rune(indent))
	truncated := false
	for i, c := range cmds {
		name := strings.TrimPrefix(c, "/")
		next := len([]rune(name))
		if i > 0 {
			next += len(sep)
		}
		if used+next > width {
			truncated = true
			break
		}
		used += next
		if i == active {
			parts = append(parts, m.theme.Accent.Render(name))
		} else {
			parts = append(parts, m.theme.Muted.Render(name))
		}
	}
	if len(parts) == 0 {
		return ""
	}

	line := indent + strings.Join(parts, sep)
	if truncated {
		line += m.theme.Muted.Render(" …")
	}
	return line
}
