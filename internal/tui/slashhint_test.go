package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOverflowSuffix asserts the truncation indicator reports the hidden-match
// count when matches were dropped and falls back to a bare ellipsis otherwise,
// so a non-positive count never renders a "+0" or negative tail.
func TestOverflowSuffix(t *testing.T) {
	t.Parallel()

	require.Equal(t, " … +12", overflowSuffix(12))
	require.Equal(t, " … +1", overflowSuffix(1))
	require.Equal(t, " …", overflowSuffix(0))
	require.Equal(t, " …", overflowSuffix(-3))
}

// TestSlashHintCommands_NonSlashBufferShowsNothing asserts the menu is inert
// for ordinary prose, so a normal prompt never grows a completion row.
func TestSlashHintCommands_NonSlashBufferShowsNothing(t *testing.T) {
	t.Parallel()

	var st inputState
	cmds, active := slashHintCommands("hello world", &st)
	require.Empty(t, cmds)
	require.Equal(t, -1, active)
}

// TestSlashHintCommands_AmbiguousPrefixListsMatches asserts a "/" prefix with
// several possibilities lists them in canonical order with no active selection
// (no Tab cycle has started yet).
func TestSlashHintCommands_AmbiguousPrefixListsMatches(t *testing.T) {
	t.Parallel()

	var st inputState
	cmds, active := slashHintCommands("/s", &st)
	require.Equal(t, []string{"/sessions", "/status", "/save", "/search"}, cmds)
	require.Equal(t, -1, active, "no Tab cycle is active, so nothing is selected")
}

// TestSlashHintCommands_FullyTypedUniqueShowsNothing asserts a complete,
// unambiguous command suppresses the menu — there is nothing left to discover.
func TestSlashHintCommands_FullyTypedUniqueShowsNothing(t *testing.T) {
	t.Parallel()

	var st inputState
	cmds, active := slashHintCommands("/help", &st)
	require.Empty(t, cmds)
	require.Equal(t, -1, active)
}

// TestSlashHintCommands_NarrowingPrefixStillHints asserts an in-progress prefix
// that narrows to a single command still shows it as a confirmation hint.
func TestSlashHintCommands_NarrowingPrefixStillHints(t *testing.T) {
	t.Parallel()

	var st inputState
	cmds, active := slashHintCommands("/he", &st)
	require.Equal(t, []string{"/help"}, cmds)
	require.Equal(t, -1, active)
}

// TestSlashHintCommands_ActiveCycleMarksSelection asserts that while a Tab cycle
// is active the full match set is returned with the selected index marked, so
// the menu stays stable as the user Tabs even though the buffer now equals a
// single command that would otherwise match only itself.
func TestSlashHintCommands_ActiveCycleMarksSelection(t *testing.T) {
	t.Parallel()

	st := inputState{
		completionMatches: []string{"/sessions", "/status", "/save"},
		completionIndex:   1,
	}
	cmds, active := slashHintCommands("/status", &st)
	require.Equal(t, []string{"/sessions", "/status", "/save"}, cmds)
	require.Equal(t, 1, active, "the buffer equals the cycle's second match")
}

// TestRenderSlashHint_VisibleAfterTyping is the end-to-end contract: typing an
// ambiguous slash prefix surfaces the matching command names in the rendered
// view, and a plain prompt shows none of them.
func TestRenderSlashHint_VisibleAfterTyping(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/s")
	// The menu highlights the matched runes, so command names are split by ANSI
	// styling spans; strip them to assert the visible text.
	view := stripANSI(m.viewString())
	require.Contains(t, view, "sessions")
	require.Contains(t, view, "status")
	require.Contains(t, view, "save")

	// A non-slash buffer must not surface command names.
	m2 := newSizedModel(t)
	typeString(t, m2, "hello")
	require.NotContains(t, stripANSI(m2.viewString()), "sessions")
}

