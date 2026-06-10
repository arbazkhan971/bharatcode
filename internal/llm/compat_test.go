package llm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// ptr returns a pointer to v — a convenience for test literals.
func ptr[T any](v T) *T { return &v }

// TestCompatContextWindowOverride verifies that a model with a
// Compat.ContextWindow set has its context window resolved to the override
// value, while a model with no Compat block keeps the heuristic default.
func TestCompatContextWindowOverride(t *testing.T) {
	// deepseek-chat: heuristic resolves to 131072 via the "deepseek" rule.
	// With a Compat override of 65536 it should use 65536.
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:    "deepseek",
			Type:    config.ProviderOpenAICompatible,
			BaseURL: "https://api.deepseek.com/v1",
			Models:  []string{"deepseek-chat"},
		}},
		Models: []config.Model{
			{
				ID:       "deepseek-chat",
				Provider: "deepseek",
				// Compat.ContextWindow overrides the heuristic.
				Compat: &config.ModelCompat{
					ContextWindow: ptr(65536),
				},
			},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	require.Len(t, models, 1)
	require.Equal(t, 65536, models[0].ContextWindow,
		"Compat.ContextWindow must override the heuristic")
	// The Compat block must be threaded through to the llm.Model.
	require.NotNil(t, models[0].Compat)
	require.NotNil(t, models[0].Compat.ContextWindow)
	require.Equal(t, 65536, *models[0].Compat.ContextWindow)
}

// TestCompatNoBlockUsesHeuristic verifies that a model with no Compat block
// uses the inferContextWindow heuristic unchanged (purely additive check).
func TestCompatNoBlockUsesHeuristic(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:    "deepseek",
			Type:    config.ProviderOpenAICompatible,
			BaseURL: "https://api.deepseek.com/v1",
			Models:  []string{"deepseek-chat"},
		}},
		Models: []config.Model{
			{
				ID:       "deepseek-chat",
				Provider: "deepseek",
				// No Compat block — heuristic must apply.
			},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	require.Len(t, models, 1)
	require.Equal(t, inferContextWindow("deepseek-chat"), models[0].ContextWindow,
		"no Compat block must leave inferContextWindow as the sole source")
	require.Nil(t, models[0].Compat,
		"no Compat block must leave Compat nil on the llm.Model")
}

// TestCompatThinkingFormatHonored verifies that resolveThinkingFormat returns
// the model's explicit Compat.ThinkingFormat when set, overriding any
// URL-based auto-detection.
func TestCompatThinkingFormatHonored(t *testing.T) {
	// openrouter.ai URL would normally auto-detect ThinkingFormatOpenRouter.
	// The model's explicit Compat.ThinkingFormat = "deepseek" must win.
	models := []Model{
		{
			ID: "some-model",
			Compat: &config.ModelCompat{
				ThinkingFormat: config.ThinkingFormatDeepSeek,
			},
		},
	}
	got := resolveThinkingFormat(models, "some-model", "https://openrouter.ai/api/v1")
	require.Equal(t, config.ThinkingFormatDeepSeek, got,
		"explicit Compat.ThinkingFormat must override URL auto-detection")
}

// TestCompatThinkingFormatAutoDetect verifies that the URL-based auto-detection
// fires when the model has no Compat block.
func TestCompatThinkingFormatAutoDetect(t *testing.T) {
	cases := []struct {
		baseURL string
		want    config.ThinkingFormat
	}{
		{"https://openrouter.ai/api/v1", config.ThinkingFormatOpenRouter},
		{"https://api.deepseek.com/v1", config.ThinkingFormatDeepSeek},
		{"https://zai.ai/v1", config.ThinkingFormatDeepSeek},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", config.ThinkingFormatQwen},
		{"https://api.openai.com/v1", config.ThinkingFormatDefault},
		{"https://api.groq.com/openai/v1", config.ThinkingFormatDefault},
	}
	// No compat block on the model.
	models := []Model{{ID: "some-model"}}
	for _, tc := range cases {
		t.Run(tc.baseURL, func(t *testing.T) {
			got := resolveThinkingFormat(models, "some-model", tc.baseURL)
			require.Equal(t, tc.want, got,
				"URL %q should auto-detect thinking format %q", tc.baseURL, tc.want)
		})
	}
}

// TestCompatNoneThinkingFormatSuppresses verifies that ThinkingFormatNone
// resolves correctly even on a URL that would otherwise auto-detect OpenRouter.
func TestCompatNoneThinkingFormatSuppresses(t *testing.T) {
	models := []Model{
		{
			ID: "some-model",
			Compat: &config.ModelCompat{
				ThinkingFormat: config.ThinkingFormatNone,
			},
		},
	}
	got := resolveThinkingFormat(models, "some-model", "https://openrouter.ai/api/v1")
	require.Equal(t, config.ThinkingFormatNone, got,
		"ThinkingFormatNone must suppress even the OpenRouter URL auto-detection")
}

