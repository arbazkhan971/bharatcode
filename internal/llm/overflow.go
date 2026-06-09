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
// "input token count (X) exceeds the maximum number of tokens", and Groq's
// "exceeds model's context limit" — plus the generic "too many tokens" form.
// Each marker carries enough qualifier (a "context" or "token" word) that it
// does not match unrelated limit messages such as rate limits.
var contextOverflowPatterns = []string{
	"context length",
	"maximum context",
	"exceeds the context window",
	"context limit",
	"prompt is too long",
	"too many tokens",
	"exceeds the maximum number of tokens",
	"input token count",
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
// context-overflow markers in contextOverflowPatterns. The match is a
// case-insensitive substring scan, so it tolerates the differing casing and
// surrounding detail providers wrap their messages in.
func IsContextOverflowString(s string) bool {
	s = strings.ToLower(s)
	for _, pat := range contextOverflowPatterns {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}
