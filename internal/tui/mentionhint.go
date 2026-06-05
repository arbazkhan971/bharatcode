package tui

import (
	"sort"
	"strings"
)

// maxMentionHints bounds how many file matches the @-mention picker surfaces and
// cycles through, so a broad token (or a bare "@") never floods the menu or the
// Tab cycle.
const maxMentionHints = 20

// activeMention reports the in-progress @-file token at the very end of buffer:
// the text after a trailing "@" that the user is still typing. It returns ok
// only when the "@" sits at a mention boundary (buffer start, whitespace, or an
// opening bracket — matching mentionPattern) and everything after it is valid
// path text with no whitespace, so a completed mention followed by a space, or a
// mid-token "@" like an email address, is not treated as active. The token may
// be empty (a lone trailing "@"), which offers the whole workspace listing.
func activeMention(buffer string) (token string, ok bool) {
	at := strings.LastIndex(buffer, "@")
	if at < 0 {
		return "", false
	}
	if at > 0 && !isMentionBoundary(rune(buffer[at-1])) {
		return "", false
	}
	token = buffer[at+1:]
	for _, r := range token {
		if !isMentionChar(r) {
			return "", false
		}
	}
	return token, true
}

// isMentionBoundary reports whether r may immediately precede a "@" that starts a
// file mention. It mirrors the leading character class of mentionPattern.
func isMentionBoundary(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '(', '[', '{':
		return true
	default:
		return false
	}
}

// isMentionChar reports whether r is part of a mention path token. It mirrors the
// path character class of mentionPattern.
func isMentionChar(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	case r == '.' || r == '_' || r == '/' || r == '-':
		return true
	default:
		return false
	}
}

// mentionMatches returns workspace-relative file paths matching the in-progress
// token, best-first and capped at maxMentionHints. An empty token returns the
// head of the listing so a bare "@" still reveals what is available. Matching is
// case-insensitive: a file ranks highest when its base name begins with the
// token, then when its full path contains the token, then on a looser
// subsequence match, with lexical order breaking ties (the listing is already
// sorted, so the stable sort preserves it).
func mentionMatches(token, root string) []string {
	files := listWorkspaceFiles(root)
	if token == "" {
		// A bare "@" has no token to score, so order by depth then length: a
		// top-level README or main.go surfaces ahead of a deeply-nested file
		// that merely sorts earlier lexically, matching how Claude Code reveals
		// the workspace for a bare "@".
		sort.SliceStable(files, func(i, j int) bool { return shallowerFirst(files[i], files[j]) })
		if len(files) > maxMentionHints {
			files = files[:maxMentionHints]
		}
		return files
	}

	lower := strings.ToLower(token)
	type scored struct {
		path  string
		score int
	}
	var matched []scored
	for _, f := range files {
		if s, ok := mentionScore(lower, strings.ToLower(f)); ok {
			matched = append(matched, scored{f, s})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score < matched[j].score
		}
		// Within a score band, prefer shallower paths, then shorter ones, so a
		// top-level file outranks a deeply-nested namesake. The stable sort then
		// leaves the original lexical order for remaining ties.
		return shallowerFirst(matched[i].path, matched[j].path)
	})
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.path)
		if len(out) >= maxMentionHints {
			break
		}
	}
	return out
}

// shallowerFirst reports whether path a should sort ahead of b purely on shape:
// fewer path segments first, then shorter overall. Callers apply it as a stable
// tie-break, so paths of equal depth and length keep their existing (lexical)
// order.
func shallowerFirst(a, b string) bool {
	if da, db := strings.Count(a, "/"), strings.Count(b, "/"); da != db {
		return da < db
	}
	return len(a) < len(b)
}

// mentionScore rates how well a lower-cased path matches a lower-cased token,
// reporting ok only on a match. Lower scores rank first: 0 when the base name is
// a prefix, 1 when the path contains the token, 2 on a subsequence match.
func mentionScore(token, path string) (int, bool) {
	base := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		base = path[i+1:]
	}
	if strings.HasPrefix(base, token) {
		return 0, true
	}
	if strings.Contains(path, token) {
		return 1, true
	}
	if isSubsequence(token, path) {
		return 2, true
	}
	return 0, false
}

