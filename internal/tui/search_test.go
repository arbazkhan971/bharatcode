package tui

import (
	"fmt"
	"regexp"
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

// TestSearchWrapAnnounced asserts the status segment gains a "(wrapped)" note
// only on the navigation step that rolls past an end of the buffer — advancing
// off the last match back to the first, or stepping back off the first to the
// last — and drops the note again on any in-range step, the way vim and less
// announce a search that wraps around the end of the file.
func TestSearchWrapAnnounced(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// Three matches; start anchored on the first, as startSearch leaves it.
	m.search = searchState{term: "x", matches: []int{2, 5, 9}, current: 0}
	require.Equal(t, "search 1/3", m.search.statusSegment(),
		"a fresh search must not be marked wrapped")

	// Advancing within range moves the position without a wrap note.
	m.searchNext()
	require.Equal(t, "search 2/3", m.search.statusSegment(),
		"an in-range next must not announce a wrap")

	// Advancing off the last match rolls back to the first and announces it.
	m.searchNext()
	m.searchNext()
	require.Equal(t, "search 1/3 (wrapped)", m.search.statusSegment(),
		"advancing past the last match must announce the wrap")

	// A following in-range step clears the note again.
	m.searchNext()
	require.Equal(t, "search 2/3", m.search.statusSegment(),
		"the wrap note must clear on the next in-range step")

	// Stepping back off the first match rolls to the last and announces it.
	m.searchPrev()
	m.searchPrev()
	require.Equal(t, "search 3/3 (wrapped)", m.search.statusSegment(),
		"stepping back past the first match must announce the wrap")
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

// TestSearchEsc_InertWhenNoSearch asserts that Esc does not consume the
// keypress for the search when no search is active — it is a no-op for the
// search subsystem and leaves searchState empty.
func TestSearchEsc_InertWhenNoSearch(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.search.active(), "Esc with no active search must leave search inactive")
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

	// Smart case: a term carrying an upper-case letter highlights only the
	// exact-case run, matching what SearchLines navigated, so a capital in the
	// query no longer lights up the lower-case occurrence the search skips.
	got = highlightTerm("Foo and foo", "Foo", style)
	require.Equal(t, style.Render("Foo")+" and foo", got,
		"an upper-case term must highlight only the exact-case occurrence")
}

// TestHighlightMatches_PlainLine asserts highlightMatches emphasizes the active
// term on the current match line, leaving non-match lines untouched, and is a
// no-op when no search is active.
func TestHighlightMatches_PlainLine(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	body := "line0\nhere is a foo hit\nline2"

	require.Equal(t, body, m.highlightMatches(body),
		"with no active search the body must be unchanged")

	m.search = searchState{term: "foo", matches: []int{1}, current: 0}
	got := m.highlightMatches(body)
	require.Contains(t, got, m.theme.Match.Render("foo"),
		"the current match line must carry the styled term")
	lines := strings.Split(got, "\n")
	require.Equal(t, "line0", lines[0], "non-match lines must be left untouched")
	require.Equal(t, "line2", lines[2], "non-match lines must be left untouched")
}

// TestHighlightMatches_AllHits asserts highlightMatches marks every match line —
// the current one in the reverse-video Match style and the others in the
// MatchOther style — so all occurrences are visible at once with the active hit
// distinguished.
func TestHighlightMatches_AllHits(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	body := "foo one\nfoo two\nfoo three"

	m.search = searchState{term: "foo", matches: []int{0, 1, 2}, current: 1}
	got := m.highlightMatches(body)
	lines := strings.Split(got, "\n")

	require.Contains(t, lines[1], m.theme.Match.Render("foo"),
		"the current match line uses the reverse-video Match style")
	require.NotContains(t, lines[1], m.theme.MatchOther.Render("foo"),
		"the current line must not also carry the secondary style")
	for _, i := range []int{0, 2} {
		require.Contains(t, lines[i], m.theme.MatchOther.Render("foo"),
			"a non-current match line uses the MatchOther style")
		require.NotContains(t, lines[i], m.theme.Match.Render("foo"),
			"a non-current line must not carry the current-match style")
	}
}

// TestHighlightMatches_HighlightsWithinStyledLine asserts that a match line
// already carrying ANSI styling (every transcript line does now: the role accent
// bar and colored label) gets the term highlighted within its plain-text spans,
// without corrupting the existing escapes — so search still works once the
// transcript is styled. A plain sibling match is also highlighted.
func TestHighlightMatches_HighlightsWithinStyledLine(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	styledLine := "\x1b[31mhere is a foo hit\x1b[0m"
	body := "plain foo line\n" + styledLine

	m.search = searchState{term: "foo", matches: []int{0, 1}, current: 1}
	got := m.highlightMatches(body)
	lines := strings.Split(got, "\n")
	// The styled current-match line gets its plain-span "foo" emphasized while its
	// existing red styling is preserved.
	require.Contains(t, lines[1], m.theme.Match.Render("foo"),
		"the term inside a styled line must be highlighted")
	require.Contains(t, lines[1], "\x1b[31m",
		"the line's existing ANSI styling must be preserved")
	require.Contains(t, lines[0], m.theme.MatchOther.Render("foo"),
		"a plain sibling match must still be highlighted")
}

// TestHighlightMatches_OutOfRange asserts a stale match index (past the end of
// the current body, e.g. after the transcript shrank) is skipped rather than
// panicking, while valid siblings are still highlighted.
func TestHighlightMatches_OutOfRange(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	body := "only\ntwo lines"

	m.search = searchState{term: "two", matches: []int{1, 99}, current: 0}
	got := m.highlightMatches(body)
	require.Contains(t, got, m.theme.Match.Render("two"),
		"the in-range match must still be highlighted")
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

// TestParseRegexTerm asserts the /pattern/ parser returns a compiled regexp for
// valid envelopes and nil for terms that are not /…/-wrapped.
func TestParseRegexTerm(t *testing.T) {
	t.Parallel()

	// A term without a leading slash is treated as a literal — no regexp.
	re, err := parseRegexTerm("plain")
	require.NoError(t, err)
	require.Nil(t, re, "a plain term must not be treated as a regex")

	// A term with a leading slash but no closing slash is also literal.
	re, err = parseRegexTerm("/unclosed")
	require.NoError(t, err)
	require.Nil(t, re, "a term with no closing slash must not be treated as a regex")

	// /pattern/ produces a case-sensitive regexp.
	re, err = parseRegexTerm("/foo.*/")
	require.NoError(t, err)
	require.NotNil(t, re, "/pattern/ must produce a compiled regexp")
	require.True(t, re.MatchString("foobar"), "the compiled pattern must match its target")
	require.False(t, re.MatchString("FOO"), "a case-sensitive pattern must not match wrong case")

	// /pattern/i produces a case-insensitive regexp.
	re, err = parseRegexTerm("/foo.*/i")
	require.NoError(t, err)
	require.NotNil(t, re, "/pattern/i must produce a compiled regexp")
	require.True(t, re.MatchString("FOOBAR"), "/i pattern must match regardless of case")

	// An invalid pattern returns an error.
	re, err = parseRegexTerm("/[invalid/")
	require.Error(t, err, "an invalid regex must return a compile error")
	require.Nil(t, re)
}

// TestSlashSearch_RegexSyntax_FindsMatches asserts that a /pattern/ term
// searches the transcript as a regex and finds lines whose content matches the
// pattern — including alternation patterns that literal search cannot express.
func TestSlashSearch_RegexSyntax_FindsMatches(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// Use lines where only a regex with alternation can select exactly two of them.
	m.chat.Append(message.Message{
		ID:   "u1",
		Role: message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: strings.Join([]string{
				"error: disk full",
				"warn: low memory",
				"error: timeout",
				"info: ok",
			}, "\n"),
		}},
	})

	// /disk|timeout/ matches only the two error lines and not the warn/info lines.
	m.input.WriteString("/search /disk|timeout/")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.search.active(), "a valid regex must activate the search")
	require.NotNil(t, m.search.re, "regex search must store the compiled regexp")
	require.Len(t, m.search.matches, 2,
		"/disk|timeout/ must match exactly the two lines containing those words")
}

