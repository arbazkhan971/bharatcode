package tui

import "strings"

// maxInputHistory bounds the number of submitted prompts retained for Up/Down
// recall. Older entries are dropped once the cap is reached.
const maxInputHistory = 100

// slashCommands is the known set of built-in slash commands offered by Tab
// completion. It is kept in sync with the commands handled in handleSlash and
// listed in slashHelp.
var slashCommands = []string{
	"/help",
	"/clear",
	"/model",
	"/agent",
	"/sessions",
	"/fork",
	"/diff",
	"/status",
	"/plan",
	"/approve",
	"/goal",
	"/permissions",
	"/budget",
	"/yolo",
	"/save",
	"/export",
	"/copy",
	"/compact",
	"/quit",
}

// inputState holds the in-session prompt history and the in-progress slash
// completion cycle. It lives on the model and is mutated only from the key
// handler on the UI goroutine, so it needs no synchronization.
type inputState struct {
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
	// Continue an active cycle only while the buffer still shows the match we
	// placed there; any edit ends the cycle and reseeds from the new buffer.
	if len(s.completionMatches) > 0 && current == s.completionMatches[s.completionIndex] {
		s.completionIndex = (s.completionIndex + 1) % len(s.completionMatches)
		return s.completionMatches[s.completionIndex], true
	}

	s.resetCompletion()
	if !strings.HasPrefix(current, "/") {
		return current, false
	}
	matches := matchSlash(current)
	if len(matches) == 0 {
		return current, false
	}
	s.completionMatches = matches
	s.completionIndex = 0
	return matches[0], true
}

// matchSlash returns the slash commands whose name begins with prefix, in the
// canonical order of slashCommands. An exact match still returns itself so the
// user sees Tab confirm a fully typed command.
func matchSlash(prefix string) []string {
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}
