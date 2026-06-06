package tui

import (
	"sort"
	"strings"
)

// maxInputHistory bounds the number of submitted prompts retained for Up/Down
// recall. Older entries are dropped once the cap is reached.
const maxInputHistory = 100

// slashCommands is the known set of built-in slash commands offered by Tab
// completion. It is kept in sync with the commands handled in handleSlash and
// listed in slashHelpLines.
var slashCommands = []string{
	"/help",
	"/keys",
	"/clear",
	"/model",
	"/agent",
	"/sessions",
	"/tab",
	"/tabs",
	"/fork",
	"/diff",
	"/status",
	"/plan",
	"/approve",
	"/goal",
	"/permissions",
	"/budget",
	"/theme",
	"/yolo",
	"/save",
	"/export",
	"/copy",
	"/search",
	"/compact",
	"/quit",
}

// inputState holds the in-session prompt history and the in-progress slash
// completion cycle. It lives on the model and is mutated only from the key
// handler on the UI goroutine, so it needs no synchronization.
type inputState struct {
	// dynamicCommands holds extra "/name" slash commands contributed at runtime
	// — the user's recipes and custom prompts — so Tab completion, the hint
	// dropdown, and the did-you-mean suggester surface them alongside the
	// built-ins, matching what /help already lists. Names that duplicate a
	// built-in are dropped by setDynamicCommands so completion never shows the
	// same command twice.
	dynamicCommands []string
	// dynamicDescriptions maps a dynamic "/name" command to its terse one-line
	// gloss (a recipe title or custom-prompt description), so the completion
	// menu can describe a settled recipe or prompt the way slashCommandDescriptions
	// already describes a built-in. Names are keyed with their leading slash to
	// match candidates(); a command with no description simply has no entry.
	dynamicDescriptions map[string]string
	// history is the bounded list of submitted prompts, oldest first.
	history []string
	// cursor indexes history during Up/Down recall. It equals len(history)
	// when not navigating (the live, editable buffer). Walking Up decrements
	// it toward 0; Down increments it back toward len(history).
	cursor int
	// draft preserves the live buffer when recall begins so Down past the
	// newest entry can restore what the user was typing.
	draft string

	// completionMatches is the set of slash commands matching the prefix that
	// started the current Tab cycle. It is empty when no cycle is active.
	completionMatches []string
	// completionIndex points at the match currently shown in the buffer. A
	// buffer that no longer equals completionMatches[completionIndex] means the
	// user edited it, which ends the cycle and reseeds on the next Tab.
	completionIndex int
}

// record appends a submitted prompt to history and resets recall and
// completion state. Blank prompts and exact consecutive duplicates are not
// recorded, matching shell behavior.
func (s *inputState) record(prompt string) {
	if prompt != "" && (len(s.history) == 0 || s.history[len(s.history)-1] != prompt) {
		s.history = append(s.history, prompt)
		if len(s.history) > maxInputHistory {
			s.history = s.history[len(s.history)-maxInputHistory:]
		}
	}
	s.cursor = len(s.history)
	s.draft = ""
	s.resetCompletion()
}

// resetRecall returns recall to the live buffer without changing history. It
// is called whenever the buffer is edited so the next Up starts from the most
// recent entry again.
func (s *inputState) resetRecall() {
	s.cursor = len(s.history)
	s.draft = ""
}

// resetCompletion ends any active Tab-completion cycle.
func (s *inputState) resetCompletion() {
	s.completionMatches = nil
	s.completionIndex = 0
}

// recallPrev walks one entry back in history. current is the live buffer; the
// returned string is the buffer's new contents and ok reports whether anything
// changed (false when history is empty or already at the oldest entry).
func (s *inputState) recallPrev(current string) (string, bool) {
	if len(s.history) == 0 {
		return current, false
	}
	if s.cursor == len(s.history) {
		// Beginning recall: stash the live buffer so Down can restore it.
		s.draft = current
	}
	if s.cursor == 0 {
		return current, false
	}
	s.cursor--
	return s.history[s.cursor], true
}

// recallNext walks one entry forward in history toward the live buffer. The
// returned string is the buffer's new contents and ok reports whether anything
// changed (false when not currently navigating).
func (s *inputState) recallNext(current string) (string, bool) {
	if s.cursor >= len(s.history) {
		return current, false
	}
	s.cursor++
	if s.cursor == len(s.history) {
		return s.draft, true
	}
	return s.history[s.cursor], true
}