// TestSlashSearch_RegexSyntax_CaseInsensitive asserts that /pattern/i matches
// regardless of the case of the content in the transcript.
func TestSlashSearch_RegexSyntax_CaseInsensitive(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	m.chat.Append(message.Message{
		ID:   "u1",
		Role: message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: "ZZMIXED line\nzzother line\nZZMIXED again\nZZMIXED lower",
		}},
	})

	// Smart-case literal search for an all-upper term would be case-sensitive,
	// but /ZZMIXED/i must match every casing.
	m.input.WriteString("/search /ZZMIXED/i")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.search.active())
	require.Len(t, m.search.matches, 3,
		"/ZZMIXED/i must match all three ZZMIXED lines regardless of case")
}

// TestSlashSearch_InvalidRegex_ShowsDialog asserts that an invalid /pattern/
// surfaces an error dialog rather than silently matching nothing.
func TestSlashSearch_InvalidRegex_ShowsDialog(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.chat.Append(message.Message{
		ID:      "u1",
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: "some content"}},
	})

	m.input.WriteString("/search /[invalid/")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.False(t, m.search.active(),
		"an invalid regex must not activate a search")
	require.True(t, m.dialogs.Contains("search"),
		"an invalid regex must open an error dialog")
	require.Contains(t, plainText(m.dialogs.Render(200)), "Invalid",
		"the error dialog must report that the pattern is invalid")
}

// TestHighlightTermRe asserts the regex highlighter wraps every match in the
// style and leaves non-matching text unchanged.
func TestHighlightTermRe(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	style := theme.Match

	// A pattern that matches two distinct substrings.
	re := regexp.MustCompile(`\d+`)
	got := highlightTermRe("foo 42 bar 7 baz", re, style)
	require.Equal(t,
		"foo "+style.Render("42")+" bar "+style.Render("7")+" baz", got,
		"every match must be wrapped in the style")

	// A line with no match is returned unchanged.
	require.Equal(t, "no digits here", highlightTermRe("no digits here", re, style),
		"a line with no match must be returned unchanged")

	// A case-insensitive pattern wraps both casings.
	rei := regexp.MustCompile(`(?i)go`)
	got = highlightTermRe("Go and go", rei, style)
	require.Equal(t, style.Render("Go")+" and "+style.Render("go"), got,
		"a case-insensitive pattern must wrap every casing")
}
