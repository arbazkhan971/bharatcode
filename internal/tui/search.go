package tui

import (
	"fmt"
	"regexp"
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
	re      *regexp.Regexp // non-nil when the query was a /pattern/ regex term
	matches []int
	current int
	// wrapped records whether the most recent next/previous-match jump rolled
	// past the end of the buffer back to the other side, so the status segment
	// can announce the wrap the way vim and less do ("search hit BOTTOM,
	// continuing at TOP"). It is set by searchNext/searchPrev on each step and is
	// false for a fresh search, so it always reflects the last navigation.
	wrapped bool
}

// active reports whether a search currently has matches to navigate.
func (s *searchState) active() bool {
	return len(s.matches) > 0
}

// statusSegment returns the status-bar segment describing search progress, e.g.
// "search 2/7". It is empty when no search is active, so the status bar is
// unchanged until the user starts navigating matches. The 1-based current index
// mirrors the dialog count ("Match 1 of N") the user first sees. When the last
// navigation wrapped past either end of the buffer the segment gains a
// "(wrapped)" note, so a Ctrl+/ that jumped from the final hit back to the first
// is visibly a wrap rather than a silent reset — the cue vim and less print when
// a search rolls over the end of the buffer.
func (s *searchState) statusSegment() string {
	if !s.active() {
		return ""
	}
	seg := fmt.Sprintf("search %d/%d", s.current+1, len(s.matches))
	if s.wrapped {
		seg += " (wrapped)"
	}
	return seg
}

// reset clears the search so the viewport is no longer pinned to a match.
func (s *searchState) reset() {
	s.term = ""
	s.re = nil
	s.matches = nil
	s.current = 0
	s.wrapped = false
}

// handleSearch runs the /search slash command. With an argument it searches the
// visible transcript for the term, positions the viewport at the first match,
// and reports the match count; subsequent /search of the same term advances to
// the next match (so repeated invocations cycle, mirroring an editor). With no
// argument it advances to the next match of the active term, or explains that a
// term is required when nothing is being searched. A term with no match clears
// any prior search and reports that nothing was found.
//
// When the argument is wrapped in /…/ or /…/i it is compiled as a regular
// expression, following the vim search convention. An 'i' flag after the
// closing slash makes the pattern case-insensitive. An invalid pattern is
// reported through a dialog instead of silently matching nothing.
func (m *model) handleSearch(text string) (tea.Model, tea.Cmd) {
	_, term := splitSlash(text)
	term = strings.TrimSpace(term)

	if term == "" {
		// A bare "/search" advances within an active search, like pressing the
		// next-match key, and otherwise prompts for a term.
		if m.search.active() {
			return m.searchNext(), nil
		}
		m.dialogs.Push(&dialog.Text{DialogID: "search", Title: "Search", Body: "Usage: /search <term>  or  /search /regex/[i]", Theme: m.theme})
		return m, nil
	}

	// Re-running the same term advances rather than re-anchoring to the first
	// match, so "/search foo" repeatedly walks the matches.
	if m.search.active() && strings.EqualFold(term, m.search.term) {
		return m.searchNext(), nil
	}

	// /pattern/ or /pattern/i triggers regex mode.
	re, err := parseRegexTerm(term)
	if err != nil {
		m.dialogs.Push(&dialog.Text{
			DialogID: "search",
			Title:    "Search",
			Body:     fmt.Sprintf("Invalid regex %q: %s", term, err),
			Theme:    m.theme,
		})
		return m, nil
	}
	if re != nil {
		return m.startSearchRe(term, re), nil
	}

	return m.startSearch(term), nil
}

// parseRegexTerm returns a compiled *regexp.Regexp when term follows the
// vim-style /pattern/ or /pattern/i syntax (starts with '/' and ends with '/'
// or '/i'). The trailing 'i' flag prepends (?i) to make the pattern
// case-insensitive. A term that does not match the /…/ envelope returns nil,
// nil so the caller falls back to literal search. An invalid pattern returns
// nil plus the compilation error so the caller can surface it to the user.
func parseRegexTerm(term string) (*regexp.Regexp, error) {
	if len(term) < 3 || term[0] != '/' {
		return nil, nil
	}
	inner := term[1:]
	flags := ""
	switch {
	case strings.HasSuffix(inner, "/i"):
		inner = inner[:len(inner)-2]
		flags = "(?i)"
	case strings.HasSuffix(inner, "/"):
		inner = inner[:len(inner)-1]
	default:
		return nil, nil // no closing slash — treat as literal
	}
	re, err := regexp.Compile(flags + inner)
	if err != nil {
		return nil, err
	}
	return re, nil
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

// startSearchRe begins a fresh regex search. displayTerm is the original user
// input (e.g. "/foo.*/i") stored in the state for same-term advance detection;
// re is the compiled pattern used for matching and highlighting.
func (m *model) startSearchRe(displayTerm string, re *regexp.Regexp) tea.Model {
	matches := chat.SearchLinesRe(m.renderedChatBody(), re)
	if len(matches) == 0 {
		m.search.reset()
		m.dialogs.Push(&dialog.Text{
			DialogID: "search",
			Title:    "Search",
			Body:     fmt.Sprintf("No matches for %q.", displayTerm),
			Theme:    m.theme,
		})
		return m
	}

	m.search = searchState{term: displayTerm, re: re, matches: matches, current: 0}
	m.scrollToMatch()
	m.dialogs.Push(&dialog.Text{
		DialogID: "search",
		Title:    "Search",
		Body:     fmt.Sprintf("Match 1 of %d for %q. /search again or Ctrl+/ for next; Ctrl+\\ for previous; Esc to clear.", len(matches), displayTerm),
		Theme:    m.theme,
	})
	return m
}

// searchNext advances to the next match, wrapping past the last back to the
// first, and repositions the viewport. The wrap is recorded so the status
// segment can announce it. It is a no-op when no search is active.
func (m *model) searchNext() tea.Model {
	if !m.search.active() {
		return m
	}
	next := m.search.current + 1
	m.search.wrapped = next >= len(m.search.matches)
	m.search.current = next % len(m.search.matches)
	m.scrollToMatch()
	return m
}

// searchPrev steps to the previous match, wrapping past the first back to the
// last, and repositions the viewport. The wrap is recorded so the status
// segment can announce it. It is a no-op when no search is active.
func (m *model) searchPrev() tea.Model {
	if !m.search.active() {
		return m
	}
	prev := m.search.current - 1
	m.search.wrapped = prev < 0
	m.search.current = (prev + len(m.search.matches)) % len(m.search.matches)
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
		style := m.theme.MatchOther
		if idx == current {
			style = m.theme.Match
		}
		// Transcript lines now carry styling (the role accent bar, colored
		// labels), so a line nearly always contains escape codes. Highlight the
		// term only within the line's PLAIN text spans — the runs between SGR
		// sequences — so the needle is emphasized without corrupting the existing
		// ANSI (which a naive whole-line replace over escaped text would do).
		if strings.ContainsRune(lines[idx], '\x1b') {
			lines[idx] = highlightInPlainSpans(lines[idx], m.search.term, m.search.re, style)
			continue
		}
		if m.search.re != nil {
			lines[idx] = highlightTermRe(lines[idx], m.search.re, style)
		} else {
			lines[idx] = highlightTerm(lines[idx], m.search.term, style)
		}
	}
	return strings.Join(lines, "\n")
}

