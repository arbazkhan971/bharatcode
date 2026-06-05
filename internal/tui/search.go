package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/arbazkhan971/bharatcode/internal/tui/chat"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
)

// searchState holds the in-progress scrollback search. It is inert by default
// (zero value: empty term, no matches), so a model that has never searched
// behaves exactly as before. term is the most recent query; matches are the
// indices, into the rendered chat body's lines, of every line containing the
// term; current points at the active match within matches.
type searchState struct {
	term    string
	matches []int
	current int
}

// active reports whether a search currently has matches to navigate.
func (s *searchState) active() bool {
	return len(s.matches) > 0
}

// statusSegment returns the status-bar segment describing search progress, e.g.
// "search 2/7". It is empty when no search is active, so the status bar is
// unchanged until the user starts navigating matches. The 1-based current index
// mirrors the dialog count ("Match 1 of N") the user first sees.
func (s *searchState) statusSegment() string {
	if !s.active() {
		return ""
	}
	return fmt.Sprintf("search %d/%d", s.current+1, len(s.matches))
}

// reset clears the search so the viewport is no longer pinned to a match.
func (s *searchState) reset() {
	s.term = ""
	s.matches = nil
	s.current = 0
}

// handleSearch runs the /search slash command. With an argument it searches the
// visible transcript for the term, positions the viewport at the first match,
// and reports the match count; subsequent /search of the same term advances to
// the next match (so repeated invocations cycle, mirroring an editor). With no
// argument it advances to the next match of the active term, or explains that a
// term is required when nothing is being searched. A term with no match clears
// any prior search and reports that nothing was found.
func (m *model) handleSearch(text string) (tea.Model, tea.Cmd) {
	_, term := splitSlash(text)
	term = strings.TrimSpace(term)

	if term == "" {
		// A bare "/search" advances within an active search, like pressing the
		// next-match key, and otherwise prompts for a term.
		if m.search.active() {
			return m.searchNext(), nil
		}
		m.dialogs.Push(&dialog.Text{DialogID: "search", Title: "Search", Body: "Usage: /search <term>", Theme: m.theme})
		return m, nil
	}

	// Re-running the same term advances rather than re-anchoring to the first
	// match, so "/search foo" repeatedly walks the matches.
	if m.search.active() && strings.EqualFold(term, m.search.term) {
		return m.searchNext(), nil
	}

	return m.startSearch(term), nil
}

// startSearch begins a fresh search for term, computing the matching lines
// against the rendered chat body (the same line space the viewport scrolls) and
// anchoring the viewport at the first match. It surfaces a dialog reporting the
// match count, or that nothing matched.
func (m *model) startSearch(term string) tea.Model {
	matches := chat.SearchLines(m.renderedChatBody(), term)
	if len(matches) == 0 {
		m.search.reset()
		m.dialogs.Push(&dialog.Text{
			DialogID: "search",
			Title:    "Search",
			Body:     fmt.Sprintf("No matches for %q.", term),
			Theme:    m.theme,
		})
		return m
	}

	m.search = searchState{term: term, matches: matches, current: 0}
	m.scrollToMatch()
	m.dialogs.Push(&dialog.Text{
		DialogID: "search",
		Title:    "Search",
		Body:     fmt.Sprintf("Match 1 of %d for %q. /search again or Ctrl+/ for next; Ctrl+\\ for previous; Esc to clear.", len(matches), term),
		Theme:    m.theme,
	})
	return m
}

// searchNext advances to the next match, wrapping past the last back to the
// first, and repositions the viewport. It is a no-op when no search is active.
func (m *model) searchNext() tea.Model {
	if !m.search.active() {
		return m
	}
	m.search.current = (m.search.current + 1) % len(m.search.matches)
	m.scrollToMatch()
	return m
}

// searchPrev steps to the previous match, wrapping past the first back to the
// last, and repositions the viewport. It is a no-op when no search is active.
func (m *model) searchPrev() tea.Model {
	if !m.search.active() {
		return m
	}
	m.search.current = (m.search.current - 1 + len(m.search.matches)) % len(m.search.matches)
	m.scrollToMatch()
	return m
}