// TestCompatReasoningOverrideTrue verifies that Compat.Reasoning=true forces
// the reasoning-model branch regardless of the id heuristic.
func TestCompatReasoningOverrideTrue(t *testing.T) {
	models := []Model{
		{
			ID: "my-quirky-reasoner",
			Compat: &config.ModelCompat{
				Reasoning: ptr(true),
			},
		},
	}
	// The id "my-quirky-reasoner" would NOT be classified as a reasoning model
	// by the heuristic (it has no o-series or gpt-5 prefix).
	require.False(t, isReasoningModel("my-quirky-reasoner"),
		"sanity: heuristic must not classify this id as reasoning")
	require.True(t, isReasoningModelForRequest(models, "my-quirky-reasoner"),
		"Compat.Reasoning=true must override the heuristic")
}

// TestCompatReasoningOverrideFalse verifies that Compat.Reasoning=false forces
// the chat-model branch even for an id the heuristic would classify as reasoning.
func TestCompatReasoningOverrideFalse(t *testing.T) {
	models := []Model{
		{
			ID: "o3-mini",
			Compat: &config.ModelCompat{
				Reasoning: ptr(false),
			},
		},
	}
	// The id "o3-mini" IS classified as a reasoning model by the heuristic.
	require.True(t, isReasoningModel("o3-mini"),
		"sanity: heuristic must classify o3-mini as reasoning")
	require.False(t, isReasoningModelForRequest(models, "o3-mini"),
		"Compat.Reasoning=false must override the heuristic")
}

// TestCompatReasoningNilFallsThrough verifies that Compat.Reasoning=nil
// defers to the id heuristic (no behavior change for existing models).
func TestCompatReasoningNilFallsThrough(t *testing.T) {
	models := []Model{
		{
			ID:     "o3-mini",
			Compat: &config.ModelCompat{}, // Compat block present but Reasoning is nil
		},
	}
	require.True(t, isReasoningModelForRequest(models, "o3-mini"),
		"nil Compat.Reasoning must fall through to the id heuristic")
}

// TestCompatSupportsImagesOverride verifies that Compat.SupportsImages
// overrides the catalog flag.
func TestCompatSupportsImagesOverride(t *testing.T) {
	// A model whose catalog entry says SupportsImages: false, but Compat
	// overrides to true.
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:    "myprovider",
			Type:    config.ProviderOpenAICompatible,
			BaseURL: "https://example.invalid/v1",
			Models:  []string{"my-vision-model"},
		}},
		Models: []config.Model{
			{
				ID:             "my-vision-model",
				Provider:       "myprovider",
				SupportsImages: false, // catalog says no images
				Compat: &config.ModelCompat{
					SupportsImages: ptr(true), // compat says yes
				},
			},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	require.Len(t, models, 1)
	require.True(t, models[0].SupportsImages,
		"Compat.SupportsImages=true must override the catalog SupportsImages=false")
}

// TestCompatStrictToolsRequest verifies that StrictTools: true adds strict:true
// to each tool definition in the built request body.
func TestCompatStrictToolsRequest(t *testing.T) {
	compat := &config.ModelCompat{StrictTools: true}
	req := Request{
		Model: "some-model",
		Tools: []Tool{
			{Name: "my_tool", Description: "does something"},
		},
	}
	body, err := buildOpenAIRequestCompat(req, imageStyleOpenAI, compat, nil)
	require.NoError(t, err)
	require.Len(t, body.Tools, 1)
	require.NotNil(t, body.Tools[0].Function.Strict,
		"StrictTools must set Strict on every tool function")
	require.True(t, *body.Tools[0].Function.Strict,
		"strict must be true")
}

// TestCompatStrictToolsNotSetByDefault verifies that without StrictTools the
// Strict field is nil (omitted), preserving baseline OpenAI behavior.
func TestCompatStrictToolsNotSetByDefault(t *testing.T) {
	req := Request{
		Model: "gpt-4o",
		Tools: []Tool{
			{Name: "my_tool"},
		},
	}
	// No compat block.
	body, err := buildOpenAIRequestCompat(req, imageStyleOpenAI, nil, nil)
	require.NoError(t, err)
	require.Len(t, body.Tools, 1)
	require.Nil(t, body.Tools[0].Function.Strict,
		"without StrictTools the Strict field must be nil (omitted)")
}

// TestCompatMaxTokensOverride verifies that Compat.MaxTokens replaces the
// request's MaxTokens in the built request body.
func TestCompatMaxTokensOverride(t *testing.T) {
	compat := &config.ModelCompat{
		MaxTokens: ptr(2048),
	}
	req := Request{
		Model:     "gpt-4o",
		MaxTokens: 8192, // caller-requested value
	}
	body, err := buildOpenAIRequestCompat(req, imageStyleOpenAI, compat, nil)
	require.NoError(t, err)
	// gpt-4o is not a reasoning model, so MaxTokens lands in body.MaxTokens.
	require.Equal(t, 2048, body.MaxTokens,
		"Compat.MaxTokens must override the request MaxTokens")
}

