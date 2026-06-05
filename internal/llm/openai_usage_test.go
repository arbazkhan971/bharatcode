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
	require.Equal(t, 1000, got.InputTokens)
	require.Equal(t, 200, got.OutputTokens)
	require.Equal(t, 512, got.CacheReadTokens, "cached_tokens must map to CacheReadTokens")
	require.Equal(t, 0, got.CacheWriteTokens)
}

// TestOpenAIUsageFlatCacheFieldsWin asserts the DeepSeek-style flat cache
// fields take precedence over prompt_tokens_details when both are present, so a
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
	require.Equal(t, 500, got.CacheWriteTokens)
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
