package agent

import (
	"fmt"
	"unicode/utf8"
)

// defaultToolResultMaxBytes caps the byte length of a single tool result before
// it is appended to the conversation history. Oversized results (a giant file
// read via the view tool, verbose bash output) can otherwise blow the model's
// context window in a single turn. The cap is generous enough to keep ordinary
// results intact while bounding pathological ones. It is overridable per Loop
// via Config.ToolResultMaxBytes.
const defaultToolResultMaxBytes = 32 * 1024

// truncateMarker is the human- and model-readable notice spliced in place of the
// elided middle of an oversized tool result. The %d is the number of bytes
// removed.
const truncateMarker = "\n\n... [%d bytes truncated] ...\n\n"

// toolResultMaxBytes returns the effective byte cap for tool results: the
// per-Loop override when set to a positive value, otherwise the package default.
func (l *Loop) toolResultMaxBytes() int {
	if l.cfg.ToolResultMaxBytes > 0 {
		return l.cfg.ToolResultMaxBytes
	}
	return defaultToolResultMaxBytes
}

// truncateToolResult bounds content to at most max bytes, replacing the elided
// middle with a clear marker that reports how many bytes were removed. It keeps
// a head and a tail so both the beginning of the output (often the most
// informative) and its end (often where errors or summaries land) survive.
//
// Error results are never truncated: their essential message is usually short
// and must reach the model intact, so an over-cap error passes through unchanged.
// Content already within the cap, and a non-positive cap, also pass through
// unchanged.
//
// The head and tail are cut on UTF-8 rune boundaries so the truncated result is
// always valid UTF-8 and never splits a multi-byte rune.
func truncateToolResult(content string, max int, isError bool) string {
	if isError || max <= 0 || len(content) <= max {
		return content
	}

	// The marker's length varies only with the digit count of the dropped-byte
	// total, which depends in turn on how much head and tail are kept — a small
	// circular dependency. Reserve room using an upper bound on the marker (the
	// full content length is the largest the dropped count can ever be) so the
	// budget is computed once and the final string never exceeds the cap, then
	// format the marker with the true dropped count.
	markerBound := len(fmt.Sprintf(truncateMarker, len(content)))

	// When the marker alone would not fit the cap, fall back to a plain head
	// truncation with the marker appended. This still bounds growth to a small
	// constant beyond max while keeping the notice visible.
	if max-markerBound <= 0 {
		head := truncateAtRuneBoundary(content, max)
		dropped := len(content) - len(head)
		return head + fmt.Sprintf(truncateMarker, dropped)
	}

	budget := max - markerBound
	headLen := (budget + 1) / 2
	tailLen := budget - headLen

	head := truncateAtRuneBoundary(content, headLen)
	tail := truncateTailAtRuneBoundary(content, tailLen)

	dropped := len(content) - len(head) - len(tail)
	return head + fmt.Sprintf(truncateMarker, dropped) + tail
}

// truncateAtRuneBoundary returns the longest prefix of s that is at most n bytes
// and does not split a multi-byte UTF-8 rune.
func truncateAtRuneBoundary(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	// Back up to the start of the rune that straddles the cut, if any.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// truncateTailAtRuneBoundary returns the longest suffix of s that is at most n
// bytes and does not split a multi-byte UTF-8 rune.
func truncateTailAtRuneBoundary(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	start := len(s) - n
	// Advance to the start of the next whole rune so the suffix begins cleanly.
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
