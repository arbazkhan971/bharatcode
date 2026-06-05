package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
	view := m.viewString()
	require.Contains(t, view, "sessions")
	require.Contains(t, view, "status")
	require.Contains(t, view, "save")

	// A non-slash buffer must not surface command names.
	m2 := newSizedModel(t)
	typeString(t, m2, "hello")
	require.NotContains(t, m2.viewString(), "sessions")
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

// TestRenderSlashHint_AmbiguousHasNoDescription asserts that while several
// commands still match, no single description is shown — the menu is just the
// name list.
func TestRenderSlashHint_AmbiguousHasNoDescription(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/s")
	view := m.viewString()
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
}
