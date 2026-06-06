package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpenAIUsageCachedTokensFromDetails asserts the Chat Completions usage
// parser surfaces prompt-cache hits reported under the OpenAI-standard
// prompt_tokens_details.cached_tokens field. Native OpenAI and spec-compliant
// relays (OpenRouter, Groq, Together) report cache reads only there, so
// dropping it would zero out cache accounting for the most common path.
func TestOpenAIUsageCachedTokensFromDetails(t *testing.T) {
	raw := `{
		"prompt_tokens": 1000,
		"completion_tokens": 200,
		"prompt_tokens_details": {"cached_tokens": 512}
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	// prompt_tokens includes the 512 cached tokens, which the ledger prices
	// additively at the cache rate, so they are subtracted back out of the
	// non-cached input count: 1000 - 512 = 488.
	require.Equal(t, 488, got.InputTokens)
	require.Equal(t, 200, got.OutputTokens)
	require.Equal(t, 512, got.CacheReadTokens, "cached_tokens must map to CacheReadTokens")
	require.Equal(t, 0, got.CacheWriteTokens)
}

// TestOpenAIUsageInputExcludesCachedTokens asserts the cached prompt tokens are
// not double-counted: prompt_tokens is the total prompt size including the
// cached portion, so toUsage subtracts the cache reads back out of InputTokens.
// The ledger prices InputTokens and CacheReadTokens additively, so leaving the
// full prompt_tokens in InputTokens would bill the cached tokens twice. This
// covers the DeepSeek flat-field shape, where prompt_cache_hit_tokens +
// prompt_cache_miss_tokens sum to prompt_tokens.
func TestOpenAIUsageInputExcludesCachedTokens(t *testing.T) {
	raw := `{
		"prompt_tokens": 800,
		"completion_tokens": 50,
		"prompt_cache_hit_tokens": 300,
		"prompt_cache_miss_tokens": 500
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 500, got.InputTokens, "input must exclude the 300 cached tokens")
	require.Equal(t, 300, got.CacheReadTokens)
}

// TestOpenAIUsageInputClampsAtZero asserts a malformed response whose cached
// count exceeds prompt_tokens does not produce a negative InputTokens.
func TestOpenAIUsageInputClampsAtZero(t *testing.T) {
	raw := `{
		"prompt_tokens": 100,
		"completion_tokens": 5,
		"prompt_tokens_details": {"cached_tokens": 250}
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 0, got.InputTokens)
	require.Equal(t, 250, got.CacheReadTokens)
}

// TestOpenAIUsageFlatCacheFieldsWin asserts the DeepSeek-style flat cache hit
// field takes precedence over prompt_tokens_details when both are present, so a
// provider that emits both shapes is not double-read inconsistently.
func TestOpenAIUsageFlatCacheFieldsWin(t *testing.T) {
	raw := `{
		"prompt_tokens": 800,
		"completion_tokens": 50,
		"prompt_cache_hit_tokens": 300,
		"prompt_cache_miss_tokens": 500,
		"prompt_tokens_details": {"cached_tokens": 999}
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 300, got.CacheReadTokens, "flat hit count must win over details")
}

// TestOpenAIUsageMissTokensAreNotCacheWrites asserts DeepSeek's
// prompt_cache_miss_tokens is treated as ordinary full-rate input, not as a
// cache write. The miss tokens already land in InputTokens (prompt_tokens minus
// the cache hits), so reporting them again under CacheWriteTokens would
// double-count them and imply a cache-creation charge DeepSeek never makes.
func TestOpenAIUsageMissTokensAreNotCacheWrites(t *testing.T) {
	raw := `{
		"prompt_tokens": 800,
		"completion_tokens": 50,
		"prompt_cache_hit_tokens": 300,
		"prompt_cache_miss_tokens": 500
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 500, got.InputTokens, "miss tokens are ordinary input")
	require.Equal(t, 300, got.CacheReadTokens)
	require.Equal(t, 0, got.CacheWriteTokens, "a cache miss is not a cache write")
}

// TestOpenAIUsageCacheCreationMapsToWrites asserts a genuine cache-creation
// count (Anthropic usage forwarded through the OpenAI shape) still maps to
// CacheWriteTokens. This is the only source that should populate it.
func TestOpenAIUsageCacheCreationMapsToWrites(t *testing.T) {
	raw := `{
		"prompt_tokens": 1000,
		"completion_tokens": 80,
		"cache_read_input_tokens": 200,
		"cache_creation_input_tokens": 150
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 800, got.InputTokens)
	require.Equal(t, 200, got.CacheReadTokens)
	require.Equal(t, 150, got.CacheWriteTokens)
}

// TestOpenAIUsageNoCacheDetails asserts a usage object with neither flat fields
// nor details reports zero cache tokens (the no-cache baseline).
func TestOpenAIUsageNoCacheDetails(t *testing.T) {
	raw := `{"prompt_tokens": 11, "completion_tokens": 4}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 11, got.InputTokens)
	require.Equal(t, 4, got.OutputTokens)
	require.Equal(t, 0, got.CacheReadTokens)
	require.Equal(t, 0, got.CacheWriteTokens)
}

// TestOpenAIUsageReasoningTokensSurfaced asserts that reasoning tokens from an
// OpenAI o-series (or gpt-5) response are mapped into ReasoningTokens without
// double-counting: they are already included in CompletionTokens (OutputTokens),
// so the total billing is unchanged and ReasoningTokens is just a breakdown.
func TestOpenAIUsageReasoningTokensSurfaced(t *testing.T) {
	raw := `{
		"prompt_tokens": 50,
		"completion_tokens": 200,
		"completion_tokens_details": {"reasoning_tokens": 150}
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 50, got.InputTokens)
	// OutputTokens must equal CompletionTokens (reasoning tokens are a subset).
	require.Equal(t, 200, got.OutputTokens)
	require.Equal(t, 150, got.ReasoningTokens, "reasoning tokens must be surfaced as a breakdown")
}

// TestOpenAIUsageReasoningTokensAbsentOnChatModel asserts that a chat-model
// response (no completion_tokens_details) leaves ReasoningTokens at zero, not
// a spurious non-zero from a previous test or uninitialized value.
func TestOpenAIUsageReasoningTokensAbsentOnChatModel(t *testing.T) {
	raw := `{"prompt_tokens": 10, "completion_tokens": 5}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 0, got.ReasoningTokens, "no completion_tokens_details means zero reasoning tokens")
}

// TestOpenAIUsageReasoningTokensWithCacheAndReasoning asserts reasoning and
// cache fields are resolved independently: a response with both cache hits and
// reasoning tokens surfaces all four token buckets correctly.
func TestOpenAIUsageReasoningTokensWithCacheAndReasoning(t *testing.T) {
	raw := `{
		"prompt_tokens": 1000,
		"completion_tokens": 300,
		"prompt_tokens_details": {"cached_tokens": 400},
		"completion_tokens_details": {"reasoning_tokens": 200}
	}`

	var u openAIUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))

	got := u.toUsage()
	require.Equal(t, 600, got.InputTokens, "prompt minus cached")
	require.Equal(t, 300, got.OutputTokens, "completion unchanged")
	require.Equal(t, 400, got.CacheReadTokens)
	require.Equal(t, 200, got.ReasoningTokens)
}
