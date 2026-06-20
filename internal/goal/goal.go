// Package goal contains the dependency-free logic for the autonomous goal
// engine: the prompts that drive a multi-turn run, the sentinel tokens the
// agent uses to signal completion or being blocked, and helpers for detecting
// and stripping those sentinels from the model's final message.
//
// This package intentionally imports only the standard library so it can be
// used as a pure-logic leaf by both headless and TUI callers.
package goal

import "strings"

// DefaultMaxIterations is the number of agentic turns RunToGoal will attempt
// before giving up when the caller does not specify a limit.
const DefaultMaxIterations = 12

// Sentinel tokens the agent appends to the END of its final turn message to
// signal status. They are matched case-sensitively as standalone tokens.
const (
	// DoneSentinel marks that the goal is fully achieved and verified.
	DoneSentinel = "GOAL_COMPLETE"
	// BlockedSentinel marks that the agent is genuinely blocked and needs
	// the user.
	BlockedSentinel = "GOAL_BLOCKED"
)

// KickoffPrompt wraps the user's goal with the instructions that put the agent
// into autonomous, multi-turn goal mode.
func KickoffPrompt(goalText string) string {
	var b strings.Builder
	b.WriteString("You are operating in autonomous goal mode. Work toward the goal below ")
	b.WriteString("across multiple turns, using your tools as needed, without waiting for further input.\n\n")
	b.WriteString("GOAL:\n")
	b.WriteString(goalText)
	b.WriteString("\n\nInstructions:\n")
	b.WriteString("- Keep working turn after turn until the goal is fully achieved and verified.\n")
	b.WriteString("- After each turn, honestly assess your progress and decide what to do next.\n")
	b.WriteString("- When the goal is fully achieved AND you have verified it, end your final message ")
	b.WriteString("with a line containing exactly:\n")
	b.WriteString(DoneSentinel)
	b.WriteString("\n")
	b.WriteString("- If you are genuinely blocked or need input from the user that you cannot obtain ")
	b.WriteString("yourself, end your message with a line containing ")
	b.WriteString(BlockedSentinel)
	b.WriteString(" followed by a short explanation of why.\n")
	b.WriteString("- Otherwise, do not emit either sentinel; just keep making progress.\n")
	return b.String()
}

// ContinuePrompt is a short reminder used on every turn after the first to keep
// the agent moving toward the goal and to re-state the sentinel contract.
func ContinuePrompt(goalText string) string {
	var b strings.Builder
	b.WriteString("Continue working toward the goal. Remember to end your final message with ")
	b.WriteString(DoneSentinel)
	b.WriteString(" when the goal is fully done and verified, or ")
	b.WriteString(BlockedSentinel)
	b.WriteString(" if you are genuinely stuck and need the user.\n\n")
	b.WriteString("GOAL:\n")
	b.WriteString(goalText)
	b.WriteString("\n")
	return b.String()
}

// IsComplete reports whether finalText signals goal completion. Per the prompt
// contract the agent ends its final message with a line containing the
// sentinel, so only the LAST non-empty line is inspected — this avoids a false
// positive when the model merely mentions the token mid-message (e.g. "I'll
// print GOAL_COMPLETE when done"). The token must sit on a left word boundary
// so embedded matches like "MEGAGOAL_COMPLETE" do not count, while trailing
// word characters are allowed so "GOAL_COMPLETED" still counts.
func IsComplete(finalText string) bool {
	return containsToken(lastNonEmptyLine(finalText), DoneSentinel)
}

// IsBlocked reports whether finalText signals that the agent is blocked. Like
// IsComplete it only inspects the last non-empty line.
func IsBlocked(finalText string) bool {
	return containsToken(lastNonEmptyLine(finalText), BlockedSentinel)
}

// lastNonEmptyLine returns the last line of text that is not blank, or "".
func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// Strip returns finalText with any trailing sentinel line removed and the
// result trimmed, suitable for display.
func Strip(finalText string) string {
	lines := strings.Split(finalText, "\n")
	for len(lines) > 0 {
		last := lines[len(lines)-1]
		if strings.TrimSpace(last) == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		if !containsToken(last, DoneSentinel) && !containsToken(last, BlockedSentinel) {
			break
		}
		cleaned := strings.ReplaceAll(last, DoneSentinel, "")
		cleaned = strings.ReplaceAll(cleaned, BlockedSentinel, "")
		if strings.TrimSpace(cleaned) == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		lines[len(lines)-1] = strings.TrimSpace(cleaned)
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// containsToken reports whether token appears in text starting at a left word
// boundary. token is assumed to be ASCII.
func containsToken(text, token string) bool {
	text = strings.TrimSpace(text)
	for idx := 0; ; {
		i := strings.Index(text[idx:], token)
		if i < 0 {
			return false
		}
		pos := idx + i
		if pos == 0 || !isWordByte(text[pos-1]) {
			return true
		}
		idx = pos + 1
	}
}

// isWordByte reports whether b is a word character ([A-Za-z0-9_]).
func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// Outcome describes how a goal run ended.
type Outcome int

const (
	// Achieved means the agent signaled GOAL_COMPLETE.
	Achieved Outcome = iota
	// Blocked means the agent signaled GOAL_BLOCKED.
	Blocked
	// MaxIterations means the iteration budget was exhausted.
	MaxIterations
	// Stalled means the agent repeated the same message (no progress).
	Stalled
	// Errored means a turn returned an error.
	Errored
	// Cancelled means the context was cancelled.
	Cancelled
)

// String returns a lowercase, stable identifier for the outcome.
func (o Outcome) String() string {
	switch o {
	case Achieved:
		return "achieved"
	case Blocked:
		return "blocked"
	case MaxIterations:
		return "max_iterations"
	case Stalled:
		return "stalled"
	case Errored:
		return "errored"
	case Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Result is the summary of a completed (or aborted) goal run.
type Result struct {
	Outcome     Outcome
	Iterations  int
	LastMessage string
}
