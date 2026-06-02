package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// matchLine returns a distinct, searchable line that carries the search needle,
// tagged with n so each match is individually identifiable in the rendered view.
func matchLine(n int) string {
	return fmt.Sprintf("ZZNEEDLE-HIT-%d", n)
}

// seedScrollableTranscript fills the chat with a tall, scrollable user message
// whose lines are individually searchable. The lines at the given hit indices
// carry the search needle (each tagged so the active match can be identified);
// all other lines are inert filler. It returns the total line count so callers
// can reason about scroll bounds. Seeding as a single user message keeps the
// content plain-wrapped (not glamour-rendered), so every marker survives the
// render verbatim and the scroll window can be asserted exactly.
func seedScrollableTranscript(m *model, total int, hits []int) {
	hitTag := make(map[int]int, len(hits))
	for tag, idx := range hits {
		hitTag[idx] = tag
	}
	lines := make([]string, total)
	for i := 0; i < total; i++ {
		if tag, ok := hitTag[i]; ok {
			lines[i] = matchLine(tag)
		} else {
			lines[i] = "filler-" + lineSuffix(i)
		}
	}
	m.chat.Append(message.Message{
		ID:      "u1",
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: strings.Join(lines, "\n")}},
	})
}

// TestSlashSearch_FindsMatchesAndPositionsViewport is the /search contract test:
// searching a seeded transcript must find every occurrence of the term, report
// the count, and position the viewport so the first match is visible.
func TestSlashSearch_FindsMatchesAndPositionsViewport(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H
	require.Greater(t, chatH, 0)

	// Three matches spread across a transcript far taller than the viewport, so
	// reaching a match always requires real scrolling.
	total := chatH * 4
	hits := []int{3, chatH + 5, 3*chatH + 2}
	seedScrollableTranscript(m, total, hits)

	rendered := func() string { return stripANSI(m.renderMain()) }

	// The first match is near the top, off-screen while the view is bottom-anchored.
	require.NotContains(t, rendered(), matchLine(0),
		"the first match must be off-screen before searching (bottom-anchored)")

	m.input.WriteString("/search ZZNEEDLE")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Len(t, m.search.matches, len(hits),
		"/search must find every line containing the term")
	require.Equal(t, 0, m.search.current, "a fresh search starts on the first match")
	require.True(t, m.dialogs.Contains("search"), "/search must surface a result dialog")
	require.Contains(t, plainText(m.dialogs.Render(200)), fmt.Sprintf("of %d", len(hits)),
		"the dialog must report the match count")
	require.Contains(t, rendered(), matchLine(0),
		"/search must scroll the first match into the visible window")
}

// TestSearchNextPrev_CyclesThroughMatches asserts the next/prev keys walk the
// matches in order and wrap around at both ends, repositioning the viewport so
// the active match is visible at each step.
func TestSearchNextPrev_CyclesThroughMatches(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H

	total := chatH * 4
	hits := []int{3, chatH + 5, 3*chatH + 2}
	seedScrollableTranscript(m, total, hits)

	rendered := func() string { return stripANSI(m.renderMain()) }

	m.input.WriteString("/search ZZNEEDLE")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, 0, m.search.current)
	require.Contains(t, rendered(), matchLine(0), "search anchors on the first match")
	// Dismiss the transient result dialog so the navigation keys reach the chat
	// rather than the dialog's key handler.
	m.dialogs.Pop()

	// Ctrl+/ advances to the second, then the third match; each becomes visible.
	_, _ = m.Update(ctrlKey('/'))
	require.Equal(t, 1, m.search.current, "ctrl+/ must advance to the next match")
	require.Contains(t, rendered(), matchLine(1), "the second match must scroll into view")

	_, _ = m.Update(ctrlKey('/'))
	require.Equal(t, 2, m.search.current)
	require.Contains(t, rendered(), matchLine(2), "the third match must scroll into view")

	// One more next wraps back to the first match.
	_, _ = m.Update(ctrlKey('/'))
	require.Equal(t, 0, m.search.current, "ctrl+/ past the last match wraps to the first")
	require.Contains(t, rendered(), matchLine(0))

	// Ctrl+\ steps backward, wrapping from the first match to the last.
	_, _ = m.Update(ctrlKey('\\'))
	require.Equal(t, 2, m.search.current, "ctrl+\\ before the first match wraps to the last")
	require.Contains(t, rendered(), matchLine(2))

	_, _ = m.Update(ctrlKey('\\'))
	require.Equal(t, 1, m.search.current, "ctrl+\\ must step to the previous match")
	require.Contains(t, rendered(), matchLine(1))
}

// TestSlashSearch_RepeatedSameTermAdvances asserts re-running /search with the
// same term walks to the next match rather than re-anchoring to the first, so
// repeated invocations cycle like an editor's search-again.
func TestSlashSearch_RepeatedSameTermAdvances(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H

	hits := []int{3, chatH + 5, 3*chatH + 2}
	seedScrollableTranscript(m, chatH*4, hits)

	search := func(term string) {
		m.input.Reset()
		m.input.WriteString(term)
		_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
		m.dialogs.Pop()
	}

	search("/search ZZNEEDLE")
	require.Equal(t, 0, m.search.current)

	search("/search ZZNEEDLE")
	require.Equal(t, 1, m.search.current, "re-running the same term must advance to the next match")

	// A bare "/search" also advances the active search.
	search("/search")
	require.Equal(t, 2, m.search.current, "a bare /search advances within the active search")
}

// TestSlashSearch_NoMatch_ReportsAndStaysInert asserts a term that is absent
// surfaces a no-match dialog, clears any prior search, and leaves the viewport
// anchored at the bottom (no spurious scroll).
func TestSlashSearch_NoMatch_ReportsAndStaysInert(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H

	seedScrollableTranscript(m, chatH*4, []int{3})

	m.input.WriteString("/search definitely-absent-xyz")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.False(t, m.search.active(), "a no-match search must leave no active matches")
	require.Equal(t, 0, m.chatScroll, "a no-match search must not scroll the viewport")
	require.Contains(t, plainText(m.dialogs.Render(200)), "No matches",
		"a no-match search must report that nothing was found")
}

// TestSearchNextPrev_NoActiveSearch_Inert asserts the next/prev keys are inert
// when no search is active: they must not move the scroll offset or panic.
func TestSearchNextPrev_NoActiveSearch_Inert(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	appendMsg(m, "a1", message.RoleAssistant, "no search has been run")
	require.Equal(t, 0, m.chatScroll)

	_, _ = m.Update(ctrlKey('/'))
	_, _ = m.Update(ctrlKey('\\'))
	require.False(t, m.search.active())
	require.Equal(t, 0, m.chatScroll, "next/prev with no active search must not move the viewport")
}

// TestSlashSearch_Bare_NoActiveSearch_PromptsForTerm asserts that "/search" with
// no argument and no active search explains the usage instead of doing nothing.
func TestSlashSearch_Bare_NoActiveSearch_PromptsForTerm(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	appendMsg(m, "u1", message.RoleUser, "hello")

	m.input.WriteString("/search")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.dialogs.Contains("search"))
	require.Contains(t, plainText(m.dialogs.Render(200)), "Usage",
		"a bare /search with no active search must prompt for a term")
}
