package llm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// ─── textual pattern corpus ──────────────────────────────────────────────────

// TestIsContextOverflowStringMatchesProviderWordings asserts the string matcher
// recognises the over-budget phrasings each supported provider actually emits,
// so a raw error message is classified as an overflow before it is wrapped in a
// sentinel.
func TestIsContextOverflowStringMatchesProviderWordings(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		// ── OpenAI ────────────────────────────────────────────────────────────
		{
			name: "openai context length",
			msg:  "This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens.",
		},
		{
			name: "openai exceeds context window",
			msg:  "Your input exceeds the context window of this model.",
		},
		{
			name: "openai context_length_exceeded code",
			msg:  "context_length_exceeded: prompt is too long",
		},
		{
			name: "openai maximum context phrase",
			msg:  "maximum context length is 4096 tokens",
		},
		{
			name: "openai tokens however your messages",
			msg:  "128000 tokens. However, your messages resulted in 132000 tokens. Please reduce your message.",
		},
		// ── Anthropic ─────────────────────────────────────────────────────────
		{
			name: "anthropic prompt too long",
			msg:  "prompt is too long: 215432 tokens > 204798 maximum",
		},
		// ── Gemini / Vertex ───────────────────────────────────────────────────
		{
			name: "gemini input token count",
			msg:  "The input token count (1100000) exceeds the maximum number of tokens allowed (1048576).",
		},
		{
			name: "gemini maximum number of tokens",
			msg:  "Request exceeds the maximum number of tokens allowed by the model.",
		},
		// ── Groq ──────────────────────────────────────────────────────────────
		{
			name: "groq context limit",
			msg:  "Request too large for model llama-3.3-70b-versatile: token count (300000) exceeds model's context limit (131072)",
		},
		{
			name: "groq request too large",
			msg:  "request too large: 300000 tokens",
		},
		// ── Generic ───────────────────────────────────────────────────────────
		{
			name: "generic too many tokens",
			msg:  "request rejected: too many tokens in prompt",
		},
		{
			name: "generic context window",
			msg:  "The context window of the model has been exceeded.",
		},
		{
			name: "generic exceeds the context",
			msg:  "The input exceeds the context limit for this model.",
		},
		{
			name: "generic input is too long",
			msg:  "input is too long for this model, please shorten it",
		},
		{
			name: "generic reduce the length",
			msg:  "Please reduce the length of the messages or completion.",
		},
		// ── Cohere ────────────────────────────────────────────────────────────
		{
			name: "cohere string too long",
			msg:  "string too long: max 2048 characters",
		},
		// ── Mistral ───────────────────────────────────────────────────────────
		{
			name: "mistral maximum token",
			msg:  "Prompt exceeds maximum token limit for this model.",
		},
		// ── Bedrock / Together ────────────────────────────────────────────────
		{
			name: "total number of tokens",
			msg:  "The total number of tokens in the request exceeds the model maximum.",
		},
		{
			name: "exceed the token limit",
			msg:  "Your input tokens exceed the token limit for this model.",
		},
		// ── Generic model context phrase ──────────────────────────────────────
		{
			name: "exceeds the model context",
			msg:  "The request exceeds the model's context window size.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, IsContextOverflowString(tc.msg),
				"expected overflow match for: %q", tc.msg)
		})
	}
}

// TestIsContextOverflowStringCaseInsensitive asserts the scan ignores casing,
// so a provider that upper-cases its error wording is still classified.
func TestIsContextOverflowStringCaseInsensitive(t *testing.T) {
	require.True(t, IsContextOverflowString("PROMPT IS TOO LONG: 300000 tokens > 200000 MAXIMUM"))
	require.True(t, IsContextOverflowString("Maximum Context Length Exceeded"))
	require.True(t, IsContextOverflowString("CONTEXT_LENGTH_EXCEEDED"))
	require.True(t, IsContextOverflowString("INPUT IS TOO LONG FOR THIS MODEL"))
}

// TestIsContextOverflowStringNegatives asserts unrelated rejections — including
// rate-limit and quota messages that share words with overflow patterns — are
// NOT classified as overflow.
func TestIsContextOverflowStringNegatives(t *testing.T) {
	negatives := []string{
		// rate-limit / quota / billing — must never be mistaken for overflow
		"rate limit exceeded",
		"429 Too Many Requests",
		"You have exceeded your rate limit",
		"quota exceeded for this billing period",
		"billing issue: payment required",
		// other unrelated errors
		"messages: at least one message is required",
		"invalid request: unknown field",
		"connection reset by peer",
		"authentication failed",
		"",
	}
	for _, msg := range negatives {
		require.False(t, IsContextOverflowString(msg),
			"unexpected overflow match for %q", msg)
	}
}

// TestIsContextOverflowStringExclusionsTrumpPatterns asserts that when a
// message contains BOTH an overflow pattern AND an exclusion phrase (e.g. a
// rate-limit error that incidentally contains the word "context"), the
// exclusion wins and the message is NOT classified as overflow.
func TestIsContextOverflowStringExclusionsTrumpPatterns(t *testing.T) {
	// Contrived but possible: a message that triggers a pattern substring while
	// also containing an exclusion phrase.
	msg := "rate limit exceeded: too many tokens sent in context window per minute"
	require.False(t, IsContextOverflowString(msg),
		"rate-limit exclusion should suppress the pattern match")
}

// ─── sentinel / error wrapping ───────────────────────────────────────────────

// TestIsContextOverflowHonoursSentinel asserts an error already tagged with the
// ErrContextLimit sentinel is recognised through errors.Is even when its message
// carries none of the textual markers.
func TestIsContextOverflowHonoursSentinel(t *testing.T) {
	require.True(t, IsContextOverflow(ErrContextLimit))
	wrapped := fmt.Errorf("provider returned 400: %w", ErrContextLimit)
	require.True(t, IsContextOverflow(wrapped))
}