// scrollToMatch sets chatScroll so the current match line sits near the middle
// of the chat window rather than pinned to its last row, keeping the lines that
// follow the match on screen so the reader sees context on both sides of the hit
// (the way an editor centers a search result). clampChat bounds the value at
// render time, so a match near the end simply pins to the bottom and one near
// the top to the first line. It is a no-op when no search is active.
func (m *model) scrollToMatch() {
	if !m.search.active() {
		return
	}
	lines := strings.Split(m.renderedChatBody(), "\n")
	matchLine := m.search.matches[m.search.current]
	// chatScroll counts lines hidden below the window. Anchoring the match on the
	// last visible row scrolls up by (lastLineIndex - matchLine); reserving the
	// bottom half of the window for the lines after the match means scrolling up
	// by that much less, leaving the match centered. The reserve stays strictly
	// below the window height, so the match is always within the window before
	// clampChat trims the request.
	below := m.layout.chat.H / 2
	m.chatScroll = (len(lines) - 1) - matchLine - below
	if m.chatScroll < 0 {
		m.chatScroll = 0
	}
}

// highlightMatches returns body with the active search term emphasized on every
// match line, so the reader sees all occurrences at once — the way an editor
// marks every hit — with the current match drawn in the solid reverse-video
// Match style and the others underlined in MatchOther so the active one still
// stands out. body must be the rendered chat body whose line space
// search.matches indexes (the same space renderedChatBody produces), so each
// match index lands on its intended line. It is a no-op when no search is
// active.
//
// Markdown-rendered assistant lines carry ANSI styling; splicing a highlight
// span into them would corrupt the existing escapes, so only plain lines are
// highlighted. A styled line keeps its color without the inline emphasis — the
// match is still centered either way — so the feature degrades gracefully
// rather than risk garbling the transcript. A stale index past the end of the
// current body (e.g. after the transcript shrank) is skipped rather than
// panicking.
func (m *model) highlightMatches(body string) string {
	if !m.search.active() || m.search.term == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	current := m.search.matches[m.search.current]
	for _, idx := range m.search.matches {
		if idx < 0 || idx >= len(lines) {
			continue
		}
		if strings.ContainsRune(lines[idx], '\x1b') {
			continue
		}
		style := m.theme.MatchOther
		if idx == current {
			style = m.theme.Match
		}
		lines[idx] = highlightTerm(lines[idx], m.search.term, style)
	}
	return strings.Join(lines, "\n")
}

// highlightTerm wraps every case-insensitive occurrence of term in line with
// style, leaving the rest untouched. Matching is rune-wise (via unicode
// case-folding) so multi-byte content is never split mid-rune and a fold that
// changes a string's byte length never misaligns the offsets. An empty term, or
// a line with no occurrence, is returned unchanged so the common case allocates
// nothing beyond the scan.
func highlightTerm(line, term string, style lipgloss.Style) string {
	tr := []rune(term)
	if len(tr) == 0 {
		return line
	}
	lr := []rune(line)
	if !containsFold(lr, tr) {
		return line
	}
	var b strings.Builder
	for i := 0; i < len(lr); {
		if matchesFold(lr[i:], tr) {
			b.WriteString(style.Render(string(lr[i : i+len(tr)])))
			i += len(tr)
			continue
		}
		b.WriteRune(lr[i])
		i++
	}
	return b.String()
}

// matchesFold reports whether the leading runes of s equal sub under Unicode
// case folding.
func matchesFold(s, sub []rune) bool {
	if len(s) < len(sub) {
		return false
	}
	for k := range sub {
		if unicode.ToLower(s[k]) != unicode.ToLower(sub[k]) {
			return false
		}
	}
	return true
}

// containsFold reports whether sub occurs anywhere in s under case folding, so
// highlightTerm can skip the allocating rewrite when there is nothing to mark.
func containsFold(s, sub []rune) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if matchesFold(s[i:], sub) {
			return true
		}
	}
	return false
}

// renderedChatBody returns the chat transcript rendered at the current chat
// width: the exact text whose lines chatScroll indexes in clampChat. Searching
// and positioning against this shared line space keeps match navigation aligned
// with what the viewport actually shows. The width math mirrors renderMain so
// the two never disagree.
func (m *model) renderedChatBody() string {
	chatW := m.layout.chat.W
	if m.filetree.visible {
		chatW = max(1, chatW-filetreeWidth-1)
	}
	return m.chat.Render(max(1, chatW))
}
