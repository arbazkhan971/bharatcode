package llm

import (
	"errors"
	"strings"
)

// contextOverflowPatterns is the set of case-insensitive substrings that mark a
// provider rejection as a context-window overflow rather than another class of
// 400. It is a package var so callers (and later provider additions) can append
// new phrasings without touching the matcher; every entry MUST already be lower
// case because IsContextOverflowString lower-cases the input once and compares
// against these as-is.
//
// The list spans the wordings the supported providers use for an over-budget
// prompt — OpenAI's "context length"/"maximum context"/"exceeds the context
// window", Anthropic's "prompt is too long: N tokens > M maximum", Gemini's
// "input token count (X) exceeds the maximum number of tokens", Groq's
// "exceeds model's context limit", plus additional phrasings from Mistral,
// Cohere, Together, Fireworks, Bedrock, Vertex, and other OpenAI-compatible
// providers. Each entry carries enough qualifier (a "context", "token", or
// length word) that it does not match unrelated limit messages such as rate
// limits; see contextOverflowExclusions for the explicit exclusion list.
var contextOverflowPatterns = []string{
	// ── General / OpenAI ──────────────────────────────────────────────────────
	"context length",
	"maximum context",
	"exceeds the context window",
	"context limit",
	"context window",
	// ── Anthropic ────────────────────────────────────────────────────────────
	"prompt is too long",
	// ── Gemini / Vertex ──────────────────────────────────────────────────────
	"input token count",
	"exceeds the maximum number of tokens",
	// ── OpenAI / generic token count ─────────────────────────────────────────
	"too many tokens",
	"maximum number of tokens",
	"tokens. however, your messages resulted in",
	// ── OpenAI / generic phrasings ───────────────────────────────────────────
	"exceeds the context",
	"exceeds model's context",
	"input is too long",
	"reduce the length",
	"request too large",
	// ── OpenAI error code surfaced as text ───────────────────────────────────
	"context_length_exceeded",
	// ── Cohere / generic ─────────────────────────────────────────────────────
	"string too long",
	// ── Mistral / generic ────────────────────────────────────────────────────
	"maximum token",
	// ── Together / Fireworks / Bedrock ────────────────────────────────────────
	"total number of tokens",
	"exceed the token limit",
	// ── Generic longer-form phrases ──────────────────────────────────────────
	"exceeds the model's context",
}

// contextOverflowExclusions is the set of case-insensitive substrings that
// disqualify an otherwise-matching message from being reported as an overflow.
// The list is intentionally small — it covers categories of errors that share
// vocabulary with overflow messages (e.g. "limit", "exceeded") but belong to a
// different failure class and must not trigger compaction. Each entry MUST
// already be lower case.
var contextOverflowExclusions = []string{
	"rate limit",
	"quota",
	"billing",
}

// IsContextOverflow reports whether err is, or wraps, a context-window overflow.
// It first honours the ErrContextLimit sentinel through errors.Is so an error
// already classified by the HTTP/stream paths is recognised regardless of its
// message, then falls back to scanning the error text for the known overflow
// phrasings so a raw, still-unclassified provider error is caught too. A nil
// error is never an overflow.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextLimit) {
		return true
	}
	return IsContextOverflowString(err.Error())
}

// IsContextOverflowString reports whether s carries one of the provider
// context-overflow markers in contextOverflowPatterns AND does not contain any
// of the exclusion phrases in contextOverflowExclusions. The match is a
// case-insensitive substring scan, so it tolerates the differing casing and
// surrounding detail providers wrap their messages in.
func IsContextOverflowString(s string) bool {
	lower := strings.ToLower(s)
	for _, ex := range contextOverflowExclusions {
		if strings.Contains(lower, ex) {
			return false
		}
	}
	for _, pat := range contextOverflowPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// IsSilentOverflow detects context-window issues on requests that completed
// without an HTTP error but exhibit symptoms of silent truncation or a
// length-bounded stop. Two independent heuristics are applied:
//
//  1. Input-exceeds-window: the provider reported more input/prompt tokens than
//     the model's context window allows. This can happen when a provider
//     accepts the request, truncates the prompt silently, and still returns a
//     response — the usage figures reveal the true state.
//
//  2. Length-stop with no output: the finish/stop reason is "length" (the
//     model was cut off by the output-token cap or context limit) AND the
//     provider reported zero output tokens, meaning the model had no room to
//     generate even a single token — a strong signal the prompt consumed the
//     entire window.
//
// Parameters:
//   - inputTokens:   the prompt/input token count reported in the response usage.
//   - outputTokens:  the completion/output token count reported in the response usage.
//   - stopReason:    the finish_reason / stop_reason string from the provider
//     (e.g. "length", "max_tokens", "stop", "end_turn").
//   - contextWindow: the maximum token capacity of the model (0 means unknown;
//     if unknown, the input-exceeds-window heuristic is skipped).
//
// The function accepts primitive types so it can be called from any provider
// path without importing additional structs.
func IsSilentOverflow(inputTokens, outputTokens int, stopReason string, contextWindow int) bool {
	// Heuristic 1: reported input tokens meet or exceed the model's declared
	// window. An input count equal to the window means the prompt consumed
	// every available token, leaving no room for output — treated as overflow.
	if contextWindow > 0 && inputTokens >= contextWindow {
		return true
	}

	// Heuristic 2: stopped due to length with zero output while input is at
	// or near the full context window. We treat "at/near" as >= 95 % of the
	// window to allow for minor accounting differences between providers.
	reason := strings.ToLower(strings.TrimSpace(stopReason))
	isLengthStop := reason == "length" || reason == "max_tokens"
	if isLengthStop && outputTokens == 0 {
		if contextWindow <= 0 {
			// Window unknown: zero-output length-stop is still suspicious.
			return true
		}
		threshold := (contextWindow * 95) / 100
		if inputTokens >= threshold {
			return true
		}
	}

	return false
}

// IsSilentOverflowFromUsage is a convenience wrapper around IsSilentOverflow
// that accepts a Usage value directly so callers that already hold the Usage
// struct from an EndEvent do not need to unpack it manually.
func IsSilentOverflowFromUsage(u Usage, stopReason string, contextWindow int) bool {
	return IsSilentOverflow(u.InputTokens, u.OutputTokens, stopReason, contextWindow)
}