// completeSlash advances slash-command completion for the buffer. It returns
// the buffer's new contents and ok reporting whether a completion applied
// (false when the buffer is not a slash prefix or nothing matches). The first
// Tab on a prefix shows the first match; subsequent Tabs cycle through the
// matches.
func (s *inputState) completeSlash(current string) (string, bool) {
	return s.stepSlash(current, +1)
}

// completeSlashPrev cycles slash-command completion backward, the reverse of
// completeSlash, so Shift+Tab can step back to a match the user overshot rather
// than cycling the whole list around to reach it again. With no active cycle it
// seeds one positioned on the last match, so the first Shift+Tab on a prefix
// lands on the final candidate.
func (s *inputState) completeSlashPrev(current string) (string, bool) {
	return s.stepSlash(current, -1)
}

// stepSlash moves the slash-command completion cycle by dir (+1 forward, -1
// backward), wrapping at either end. It is the shared core of completeSlash and
// completeSlashPrev. Continuing an active cycle only while the buffer still
// shows the match we placed there; any edit ends the cycle and reseeds from the
// new buffer. A fresh cycle starts on the first match when stepping forward or
// the last when stepping backward, so a single Shift+Tab reaches the end of the
// list directly.
func (s *inputState) stepSlash(current string, dir int) (string, bool) {
	if len(s.completionMatches) > 0 && current == s.completionMatches[s.completionIndex] {
		s.completionIndex = wrapIndex(s.completionIndex+dir, len(s.completionMatches))
		return s.completionMatches[s.completionIndex], true
	}

	s.resetCompletion()
	if !strings.HasPrefix(current, "/") {
		return current, false
	}
	matches := matchSlash(s.candidates(), current)
	if len(matches) == 0 {
		return current, false
	}
	s.completionMatches = matches
	s.completionIndex = seedIndex(dir, len(matches))
	return matches[s.completionIndex], true
}

// wrapIndex returns i reduced into the range [0, n) with negative values
// wrapping to the end, so a completion cycle steps off either end onto the
// other. n is always positive at the call sites (a non-empty match list).
func wrapIndex(i, n int) int {
	return ((i % n) + n) % n
}

// seedIndex returns the index a freshly-seeded completion cycle starts on for a
// step direction dir: the first match (0) when stepping forward, the last when
// stepping backward, so the very first Shift+Tab lands on the final candidate
// rather than the first.
func seedIndex(dir, n int) int {
	if dir < 0 {
		return n - 1
	}
	return 0
}

