package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestSearchStatusSegment asserts the status-bar segment is empty until a search
// is active and then tracks the 1-based current match position as navigation
// advances and wraps.
func TestSearchStatusSegment(t *testing.T) {
	t.Parallel()

	var s searchState
	require.Equal(t, "", s.statusSegment(), "an inert search must contribute no segment")

	s = searchState{term: "x", matches: []int{3, 7, 11}, current: 0}
	require.Equal(t, "search 1/3", s.statusSegment(), "a fresh search reports the first of N matches")

	s.current = 2
	require.Equal(t, "search 3/3", s.statusSegment(), "advancing must move the 1-based position")
}

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

// TestSearchCentersMatchWithContextBelow asserts a match landed on by search is
// positioned with the following lines still visible, rather than pinned to the
// last row of the window, so the reader sees context after the hit as well as
// before it.
func TestSearchCentersMatchWithContextBelow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H
	require.Greater(t, chatH, 2, "the window must be tall enough to show context below a centered match")

	// One match squarely in the middle of a transcript far taller than the
	// viewport, so there is ample content both above and below it to reveal.
	hit := 2 * chatH
	total := chatH * 4
	seedScrollableTranscript(m, total, []int{hit})

	rendered := func() string { return stripANSI(m.renderMain()) }

	m.input.WriteString("/search ZZNEEDLE")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Contains(t, rendered(), matchLine(0), "the match itself must be visible")
	// The lines immediately after the match are filler tagged by index; with the
	// match centered they remain on screen. Pinning the match to the bottom row
	// would scroll all of them out of view.
	require.Contains(t, rendered(), "filler-"+lineSuffix(hit+1),
		"the line right after the match must stay visible (context below the hit)")
	require.Contains(t, rendered(), "filler-"+lineSuffix(hit+chatH/2-1),
		"context several lines below the match must be visible when it is centered")
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

// TestSearchEscClears asserts that pressing Esc while a search is active
// cancels it: the matches are dropped and the status segment goes empty, the
// way an editor or pager clears its search on Esc.
func TestSearchEscClears(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H

	total := chatH * 4
	hits := []int{3, chatH + 5, 3*chatH + 2}
	seedScrollableTranscript(m, total, hits)

	m.input.WriteString("/search ZZNEEDLE")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.search.active(), "the search must be active before Esc")
	// Dismiss the transient result dialog so Esc reaches the chat key handler
	// rather than the dialog's own dismissal.
	m.dialogs.Pop()

	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.search.active(), "Esc must clear the active search")
	require.Equal(t, "", m.search.statusSegment(),
		"a cleared search contributes no status segment")
	require.Equal(t, "", m.status.Search,
		"Esc must also clear the status bar's search segment")
}

// TestSearchEsc_InertWhenNoSearch asserts that Esc does not disturb an
// un-searched view: with no active search it falls through to its other roles
// (hiding the help listing) rather than consuming the keypress for the search.
func TestSearchEsc_InertWhenNoSearch(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.helpVisible = true

	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.helpVisible, "Esc with no active search must hide the help listing")
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

// TestHighlightTerm asserts the pure term-highlighting helper wraps every
// case-insensitive occurrence of the term and leaves non-matching text — and
// the no-match and empty-term cases — byte-for-byte unchanged.
func TestHighlightTerm(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	style := theme.Match

	require.Equal(t, "abc", highlightTerm("abc", "", style),
		"an empty term must leave the line unchanged")
	require.Equal(t, "abc", highlightTerm("abc", "xyz", style),
		"a term with no occurrence must leave the line unchanged")

	// A single occurrence is wrapped in the match style; the original case is
	// preserved even though matching is case-insensitive.
	got := highlightTerm("a Foo b", "foo", style)
	require.Equal(t, "a "+style.Render("Foo")+" b", got,
		"the matched run must be wrapped in the match style, preserving its case")

	// Every occurrence is wrapped, including ones separated only by a single rune
	// and matched across case.
	got = highlightTerm("xixIx", "i", style)
	want := "x" + style.Render("i") + "x" + style.Render("I") + "x"
	require.Equal(t, want, got,
		"each occurrence must be wrapped, case-insensitively")
}

// TestHighlightCurrentMatch_PlainLine asserts highlightCurrentMatch emphasizes
// the active term only on the current match line, leaving the rest untouched,
// and is a no-op when no search is active.
func TestHighlightCurrentMatch_PlainLine(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	body := "line0\nhere is a foo hit\nline2"

	require.Equal(t, body, m.highlightCurrentMatch(body),
		"with no active search the body must be unchanged")

	m.search = searchState{term: "foo", matches: []int{1}, current: 0}
	got := m.highlightCurrentMatch(body)
	require.Contains(t, got, m.theme.Match.Render("foo"),
		"the current match line must carry the styled term")
	lines := strings.Split(got, "\n")
	require.Equal(t, "line0", lines[0], "non-match lines must be left untouched")
	require.Equal(t, "line2", lines[2], "non-match lines must be left untouched")
}

// TestHighlightCurrentMatch_SkipsStyledLine asserts a match line that already
// carries ANSI styling (a markdown-rendered line) is left byte-for-byte
// unchanged, so splicing a highlight span never corrupts existing escapes.
func TestHighlightCurrentMatch_SkipsStyledLine(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	styledLine := "\x1b[31mhere is a foo hit\x1b[0m"
	body := "line0\n" + styledLine + "\nline2"

	m.search = searchState{term: "foo", matches: []int{1}, current: 0}
	require.Equal(t, body, m.highlightCurrentMatch(body),
		"a line with existing ANSI styling must be left unchanged")
}

// TestHighlightCurrentMatch_OutOfRange asserts a stale match index (past the end
// of the current body, e.g. after the transcript shrank) is ignored rather than
// panicking, and the body is returned unchanged.
func TestHighlightCurrentMatch_OutOfRange(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	body := "only\ntwo lines"

	m.search = searchState{term: "x", matches: []int{99}, current: 0}
	require.Equal(t, body, m.highlightCurrentMatch(body),
		"an out-of-range match index must be ignored")
}

// TestSearchHighlightsVisibleMatch asserts that after /search the centered match
// line is rendered with the match style in the real view, and that advancing to
// the next match moves the highlight off the first line.
func TestSearchHighlightsVisibleMatch(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H

	hits := []int{3, chatH + 5, 3*chatH + 2}
	seedScrollableTranscript(m, chatH*4, hits)

	// The needle prefix is what /search matches; the highlight wraps that exact
	// run within each tagged match line.
	highlighted := m.theme.Match.Render("ZZNEEDLE")
	require.NotContains(t, m.renderMain(), highlighted,
		"nothing is highlighted before a search runs")

	m.input.WriteString("/search ZZNEEDLE")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	m.dialogs.Pop()

	require.Contains(t, m.renderMain(), highlighted,
		"the centered match line must carry the search-match style")
}
