package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseProviderErrorClassifiesAnthropicPromptTooLong asserts that Anthropic's
// over-budget rejection — an invalid_request_error whose message reads "prompt is
// too long: N tokens > M maximum" rather than the OpenAI "context length" wording
// — is mapped to ErrContextLimit so the agent's compaction path can recover the
// turn instead of failing it as a generic 400.
func TestParseProviderErrorClassifiesAnthropicPromptTooLong(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 215432 tokens > 204798 maximum"}}`)
	err := parseProviderError(body)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrContextLimit)
}

// TestMentionsContextLimitMatchesAnthropicWording guards the 400-body fallback
// scan that classifyHTTPError uses when the error envelope does not parse,
// ensuring Anthropic's distinct phrasing is still recognized as a context
// overflow.
func TestMentionsContextLimitMatchesAnthropicWording(t *testing.T) {
	require.True(t, mentionsContextLimit("prompt is too long: 215432 tokens > 204798 maximum"))
	require.True(t, mentionsContextLimit("PROMPT IS TOO LONG"), "match must be case-insensitive")
	require.False(t, mentionsContextLimit("invalid request: unknown field"))
}

// TestClassifyAnthropicStreamErrorPromptTooLong asserts that an over-budget
// prompt surfaced mid-stream as a (terminal) invalid_request_error is tagged
// ErrContextLimit, mirroring the HTTP 400 path, while an unrelated
// invalid_request_error stays unclassified so it is not mistaken for a
// recoverable overflow.
func TestClassifyAnthropicStreamErrorPromptTooLong(t *testing.T) {
	overflow := classifyAnthropicStreamError("invalid_request_error", "prompt is too long: 300000 tokens > 200000 maximum")
	require.ErrorIs(t, overflow, ErrContextLimit)

	other := classifyAnthropicStreamError("invalid_request_error", "messages: at least one message is required")
	require.NotErrorIs(t, other, ErrContextLimit, "an unrelated invalid_request_error must not be classified as a context overflow")
}