// setDynamicCommands records the runtime-contributed slash commands (recipes and
// custom prompts), each as a leading-slash name. Blanks, names without a leading
// slash, names colliding with a built-in (the built-in handler wins at runtime,
// so listing it twice would mislead), and duplicates are dropped; the surviving
// order is preserved so the caller controls how dynamic commands sort after the
// built-ins.
func (s *inputState) setDynamicCommands(names []string) {
	builtin := make(map[string]struct{}, len(slashCommands))
	for _, c := range slashCommands {
		builtin[c] = struct{}{}
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || !strings.HasPrefix(n, "/") {
			continue
		}
		if _, dup := builtin[n]; dup {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	s.dynamicCommands = out
}

// setDynamicDescriptions records one-line glosses for the runtime slash
// commands so the completion menu can describe a settled recipe or custom
// prompt the way it already describes a built-in. Keys are "/name" commands;
// blank descriptions and entries that do not survive setDynamicCommands are
// harmless since the menu only reads a description for a command it is already
// showing. A nil or empty map simply leaves dynamic commands undescribed.
func (s *inputState) setDynamicDescriptions(desc map[string]string) {
	s.dynamicDescriptions = desc
}

// candidates returns the full set of completion targets: the built-in slash
// commands followed by any dynamic ones (recipes, custom prompts). With no
// dynamic commands it returns the shared slashCommands slice unchanged so the
// common case allocates nothing.
func (s *inputState) candidates() []string {
	if len(s.dynamicCommands) == 0 {
		return slashCommands
	}
	out := make([]string, 0, len(slashCommands)+len(s.dynamicCommands))
	out = append(out, slashCommands...)
	out = append(out, s.dynamicCommands...)
	return out
}

// matchSlash returns the commands matching prefix, in the order of commands. A
// leading-prefix match is preferred: an exact match still returns itself so the
// user sees Tab confirm a fully typed command. Only when no command begins with
// the prefix does it fall back to a case-insensitive subsequence match on the
// command name, so a user who types the wrong start — "/exp" finds "/export" as
// a prefix, but "/port" does not — can still reach the command, matching the
// fuzzy command palettes of Claude Code and opencode. The fallback never fires
// while a prefix matches, so prefix completion and the existing Tab cycle are
// unchanged. commands is the candidate set (built-ins plus any dynamic recipes
// and custom prompts) so user-defined commands complete like the built-ins.
func matchSlash(commands []string, prefix string) []string {
	var matches []string
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) > 0 {
		return matches
	}

	token := strings.ToLower(strings.TrimPrefix(prefix, "/"))
	if !strings.HasPrefix(prefix, "/") || token == "" {
		return nil
	}
	for _, cmd := range commands {
		name := strings.ToLower(strings.TrimPrefix(cmd, "/"))
		if isSubsequence(token, name) {
			matches = append(matches, cmd)
		}
	}
	rankFuzzySlash(matches, token)
	return matches
}

// maxSuggestDistance bounds how far an unrecognized command name may sit from a
// built-in one and still be offered as a "did you mean" suggestion. Two edits
// covers the common typos — a transposition, a dropped or doubled letter, a
// wrong key — without proposing an unrelated command for a genuinely novel name.
const maxSuggestDistance = 2

// suggestSlash returns the slash command closest to an unrecognized command
// name (the bare token, without its leading slash), or "" when none is near
// enough to be a likely typo. Closeness is Levenshtein edit distance on the
// lower-cased name; the nearest command within maxSuggestDistance wins, ties
// broken by the order of commands so the result is deterministic. A suggestion
// is never offered when the edit distance is as long a leap as simply retyping
// either name (distance >= either length), so a one- or two-letter stub does not
// get "corrected" to an unrelated command. This backs the unknown-command
// dialog's hint, matching how git and the Claude Code / opencode palettes point
// a mistyped command at its closest real one. commands is the candidate set
// (built-ins plus any dynamic recipes and custom prompts) so a mistyped recipe
// name is corrected to the recipe.
func suggestSlash(commands []string, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	best := ""
	bestDist := maxSuggestDistance + 1
	nameLen := len([]rune(name))
	for _, cmd := range commands {
		target := strings.TrimPrefix(cmd, "/")
		d := levenshtein(name, target)
		// Require the edit distance to be strictly less than both names' lengths:
		// "correcting" a one- or two-letter stub into a short command (e.g. "a" →
		// "tab", "go" → "goal") takes as many edits as just typing it, so it is a
		// guess rather than a typo fix.
		if d > maxSuggestDistance || d >= len(target) || d >= nameLen {
			continue
		}
		if d < bestDist {
			best, bestDist = cmd, d
		}
	}
	return best
}

// levenshtein returns the edit distance between a and b: the minimum number of
// single-rune insertions, deletions, or substitutions to turn one into the
// other. It runs on runes so multi-byte names compare by character, and keeps a
// single row of state so the allocation is proportional to the shorter input.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	// Iterate columns over the longer string so the retained row is the shorter.
	if len(ar) < len(br) {
		ar, br = br, ar
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		diag := prev[0]
		prev[0] = i
		for j := 1; j <= len(br); j++ {
			cur := prev[j]
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			prev[j] = minInt3(prev[j]+1, prev[j-1]+1, diag+cost)
			diag = cur
		}
	}
	return prev[len(br)]
}

// minInt3 returns the smallest of three ints.
func minInt3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// rankFuzzySlash reorders the fuzzy-fallback matches in place so the most
// relevant command sorts first. A command whose name contains the token as a
// contiguous substring ranks ahead of one that only matches as a scattered
// subsequence; within a rank a tighter match span wins, then a shorter name,
// and the stable sort otherwise preserves the canonical slashCommands order.
// This mirrors the relevance scoring the @-file picker already applies, so
// "/et" surfaces "/budget" (which contains "et") ahead of "/agent" and
// "/export" (which only spell it out of order). token is expected lower-cased
// by the caller.
func rankFuzzySlash(matches []string, token string) {
	name := func(cmd string) string { return strings.ToLower(strings.TrimPrefix(cmd, "/")) }
	rank := func(cmd string) int {
		if strings.Contains(name(cmd), token) {
			return 0
		}
		return 1
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if ri, rj := rank(matches[i]), rank(matches[j]); ri != rj {
			return ri < rj
		}
		if si, sj := matchSpan(token, name(matches[i])), matchSpan(token, name(matches[j])); si != sj {
			return si < sj
		}
		return len(matches[i]) < len(matches[j])
	})
}
