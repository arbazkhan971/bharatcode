package llm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsContextOverflowStringMatchesProviderWordings asserts the string matcher
// recognises the over-budget phrasings each supported provider actually emits,
// so a raw error message is classified as an overflow before it is wrapped in a
// sentinel.
func TestIsContextOverflowStringMatchesProviderWordings(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{
			name: "openai context length",
			msg:  "This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens.",
		},
		{
			name: "openai exceeds context window",
			msg:  "Your input exceeds the context window of this model.",
		},
		{
			name: "anthropic prompt too long",
			msg:  "prompt is too long: 215432 tokens > 204798 maximum",
		},
		{
			name: "gemini input token count",
			msg:  "The input token count (1100000) exceeds the maximum number of tokens allowed (1048576).",
		},
		{
			name: "groq context limit",
			msg:  "Request too large for model llama-3.3-70b-versatile: token count (300000) exceeds model's context limit (131072)",
		},
		{
			name: "generic too many tokens",
			msg:  "request rejected: too many tokens in prompt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, IsContextOverflowString(tc.msg))
		})
	}
}

// TestIsContextOverflowStringCaseInsensitive asserts the scan ignores casing, so
// a provider that upper-cases its error wording is still classified.
func TestIsContextOverflowStringCaseInsensitive(t *testing.T) {
	require.True(t, IsContextOverflowString("PROMPT IS TOO LONG: 300000 tokens > 200000 MAXIMUM"))
	require.True(t, IsContextOverflowString("Maximum Context Length Exceeded"))
}

// TestIsContextOverflowStringNegatives asserts unrelated rejections — including a
// rate limit, which shares the "limit"/"exceeded" words but not the context
// qualifier — stay unclassified so they are not mistaken for a recoverable
// overflow.
func TestIsContextOverflowStringNegatives(t *testing.T) {
	negatives := []string{
		"rate limit exceeded",
		"messages: at least one message is required",
		"invalid request: unknown field",
		"429 Too Many Requests",
		"",
	}
	for _, msg := range negatives {
		require.False(t, IsContextOverflowString(msg), "unexpected overflow match for %q", msg)
	}
}

// TestIsContextOverflowHonoursSentinel asserts an error already tagged with the
// ErrContextLimit sentinel is recognised through errors.Is even when its message
// carries none of the textual markers.
func TestIsContextOverflowHonoursSentinel(t *testing.T) {
	require.True(t, IsContextOverflow(ErrContextLimit))
	wrapped := fmt.Errorf("provider returned 400: %w", ErrContextLimit)
	require.True(t, IsContextOverflow(wrapped))
}

// TestIsContextOverflowMatchesUnclassifiedError asserts a plain error whose text
// carries an overflow marker but no sentinel is still classified, covering the
// fallback path for errors that reach the caller unwrapped.
func TestIsContextOverflowMatchesUnclassifiedError(t *testing.T) {
	err := errors.New("This model's maximum context length is 8192 tokens")
	require.True(t, IsContextOverflow(err))
}

// TestIsContextOverflowNilAndUnrelated asserts a nil error and an unrelated error
// are both reported as non-overflows.
func TestIsContextOverflowNilAndUnrelated(t *testing.T) {
	require.False(t, IsContextOverflow(nil))
	require.False(t, IsContextOverflow(errors.New("connection reset by peer")))
	require.False(t, IsContextOverflow(ErrRateLimit))
}