// isSubsequence reports whether every rune of token appears in s in order (not
// necessarily contiguously). Both are expected pre-lowered by the caller.
func isSubsequence(token, s string) bool {
	if token == "" {
		return true
	}
	t := []rune(token)
	i := 0
	for _, r := range s {
		if r == t[i] {
			i++
			if i == len(t) {
				return true
			}
		}
	}
	return false
}

// completeMention advances @-file Tab completion for the buffer, returning the
// buffer's new contents and ok reporting whether a completion applied. It
// mirrors completeSlash: the first Tab on an active mention replaces the token
// with the best match, and subsequent Tabs cycle through the matches. The cycle
// continues only while the buffer still shows the match we placed there; any
// edit ends it and reseeds on the next Tab. It reuses the shared completion
// state, which is safe because slash and mention completion never run on the
// same buffer (one needs a leading "/", the other a trailing "@" token).
func (s *inputState) completeMention(current, root string) (string, bool) {
	if len(s.completionMatches) > 0 && current == s.completionMatches[s.completionIndex] {
		s.completionIndex = (s.completionIndex + 1) % len(s.completionMatches)
		return s.completionMatches[s.completionIndex], true
	}

	s.resetCompletion()
	token, ok := activeMention(current)
	if !ok {
		return current, false
	}
	files := mentionMatches(token, root)
	if len(files) == 0 {
		return current, false
	}
	prefix := current[:len(current)-len(token)] // text up to and including "@"
	matches := make([]string, len(files))
	for i, f := range files {
		matches[i] = prefix + f
	}
	s.completionMatches = matches
	s.completionIndex = 0
	return matches[0], true
}

// mentionHintFiles returns the file paths to surface in the @-mention completion
// menu for the current buffer, plus the index selected by an active Tab cycle
// (-1 when none). It mirrors slashHintCommands: during a cycle the buffer equals
// the selected match, so the full cycle is returned with the active entry marked
// to keep the menu stable as the user Tabs through it. The displayed names are
// bare paths (the "@" and any preceding text are stripped).
func mentionHintFiles(buffer, root string, st *inputState) (files []string, active int) {
	token, ok := activeMention(buffer)
	if !ok {
		return nil, -1
	}
	if len(st.completionMatches) > 0 && buffer == st.completionMatches[st.completionIndex] {
		prefix := buffer[:len(buffer)-len(token)]
		names := make([]string, len(st.completionMatches))
		for i, m := range st.completionMatches {
			names[i] = strings.TrimPrefix(m, prefix)
		}
		return names, st.completionIndex
	}
	files = mentionMatches(token, root)
	if len(files) == 0 {
		return nil, -1
	}
	return files, -1
}

// renderMentionHint formats the @-file completion menu for the input region,
// matching renderSlashHint's one-row, token-by-token truncation so the layout
// height is unchanged. It returns "" when there is nothing to show, leaving the
// default prompt untouched. The path selected by an active Tab cycle is accented
// and the rest are muted.
func (m *model) renderMentionHint(width int) string {
	files, active := mentionHintFiles(m.input.String(), m.workspaceRoot, &m.inputHistory)
	if len(files) == 0 || width <= 0 {
		return ""
	}

	const sep = "  "
	const indent = "  "

	var parts []string
	used := len([]rune(indent))
	truncated := false
	for i, f := range files {
		next := len([]rune(f))
		if i > 0 {
			next += len(sep)
		}
		if used+next > width {
			truncated = true
			break
		}
		used += next
		if i == active {
			parts = append(parts, m.theme.Accent.Render(f))
		} else {
			parts = append(parts, m.theme.Muted.Render(f))
		}
	}
	if len(parts) == 0 {
		return ""
	}

	line := indent + strings.Join(parts, sep)
	if truncated {
		line += m.theme.Muted.Render(" …")
	}
	return line
}
