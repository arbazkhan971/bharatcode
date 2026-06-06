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

// cyclePositionSuffix formats the "(i/N)" indicator a one-row completion menu
// appends while the user is Tab-cycling through matches, reporting the 1-based
// position of the selected entry within the cycle. It returns "" when no cycle
// is active (active < 0) or the index is out of range, so a menu merely
// previewing matches before the first Tab shows no counter. Surfacing the
// position lets the user see how far through the matches a Tab cycle has walked
// without counting the entries themselves, the way Claude Code and opencode mark
// the selected completion. It is shared by the slash-command and @-file menus so
// both report a cycle the same way.
func cyclePositionSuffix(active, total int) string {
	if active < 0 || total <= 0 || active >= total {
		return ""
	}
	return " (" + strconv.Itoa(active+1) + "/" + strconv.Itoa(total) + ")"
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
	"/revert":      "undo this session's file changes",
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

// slashCommandArgHints maps a built-in slash command that takes arguments to the
// placeholder describing what to type after it, so the moment the user finishes a
// command name and presses space the menu can keep guiding them — showing the
// accepted arguments (and, for enumerated options, the exact tokens) rather than
// going blank. The placeholders mirror the argument hints in slashHelpLines so the
// inline cue and /help agree. Angle brackets mark a required argument, square
// brackets an optional one, matching the convention Claude Code and opencode use
// for inline argument hints.
var slashCommandArgHints = map[string]string{
	"/keys":        "[filter]",
	"/tab":         "[new|next|prev|close|N]",
	"/export":      "[md|html]",
	"/copy":        "[last|all]",
	"/revert":      "[apply|force]",
	"/search":      "<term>",
	"/goal":        "[text|run|stop|clear]",
	"/permissions": "[read-only|auto|full]",
	"/theme":       "[dark|light|high-contrast]",
}

// slashArgHint returns the argument-usage placeholder to surface once the user has
// typed a complete slash command followed by whitespace — the point at which the
// completion menu would otherwise vanish because the name is settled and the rest
// is arguments. It returns "" unless the buffer is exactly a known arg-taking
// command name immediately followed by a space, so an in-progress name (no space
// yet, still handled by the command menu) or a command that takes no arguments
// shows nothing. The hint persists while the argument is typed, so an enumerated
// option list (theme names, tab actions) stays visible as a reminder rather than
// disappearing on the first keystroke.
func slashArgHint(buffer string) string {
	sp := strings.IndexAny(buffer, " \t")
	if sp < 0 {
		return ""
	}
	return slashCommandArgHints[buffer[:sp]]
}

// slashCommandKeys maps a built-in slash command to the keyboard shortcut that
// invokes the very same action, so the completion menu can teach the binding next
// to the command once the user settles on it — the way Claude Code and opencode
// surface a command's shortcut inline so a returning user learns the faster path.
// Only commands whose key opens exactly what the command does are listed (Ctrl+P
// is the model picker, Ctrl+A the agent picker, Ctrl+D the latest-edit diff); a
// command with no clean one-key equivalent simply has no entry. The shortcuts
// mirror the rows in keybindingGroups and the handlers in handleKey, so the
// inline cue, the /keys overlay, and the actual binding all agree.
var slashCommandKeys = map[string]string{
	"/model": "Ctrl+P",
	"/agent": "Ctrl+A",
	"/diff":  "Ctrl+D",
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

// slashDescription returns the terse gloss shown for a settled command in the
// completion menu, or "" when none is known. A built-in is described by the
// static slashCommandDescriptions table; a dynamic recipe or custom prompt is
// described by the gloss captured at setup time, so user-defined commands are
// documented inline just like the built-ins. The built-in table wins on the
// rare name overlap, matching how the runtime resolves a built-in ahead of a
// like-named dynamic command.
func (m *model) slashDescription(cmd string) string {
	if d := slashCommandDescriptions[cmd]; d != "" {
		return d
	}
	return m.inputHistory.dynamicDescriptions[cmd]
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
	matches := matchSlash(st.candidates(), buffer)
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

// slashHintNote returns the quiet one-line note shown beneath the prompt when an
// in-progress "/command" name matches nothing, or "" when no note applies. It
// mirrors the @-file picker's "no matching files" note so a mistyped command
// gets immediate feedback while typing rather than only after submission, and
// when a close built-in or recipe exists it appends a "did you mean" pointer
// reusing the same suggester the unknown-command dialog uses — matching how
// Claude Code and opencode steer a mistyped command toward its nearest match.
//
// The note fires only for a non-empty, whitespace-free command name (a bare "/"
// lists everything, and text past a space is command arguments, not a name) that
// no candidate matches even under the fuzzy subsequence fallback, so a command
// the menu can still surface never draws a spurious "no matching" note.
func slashHintNote(buffer string, st *inputState) string {
	if !strings.HasPrefix(buffer, "/") {
		return ""
	}
	name := buffer[1:]
	if name == "" || strings.ContainsAny(name, " \t") {
		return ""
	}
	if len(matchSlash(st.candidates(), buffer)) > 0 {
		return ""
	}
	note := "no matching commands"
	if s := suggestSlash(st.candidates(), name); s != "" {
		note += " — did you mean " + s + "?"
	}
	return note
}

// renderSlashHint formats the slash-completion menu for the input region. It
// returns "" when there is nothing to show, so the default prompt is byte-for-
// byte unchanged. Command names are listed without their leading slash; the
// command selected by an active Tab cycle is accented and the rest are muted.
// The list is truncated token-by-token to fit width, appending an ellipsis when
// not every match fits, so the menu never spills past one row.
func (m *model) renderSlashHint(width int) string {
	buffer := m.input.String()
	if width <= 0 {
		return ""
	}
	cmds, active := slashHintCommands(buffer, &m.inputHistory)
	if len(cmds) == 0 {
		// A "/command" prefix that matches nothing gets a quiet note (with a
		// did-you-mean pointer when a close command exists) so the user learns
		// the name is unknown while typing, the way the @-file picker reports an
		// empty search. The note is dropped when it would not fit one row, so the
		// layout height is never exceeded.
		if note := slashHintNote(buffer, &m.inputHistory); note != "" {
			const indent = "  "
			if len([]rune(indent))+len([]rune(note)) <= width {
				return indent + m.theme.Muted.Render(note)
			}
		}
		// Once the command name is settled and the user has moved on to its
		// arguments (a trailing space), keep guiding them with the argument-usage
		// placeholder instead of going blank, so the accepted arguments stay visible
		// while they type. The cue is dropped when it would not fit one row, so the
		// layout height is never exceeded.
		if hint := slashArgHint(buffer); hint != "" {
			const indent = "  "
			usage := "usage: " + buffer[:strings.IndexAny(buffer, " \t")] + " " + hint
			if len([]rune(indent))+len([]rune(usage)) <= width {
				return indent + m.theme.Muted.Render(usage)
			}
		}
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

	// While Tab-cycling, append the position within the cycle so the user can see
	// how far through the matches they have walked. It is added before the
	// overflow/description suffixes and width-guarded so the menu still never
	// spills past one row.
	if suffix := cyclePositionSuffix(active, len(cmds)); suffix != "" && used+len([]rune(suffix)) <= width {
		line += m.theme.Muted.Render(suffix)
		used += len([]rune(suffix))
	}

	if truncated {
		line += m.theme.Muted.Render(overflowSuffix(len(cmds) - len(parts)))
		return line
	}

	// Once the user has settled on a single command, append its description on
	// the same row when there is spare width, followed by the keyboard shortcut
	// that runs the same action when the command has one — so a returning user
	// discovers the faster path inline rather than only from the /keys overlay. A
	// truncated name list already ends in an ellipsis and has no room, so the gloss
	// is only added to a list that fully fit. The description and shortcut are
	// built into one suffix and width-guarded together, so the menu still never
	// spills past a single row.
	if di := slashHintDescIndex(cmds, active); di >= 0 {
		suffix := ""
		if desc := m.slashDescription(cmds[di]); desc != "" {
			suffix += " — " + desc
		}
		if key := slashCommandKeys[cmds[di]]; key != "" {
			suffix += " (" + key + ")"
		}
		if suffix != "" && used+len([]rune(suffix)) <= width {
			line += m.theme.Muted.Render(suffix)
		}
	}
	return line
}