// TestSlashHintDescIndex selects the command whose gloss is shown: the active
// Tab selection when cycling, the sole match when a prefix narrows to one, and
// nothing while several candidates remain.
func TestSlashHintDescIndex(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, slashHintDescIndex([]string{"/a", "/b", "/c"}, 1),
		"the active cycle selection is glossed")
	require.Equal(t, 0, slashHintDescIndex([]string{"/help"}, -1),
		"a sole remaining match is glossed")
	require.Equal(t, -1, slashHintDescIndex([]string{"/a", "/b"}, -1),
		"an ambiguous list has no single command to describe")
	require.Equal(t, -1, slashHintDescIndex(nil, 5),
		"an out-of-range active index is ignored")
}

// TestRenderSlashHint_NarrowedShowsDescription asserts that once a prefix
// narrows to a single command the rendered hint carries its one-line gloss, so
// the user can confirm what the command does without opening /help.
func TestRenderSlashHint_NarrowedShowsDescription(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/diff")
	require.NotContains(t, m.viewString(), "show the latest edit diff",
		"a fully typed unique command suppresses the menu entirely")

	m2 := newSizedModel(t)
	typeString(t, m2, "/dif")
	view := m2.viewString()
	require.Contains(t, view, "diff")
	require.Contains(t, view, "show the latest edit diff",
		"the narrowed single match carries its description")
}

// TestSlashDescription_PrefersBuiltinThenDynamic asserts the gloss lookup
// returns the built-in table entry first and falls back to a dynamic command's
// captured description, so recipes and custom prompts are described inline like
// the built-ins while a name overlap still resolves to the built-in.
func TestSlashDescription_PrefersBuiltinThenDynamic(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.inputHistory.setDynamicDescriptions(map[string]string{
		"/triage": "sort the open issues",
		"/diff":   "a dynamic gloss that must not win",
	})

	require.Equal(t, "sort the open issues", m.slashDescription("/triage"),
		"a dynamic command is described by its captured gloss")
	require.Equal(t, "show the latest edit diff", m.slashDescription("/diff"),
		"the built-in table wins over a like-named dynamic command")
	require.Empty(t, m.slashDescription("/unknown"),
		"an undescribed command has no gloss")
}

// TestRenderSlashHint_NarrowedShowsDynamicDescription asserts that narrowing to a
// single dynamic recipe/prompt command renders its captured gloss on the same
// row, the way a built-in's description is shown.
func TestRenderSlashHint_NarrowedShowsDynamicDescription(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.inputHistory.setDynamicCommands([]string{"/triage"})
	m.inputHistory.setDynamicDescriptions(map[string]string{"/triage": "sort the open issues"})

	typeString(t, m, "/triag")
	view := stripANSI(m.viewString())
	require.Contains(t, view, "triage")
	require.Contains(t, view, "sort the open issues",
		"the narrowed dynamic command carries its captured description")
}

// TestRenderSlashHint_AmbiguousHasNoDescription asserts that while several
// commands still match, no single description is shown — the menu is just the
// name list.
func TestRenderSlashHint_AmbiguousHasNoDescription(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/s")
	view := stripANSI(m.viewString())
	require.Contains(t, view, "sessions")
	require.NotContains(t, view, "restore a recent session",
		"an ambiguous prefix shows names only, no gloss")
}

// TestRenderSlashHint_DescriptionDroppedWhenNarrow asserts the gloss is omitted
// rather than wrapping when the row is too narrow to hold both the command name
// and its description, keeping the hint on a single line.
func TestRenderSlashHint_DescriptionDroppedWhenNarrow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.width = 10
	typeString(t, m, "/dif")
	hint := m.renderSlashHint(m.width)
	require.NotEmpty(t, hint)
	require.NotContains(t, hint, "\n", "the hint must stay on one row")
	require.NotContains(t, hint, "show the latest edit diff",
		"no spare width, so the description is dropped")
}

// TestRenderSlashHint_FitsOneRow asserts the menu never spills past a single
// row regardless of how many commands match, truncating with an ellipsis.
func TestRenderSlashHint_FitsOneRow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.width = 24
	typeString(t, m, "/")
	hint := m.renderSlashHint(m.width)
	require.NotEmpty(t, hint)
	require.NotContains(t, hint, "\n", "the menu must stay on one row")
	require.Contains(t, hint, "…", "an over-long match set is truncated")
	require.Regexp(t, `\+\d+`, hint, "truncation reports how many matches are hidden")
}