// TestIsContextOverflowMatchesUnclassifiedError asserts a plain error whose
// text carries an overflow marker but no sentinel is still classified, covering
// the fallback path for errors that reach the caller unwrapped.
func TestIsContextOverflowMatchesUnclassifiedError(t *testing.T) {
	err := errors.New("This model's maximum context length is 8192 tokens")
	require.True(t, IsContextOverflow(err))
}

// TestIsContextOverflowNilAndUnrelated asserts a nil error and an unrelated
// error are both reported as non-overflows.
func TestIsContextOverflowNilAndUnrelated(t *testing.T) {
	require.False(t, IsContextOverflow(nil))
	require.False(t, IsContextOverflow(errors.New("connection reset by peer")))
	require.False(t, IsContextOverflow(ErrRateLimit))
}

// ─── IsSilentOverflow ────────────────────────────────────────────────────────

// TestIsSilentOverflowInputExceedsWindow covers heuristic 1: the reported
// input token count exceeds the model's declared context window.
func TestIsSilentOverflowInputExceedsWindow(t *testing.T) {
	const window = 128_000

	// Clearly over window — should be detected.
	require.True(t, IsSilentOverflow(130_000, 50, "stop", window),
		"input > window must be a silent overflow")

	// Exactly at window boundary — still considered over.
	require.True(t, IsSilentOverflow(128_000, 200, "stop", window),
		"input == window must be a silent overflow")

	// Comfortably under window — not an overflow.
	require.False(t, IsSilentOverflow(64_000, 400, "stop", window),
		"input well under window must not be a silent overflow")

	// One token under window — not an overflow.
	require.False(t, IsSilentOverflow(127_999, 100, "stop", window),
		"input one token under window must not be a silent overflow")
}

// TestIsSilentOverflowLengthStopNoOutput covers heuristic 2: the stop reason
// is "length" (or "max_tokens") AND the model produced zero output tokens
// while the input is at or near the full context window.
func TestIsSilentOverflowLengthStopNoOutput(t *testing.T) {
	const window = 128_000
	threshold := (window * 95) / 100 // 121_600

	// At 95 % threshold, zero output, length stop.
	require.True(t, IsSilentOverflow(threshold, 0, "length", window),
		"length stop + zero output at threshold must be a silent overflow")

	// Over threshold, zero output, length stop.
	require.True(t, IsSilentOverflow(125_000, 0, "length", window),
		"length stop + zero output above threshold must be a silent overflow")

	// max_tokens alias also detected.
	require.True(t, IsSilentOverflow(125_000, 0, "max_tokens", window),
		"max_tokens stop + zero output must be detected")

	// Case-insensitive stop reason.
	require.True(t, IsSilentOverflow(125_000, 0, "LENGTH", window),
		"stop reason matching must be case-insensitive")

	// Same conditions but non-zero output — not a silent overflow via h2.
	require.False(t, IsSilentOverflow(125_000, 10, "length", window),
		"length stop with non-zero output must not trigger h2")

	// Normal stop — not an overflow.
	require.False(t, IsSilentOverflow(64_000, 200, "stop", window),
		"normal stop must not be a silent overflow")

	// Length stop + zero output but input well below threshold.
	require.False(t, IsSilentOverflow(50_000, 0, "length", window),
		"length stop + zero output well below threshold must not be detected")

	// Just below threshold.
	require.False(t, IsSilentOverflow(threshold-1, 0, "length", window),
		"one token below threshold must not trigger h2")
}

// TestIsSilentOverflowUnknownWindow asserts that when contextWindow is 0
// (unknown), heuristic 1 is skipped but heuristic 2 (length stop + zero
// output) still fires.
func TestIsSilentOverflowUnknownWindow(t *testing.T) {
	// H1 skipped — should not fire for any input count when window is unknown.
	require.False(t, IsSilentOverflow(9_999_999, 100, "stop", 0),
		"H1 must be skipped when contextWindow is 0")

	// H2 fires even without a window when output is zero and stop is length.
	require.True(t, IsSilentOverflow(50_000, 0, "length", 0),
		"H2 must fire on zero-output length-stop even without a known window")

	// H2 with non-zero output — should not fire.
	require.False(t, IsSilentOverflow(50_000, 5, "length", 0),
		"H2 must not fire when output > 0 even without a known window")
}

// TestIsSilentOverflowBothHeuristicsDisabled asserts a clean, successful
// response reports no silent overflow.
func TestIsSilentOverflowBothHeuristicsDisabled(t *testing.T) {
	require.False(t, IsSilentOverflow(10_000, 500, "stop", 128_000))
	require.False(t, IsSilentOverflow(10_000, 500, "end_turn", 128_000))
	require.False(t, IsSilentOverflow(0, 0, "stop", 128_000))
}

// TestIsSilentOverflowFromUsageWrapper asserts the Usage-accepting wrapper
// delegates correctly to IsSilentOverflow.
func TestIsSilentOverflowFromUsageWrapper(t *testing.T) {
	u := Usage{InputTokens: 130_000, OutputTokens: 50}
	require.True(t, IsSilentOverflowFromUsage(u, "stop", 128_000),
		"wrapper must detect input > window via H1")

	u2 := Usage{InputTokens: 125_000, OutputTokens: 0}
	require.True(t, IsSilentOverflowFromUsage(u2, "length", 128_000),
		"wrapper must detect length-stop + zero output via H2")

	u3 := Usage{InputTokens: 64_000, OutputTokens: 400}
	require.False(t, IsSilentOverflowFromUsage(u3, "stop", 128_000),
		"wrapper must not flag a healthy response")
}