// TestCompatNoBlockMaxTokensPreserved verifies that without a Compat block the
// request MaxTokens passes through unchanged.
func TestCompatNoBlockMaxTokensPreserved(t *testing.T) {
	req := Request{
		Model:     "gpt-4o",
		MaxTokens: 8192,
	}
	body, err := buildOpenAIRequestCompat(req, imageStyleOpenAI, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 8192, body.MaxTokens,
		"without Compat block the original request MaxTokens must pass through")
}

// TestCompatToolResultQuirkUserContent verifies that ToolResultQuirkUserContent
// converts a ToolResultBlock into a user-role message instead of a tool message.
func TestCompatToolResultQuirkUserContent(t *testing.T) {
	result := message.ToolResultBlock{
		ToolUseID: "call_123",
		Content:   "42",
	}
	msgs := toolResultMessages(result, config.ToolResultQuirkUserContent)
	require.Len(t, msgs, 1)
	require.Equal(t, "user", msgs[0].Role,
		"ToolResultQuirkUserContent must produce a user-role message")
	require.Contains(t, msgs[0].Content.(string), "call_123",
		"the tool call id must appear in the user message")
	require.Contains(t, msgs[0].Content.(string), "42",
		"the tool result content must appear in the user message")
}

// TestCompatToolResultQuirkNonePreservesStandard verifies that the zero-value
// quirk (ToolResultQuirkNone) emits the standard tool-role message format.
func TestCompatToolResultQuirkNonePreservesStandard(t *testing.T) {
	result := message.ToolResultBlock{
		ToolUseID: "call_456",
		Content:   "hello",
	}
	msgs := toolResultMessages(result, config.ToolResultQuirkNone)
	require.Len(t, msgs, 1)
	require.Equal(t, "tool", msgs[0].Role,
		"ToolResultQuirkNone must produce a standard tool-role message")
	require.Equal(t, "call_456", msgs[0].ToolCallID,
		"tool_call_id must be set on the standard tool message")
}

// TestCompatRegistryRoundTrip is the end-to-end test called out in the feature
// spec: a Model with a Compat block overriding ContextWindow and ThinkingFormat
// is honored; a Model with no Compat block uses the heuristic defaults unchanged.
func TestCompatRegistryRoundTrip(t *testing.T) {
	customWindow := 65536
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:    "quirky",
				Type:    config.ProviderOpenAICompatible,
				BaseURL: "https://api.deepseek.com/v1",
				Models:  []string{"deepseek-r1", "gpt-4o"},
			},
		},
		Models: []config.Model{
			{
				ID:       "deepseek-r1",
				Provider: "quirky",
				// Compat overrides context window and pins the thinking format.
				Compat: &config.ModelCompat{
					ContextWindow:  &customWindow,
					ThinkingFormat: config.ThinkingFormatDeepSeek,
				},
			},
			{
				ID:       "gpt-4o",
				Provider: "quirky",
				// No Compat block: must use heuristic defaults unchanged.
			},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	byID := make(map[string]Model)
	for _, m := range reg.ListModels() {
		byID[m.ID] = m
	}

	// --- Model WITH Compat block ---
	r1 := byID["deepseek-r1"]
	require.Equal(t, customWindow, r1.ContextWindow,
		"deepseek-r1: Compat.ContextWindow must override the heuristic")
	require.NotNil(t, r1.Compat, "deepseek-r1: Compat must be non-nil on the llm.Model")
	require.Equal(t, config.ThinkingFormatDeepSeek, r1.Compat.ThinkingFormat,
		"deepseek-r1: ThinkingFormat must be threaded through")

	// resolveThinkingFormat must honor the explicit compat even though the
	// baseURL (deepseek.com) would produce the same result by auto-detection.
	// Use a different baseURL to prove the explicit flag wins over auto-detection.
	allModels := reg.ListModels()
	gotFmt := resolveThinkingFormat(allModels, "deepseek-r1", "https://api.openai.com/v1")
	require.Equal(t, config.ThinkingFormatDeepSeek, gotFmt,
		"resolveThinkingFormat must return the explicit Compat.ThinkingFormat even against an OpenAI base URL")

	// --- Model WITHOUT Compat block ---
	gpt4o := byID["gpt-4o"]
	heuristicWindow := inferContextWindow("gpt-4o")
	require.Equal(t, heuristicWindow, gpt4o.ContextWindow,
		"gpt-4o: no Compat block must leave the heuristic context window unchanged")
	require.Nil(t, gpt4o.Compat, "gpt-4o: Compat must be nil when no compat block is configured")

	gotFmtNoCompat := resolveThinkingFormat(allModels, "gpt-4o", "https://api.openai.com/v1")
	require.Equal(t, config.ThinkingFormatDefault, gotFmtNoCompat,
		"gpt-4o on OpenAI base URL must resolve to ThinkingFormatDefault (no change)")
}