// highlightInPlainSpans applies the term/regex highlight to the plain-text runs
// of a line that already contains ANSI escape sequences (e.g. an accent bar and
// colored label prefix). It splits the line into escape sequences and plain
// spans, highlights only the plain spans, and rejoins — so the needle is
// emphasized without slicing through or duplicating the surrounding SGR codes.
func highlightInPlainSpans(line, term string, re *regexp.Regexp, style lipgloss.Style) string {
	var b strings.Builder
	for len(line) > 0 {
		loc := ansiSeq.FindStringIndex(line)
		if loc == nil {
			b.WriteString(highlightSpan(line, term, re, style))
			break
		}
		if loc[0] > 0 {
			b.WriteString(highlightSpan(line[:loc[0]], term, re, style))
		}
		b.WriteString(line[loc[0]:loc[1]]) // the escape sequence, untouched
		line = line[loc[1]:]
	}
	return b.String()
}

// highlightSpan highlights a plain (escape-free) span using the regex when set,
// else the literal term.
func highlightSpan(span, term string, re *regexp.Regexp, style lipgloss.Style) string {
	if re != nil {
		return highlightTermRe(span, re, style)
	}
	return highlightTerm(span, term, style)
}

// ansiSeq matches a single ANSI SGR escape sequence.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// highlightTerm wraps every occurrence of term in line with style, leaving the
// rest untouched. Matching follows the same smart-case rule as the search that
// produced the matches (chat.SearchFold): a term with no upper-case letter folds
// case, one carrying an upper-case letter is matched exactly — so the emphasis
// marks precisely the occurrences the search navigates rather than lighting up
// stray case-folded text the search would not stop on. Matching is rune-wise so
// multi-byte content is never split mid-rune and a fold that changes a string's
// byte length never misaligns the offsets. An empty term, or a line with no
// occurrence, is returned unchanged so the common case allocates nothing beyond
// the scan.
func highlightTerm(line, term string, style lipgloss.Style) string {
	tr := []rune(term)
	if len(tr) == 0 {
		return line
	}
	fold := chat.SearchFold(term)
	lr := []rune(line)
	if !containsTerm(lr, tr, fold) {
		return line
	}
	var b strings.Builder
	for i := 0; i < len(lr); {
		if matchesTerm(lr[i:], tr, fold) {
			b.WriteString(style.Render(string(lr[i : i+len(tr)])))
			i += len(tr)
			continue
		}
		b.WriteRune(lr[i])
		i++
	}
	return b.String()
}

// highlightTermRe wraps every substring of line matched by re with style,
// leaving non-matching text unchanged. It is the regex counterpart to
// highlightTerm and is used when the active search was entered as /pattern/.
// An empty match (zero-length) is skipped to avoid an infinite loop.
func highlightTermRe(line string, re *regexp.Regexp, style lipgloss.Style) string {
	return re.ReplaceAllStringFunc(line, func(match string) string {
		if match == "" {
			return match
		}
		return style.Render(match)
	})
}

// matchesTerm reports whether the leading runes of s equal sub, comparing under
// Unicode case folding when fold is set and exactly otherwise.
func matchesTerm(s, sub []rune, fold bool) bool {
	if len(s) < len(sub) {
		return false
	}
	for k := range sub {
		a, b := s[k], sub[k]
		if fold {
			a, b = unicode.ToLower(a), unicode.ToLower(b)
		}
		if a != b {
			return false
		}
	}
	return true
}

// containsTerm reports whether sub occurs anywhere in s under the given case
// rule, so highlightTerm can skip the allocating rewrite when there is nothing
// to mark.
func containsTerm(s, sub []rune, fold bool) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if matchesTerm(s[i:], sub, fold) {
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