// TestRenderSlashHint_HighlightsPrefixMatch asserts the runes a prefix matched
// are accented while the rest of each command name stays muted, so the menu
// shows why every entry qualified — the same emphasis the @-file picker applies.
func TestRenderSlashHint_HighlightsPrefixMatch(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/se")
	hint := m.renderSlashHint(m.width)
	require.NotEmpty(t, hint)
	// The visible text is unchanged: highlighting only re-styles, never edits.
	require.Contains(t, stripANSI(hint), "search")
	// The matched "se" prefix is accented, and the name is no longer one muted
	// span the way an unmatched entry would be.
	require.Contains(t, hint, m.theme.Accent.Render("se"))
	require.NotContains(t, hint, m.theme.Muted.Render("search"),
		"a matched name must not render as a single muted span")
}

// TestRenderSlashHint_HighlightsSubsequenceFallback asserts that when no command
// shares the typed prefix, the fuzzy subsequence fallback still accents the
// scattered runes it matched, revealing why an off-prefix token reached a
// command (here "/srch" → "search").
func TestRenderSlashHint_HighlightsSubsequenceFallback(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/srch")
	hint := m.renderSlashHint(m.width)
	require.NotEmpty(t, hint)
	require.Contains(t, stripANSI(hint), "search",
		"the subsequence fallback still surfaces the command")
	require.NotContains(t, hint, m.theme.Muted.Render("search"),
		"the matched runes are accented, so the name is not one muted span")
}

// TestSlashHintNote_UnknownCommand asserts a "/command" that matches nothing —
// not even under the fuzzy subsequence fallback — yields the "no matching
// commands" note, so a mistyped name gets feedback while typing.
func TestSlashHintNote_UnknownCommand(t *testing.T) {
	t.Parallel()

	var st inputState
	require.Equal(t, "no matching commands", slashHintNote("/zzzqq", &st))
}

// TestSlashHintNote_SuggestsClosest asserts the note points a near-miss command
// at its closest real one, reusing the same suggester the unknown-command dialog
// uses, so "/exprot" steers toward "/export" inline.
func TestSlashHintNote_SuggestsClosest(t *testing.T) {
	t.Parallel()

	var st inputState
	require.Equal(t, "no matching commands — did you mean /export?", slashHintNote("/exprot", &st))
}

// TestSlashHintNote_SilentCases asserts the note stays empty wherever feedback
// would be noise: ordinary prose, a bare "/", a name the menu can still surface
// (prefix or fuzzy), a fully typed command, and text past a space (arguments,
// not a command name).
func TestSlashHintNote_SilentCases(t *testing.T) {
	t.Parallel()

	var st inputState
	for _, buffer := range []string{
		"hello world", // not a slash command
		"/",           // bare slash lists everything
		"/s",          // a real prefix the menu lists
		"/srch",       // reachable via the fuzzy subsequence fallback
		"/help",       // a complete, valid command
		"/diff foo",   // a command name plus arguments
	} {
		require.Empty(t, slashHintNote(buffer, &st), "buffer %q must show no note", buffer)
	}
}

// TestRenderSlashHint_UnknownCommandNote is the end-to-end contract: typing an
// unknown "/command" surfaces the note in the rendered view, while a token the
// fuzzy fallback can still resolve surfaces the command name rather than the
// note.
func TestRenderSlashHint_UnknownCommandNote(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/zzzqq")
	require.Contains(t, stripANSI(m.viewString()), "no matching commands")

	m2 := newSizedModel(t)
	typeString(t, m2, "/srch")
	view := stripANSI(m2.viewString())
	require.Contains(t, view, "search", "a fuzzy-matchable token still lists the command")
	require.NotContains(t, view, "no matching commands",
		"a resolvable token must not draw the unknown-command note")
}

// TestRenderSlashHint_NoteDroppedWhenNarrow asserts the note is omitted rather
// than wrapping when the row is too narrow to hold it, keeping the input region
// height unchanged.
func TestRenderSlashHint_NoteDroppedWhenNarrow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.width = 8
	typeString(t, m, "/zzzqq")
	require.Empty(t, m.renderSlashHint(m.width),
		"no room for the note, so nothing is rendered")
}
