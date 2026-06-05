package tui

import (
	"strconv"
	"strings"
)

// overflowSuffix formats the trailing indicator a one-row completion menu
// appends when not every match fits, reporting how many matches were dropped
// so the count is visible rather than a bare ellipsis. When hidden is not
// positive the plain ellipsis is used, so a rounding mismatch never shows a
// "+0" or negative count. This is shared by the slash-command and @-file menus
// so both communicate overflow the same way, matching how Claude Code and
// opencode show the remaining-match count.
func overflowSuffix(hidden int) string {
	if hidden <= 0 {
		return " …"
	}
	return " … +" + strconv.Itoa(hidden)
}

// slashCommandDescriptions maps a built-in slash command to a terse one-line
// summary surfaced in the completion menu once the selection narrows to a
// single command. Keeping the gloss next to the command — rather than only in
// /help — lets the user confirm what a command does without leaving the prompt,
// matching the inline command descriptions in Claude Code and opencode.
var slashCommandDescriptions = map[string]string{
	"/help":        "list commands",
	"/keys":        "show keyboard shortcuts",
	"/clear":       "clear visible chat",
	"/model":       "open model picker",
	"/agent":       "open agent picker",
	"/sessions":    "restore a recent session",
	"/tab":         "open or switch session tabs",
	"/tabs":        "list open tabs",
	"/fork":        "branch the current session",
	"/diff":        "show the latest edit diff",
	"/status":      "show model, session, and spend",
	"/plan":        "restrict to read-only tools and propose a plan",
	"/approve":     "exit plan mode and re-enable execution",
	"/goal":        "show, set, run, or clear the goal",
	"/permissions": "show or set approval mode",
	"/budget":      "show ledger and budget settings",
	"/theme":       "show or switch the color theme",
	"/yolo":        "toggle permission bypass",
	"/save":        "persist session",
	"/export":      "write the transcript to a file",
	"/copy":        "copy reply or chat to clipboard",
	"/search":      "find a term in the chat",
	"/compact":     "summarize older turns to shrink context",
	"/quit":        "exit",
}

// slashHintDescIndex returns the index of the command whose description should
// be shown, or -1 when none applies. A description is shown for the command the
// user has settled on: the one marked active during a Tab cycle, or the sole
// remaining match when a prefix narrows to one command.
func slashHintDescIndex(cmds []string, active int) int {
	if active >= 0 && active < len(cmds) {
		return active
	}
	if len(cmds) == 1 {
		return 0
	}
	return -1
}

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
	buffer := m.input.String()
	cmds, active := slashHintCommands(buffer, &m.inputHistory)
	if len(cmds) == 0 || width <= 0 {
		return ""
	}

	// Outside an active Tab cycle the buffer is the search token; highlight the
	// command-name runes it matched so the user can see why each entry qualified —
	// especially under the fuzzy subsequence fallback, where the matched letters
	// are scattered. During a cycle the buffer holds the selected command, so the
	// token is suppressed and the active entry is accented whole instead.
	var token string
	if active < 0 {
		token = strings.TrimPrefix(buffer, "/")
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
		switch {
		case i == active:
			parts = append(parts, m.theme.Accent.Render(name))
		case token != "":
			parts = append(parts, m.highlightMatch(name, token))
		default:
			parts = append(parts, m.theme.Muted.Render(name))
		}
	}
	if len(parts) == 0 {
		return ""
	}

	line := indent + strings.Join(parts, sep)
	if truncated {
		line += m.theme.Muted.Render(overflowSuffix(len(cmds) - len(parts)))
		return line
	}

	// Once the user has settled on a single command, append its description on
	// the same row when there is spare width. A truncated name list already ends
	// in an ellipsis and has no room, so the gloss is only added to a list that
	// fully fit.
	if di := slashHintDescIndex(cmds, active); di >= 0 {
		if desc := slashCommandDescriptions[cmds[di]]; desc != "" {
			suffix := " — " + desc
			if used+len([]rune(suffix)) <= width {
				line += m.theme.Muted.Render(suffix)
			}
		}
	}
	return line
}
