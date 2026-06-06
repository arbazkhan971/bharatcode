package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestFilterHelpLines_EmptyFilterReturnsAll proves a blank or whitespace-only
// filter returns every line unchanged, so a bare "/help" is unchanged.
func TestFilterHelpLines_EmptyFilterReturnsAll(t *testing.T) {
	lines := []string{"/help - list commands", "/clear - clear visible chat", "/model - open model picker"}

	require.Equal(t, lines, filterHelpLines(lines, ""))
	require.Equal(t, lines, filterHelpLines(lines, "   "))
}

// TestFilterHelpLines_SingleTermNarrows proves a single-term filter keeps only
// lines that contain the term (case-insensitive), dropping the rest.
func TestFilterHelpLines_SingleTermNarrows(t *testing.T) {
	lines := []string{
		"/help - list commands",
		"/clear - clear visible chat",
		"/model - open model picker",
		"/tab - open or switch session tabs",
	}

	got := filterHelpLines(lines, "tab")
	require.Len(t, got, 1)
	require.Equal(t, "/tab - open or switch session tabs", got[0])
}

// TestFilterHelpLines_CaseInsensitive proves the filter folds case, so "TAB"
// finds the same lines as "tab".
func TestFilterHelpLines_CaseInsensitive(t *testing.T) {
	lines := []string{
		"/tab - open or switch session tabs",
		"/model - open model picker",
	}

	require.Equal(t, filterHelpLines(lines, "tab"), filterHelpLines(lines, "TAB"))
}

// TestFilterHelpLines_MultiTermAND proves a multi-word filter requires all
// terms to appear in a line, so "/help diff revert" surfaces only commands
// that mention both words.
func TestFilterHelpLines_MultiTermAND(t *testing.T) {
	lines := []string{
		"/diff - show the latest edit diff",
		"/revert [apply|force] - undo this session's file changes",
		"/export [md|html] - write the transcript to a file",
	}

	// "diff" alone matches the diff line.
	got := filterHelpLines(lines, "diff")
	require.Len(t, got, 1)
	require.Contains(t, got[0], "/diff")

	// "session file" must match lines mentioning both words; /revert qualifies.
	got2 := filterHelpLines(lines, "session file")
	require.Len(t, got2, 1)
	require.Contains(t, got2[0], "/revert")
}

// TestFilterHelpLines_NoSubstringFallsBackToSubsequence proves that when the
// substring pass finds nothing, a fuzzy subsequence fallback takes over so a
// run-together abbreviation still surfaces its command.
func TestFilterHelpLines_NoSubstringFallsBackToSubsequence(t *testing.T) {
	lines := []string{
		"/diff - show the latest edit diff",
		"/revert [apply|force] - undo this session's file changes",
		"/export [md|html] - write the transcript to a file",
	}

	// "dflt" is not a substring of any line, but its letters appear in order
	// within "latest edit diff", so the fuzzy fallback should surface /diff.
	got := filterHelpLines(lines, "dflt")
	require.NotEmpty(t, got, "fuzzy fallback must surface /diff for abbreviation 'dflt'")
	var found bool
	for _, line := range got {
		if strings.Contains(line, "/diff") {
			found = true
		}
	}
	require.True(t, found, "/diff should surface through the subsequence fallback")
}

// TestFilterHelpLines_TrulyUnmatchedReturnsNil proves a query matching nothing
// even as a subsequence yields nil, so the caller can show a "no commands
// match" note rather than an empty list.
func TestFilterHelpLines_TrulyUnmatchedReturnsNil(t *testing.T) {
	lines := []string{"/help - list commands", "/clear - clear visible chat"}
	require.Nil(t, filterHelpLines(lines, "zzzqqqxxx"))
}

// TestSlashHelpBodyFiltered_EmptyFilterIsFullBody proves a blank filter returns
// the full listing as a joined string, so a bare "/help" opens everything.
func TestSlashHelpBodyFiltered_EmptyFilterIsFullBody(t *testing.T) {
	m := newSizedModel(t)

	full := strings.Join(m.slashHelpLines(), "\n")
	require.Equal(t, full, m.slashHelpBodyFiltered(""))
	require.Equal(t, full, m.slashHelpBodyFiltered("   "))
}

// TestSlashHelpBodyFiltered_FilterNarrowsWithCountHeader proves a successful
// filter leads with an "M of N commands match" count header and contains only
// matching lines, mirroring keybindingHelpBodyFiltered's format.
func TestSlashHelpBodyFiltered_FilterNarrowsWithCountHeader(t *testing.T) {
	m := newSizedModel(t)

	body := m.slashHelpBodyFiltered("tab")
	all := m.slashHelpLines()
	matched := filterHelpLines(all, "tab")

	noun := "commands"
	if len(matched) == 1 {
		noun = "command"
	}
	expectedHeader := fmt.Sprintf("%d of %d %s match %q", len(matched), len(all), noun, "tab")
	require.True(t, strings.HasPrefix(body, expectedHeader), "body must lead with the count header")
	require.True(t, strings.HasPrefix(body, expectedHeader+"\n\n"), "a blank line must separate the header from the listing")

	// Every matching line appears in the body.
	for _, line := range matched {
		require.Contains(t, body, line)
	}
	// A line that should not match is absent.
	require.NotContains(t, body, "/diff - show the latest edit diff")
}

// TestSlashHelpBodyFiltered_NoMatchNote proves a filter matching nothing returns
// a quiet explanatory note rather than an empty body.
func TestSlashHelpBodyFiltered_NoMatchNote(t *testing.T) {
	m := newSizedModel(t)

	body := m.slashHelpBodyFiltered("zzzqqqxxx")
	require.Contains(t, body, "No commands match")
	require.Contains(t, body, "zzzqqqxxx")
}

// TestSlashHelpBodyFiltered_SingularNounForOneMatch proves the count noun
// agrees in number when exactly one command matches.
func TestSlashHelpBodyFiltered_SingularNounForOneMatch(t *testing.T) {
	m := newSizedModel(t)

	// "/yolo" is the only command mentioning "bypass"; it should be the sole
	// match so the singular noun is used.
	body := m.slashHelpBodyFiltered("bypass")
	require.Contains(t, body, "command match", "a single match uses the singular noun")
	require.NotContains(t, body, "commands match", "a single match must not use the plural noun")
}

// TestSlashHelp_OpensDialogOnSubmit proves that submitting "/help" opens the
// help dialog rather than rendering inline in the chat, matching how "/keys"
// opens a scrollable shortcut overlay.
func TestSlashHelp_OpensDialogOnSubmit(t *testing.T) {
	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	m.input.WriteString("/help")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.dialogs.Contains("help"), "/help must push the help dialog onto the stack")
}

// TestSlashHelp_FilteredDialogNarrows proves "/help <filter>" opens a dialog
// whose body contains only commands matching the filter, and the dialog title
// echoes the filter.
func TestSlashHelp_FilteredDialogNarrows(t *testing.T) {
	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	m.input.WriteString("/help tab")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.dialogs.Contains("help"), "/help <filter> must push the help dialog")
	out := m.dialogs.Render(200)
	require.Contains(t, out, "tab", "dialog body must mention the filter term")
	require.Contains(t, out, "Commands · tab", "dialog title must echo the filter")
}
