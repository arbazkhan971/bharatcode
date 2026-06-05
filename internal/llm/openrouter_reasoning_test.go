package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

func TestIsOpenRouter(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://openrouter.ai/api/v1", true},
		// Detection is case-insensitive so a differently-cased host still matches.
		{"https://OpenRouter.AI/api/v1", true},
		{"https://api.openai.com/v1", false},
		{"https://generativelanguage.googleapis.com/v1beta/openai/", false},
		{"", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, isOpenRouter(c.url), c.url)
	}
}

func TestOpenRouterReasoning(t *testing.T) {
	// A positive thinking budget wins over an effort and is sent as max_tokens.
	r := openRouterReasoning(Request{Thinking: &ThinkingConfig{BudgetTokens: 4096}, ReasoningEffort: "high"})
	require.NotNil(t, r)
	require.Equal(t, 4096, r.MaxTokens)
	require.Empty(t, r.Effort)

	// With no budget, a configured effort is sent as effort.
	r = openRouterReasoning(Request{ReasoningEffort: "medium"})
	require.NotNil(t, r)
	require.Equal(t, "medium", r.Effort)
	require.Zero(t, r.MaxTokens)

	// A non-positive budget falls through to the effort.
	r = openRouterReasoning(Request{Thinking: &ThinkingConfig{BudgetTokens: 0}, ReasoningEffort: "low"})
	require.NotNil(t, r)
	require.Equal(t, "low", r.Effort)
	require.Zero(t, r.MaxTokens)

	// Nothing configured (or only whitespace effort) leaves reasoning unset so the
	// model's own default applies rather than reasoning being force-enabled.
	require.Nil(t, openRouterReasoning(Request{}))
	require.Nil(t, openRouterReasoning(Request{ReasoningEffort: "   "}))
}

// openRouterWireBody streams a request through a provider whose configured
// base_url carries the "openrouter.ai" marker (here as a path segment so the
// substring detection fires while the request still reaches the test server) and
// returns the raw request body the provider sent.
func openRouterWireBody(t *testing.T, baseURLSuffix, modelID string, mutate func(*Request)) []byte {
	t.Helper()
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("openrouter", config.ProviderOpenAICompatible, server.URL+baseURLSuffix)
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	cfg.Providers[0].Models = []string{modelID}
	cfg.Models[0].ID = modelID
	cfg.Models[0].Provider = "openrouter"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openrouter")
	require.NoError(t, err)

	req := streamRequest()
	req.Model = modelID
	if mutate != nil {
		mutate(&req)
	}
	events, err := provider.Stream(context.Background(), req)
	require.NoError(t, err)
	_ = collectEvents(events)
	require.NotEmpty(t, rawBody, "server must have received the request body")
	return rawBody
}

// TestOpenRouterStreamSetsReasoningFromBudget proves a thinking budget configured
// against a non-OpenAI model behind OpenRouter is forwarded as the unified
// `reasoning` object, the only knob that enables extended thinking for OpenRouter's
// Anthropic/Gemini/Grok upstreams.
func TestOpenRouterStreamSetsReasoningFromBudget(t *testing.T) {
	body := openRouterWireBody(t, "/openrouter.ai/api/v1", "anthropic/claude-sonnet-4-5", func(req *Request) {
		req.Thinking = &ThinkingConfig{BudgetTokens: 2048}
	})
	require.Contains(t, string(body), `"reasoning":{"max_tokens":2048}`)
}

// TestOpenRouterStreamSetsReasoningFromEffort proves a configured effort is
// forwarded as reasoning.effort when no explicit budget is set.
func TestOpenRouterStreamSetsReasoningFromEffort(t *testing.T) {
	body := openRouterWireBody(t, "/openrouter.ai/api/v1", "google/gemini-2.5-pro", func(req *Request) {
		req.ReasoningEffort = "high"
	})
	require.Contains(t, string(body), `"reasoning":{"effort":"high"}`)
}

// TestOpenRouterStreamExcludesOpenAIReasoningModel proves an OpenAI o-series model
// reached via OpenRouter keeps its native reasoning_effort path and is not also
// sent the unified reasoning object, so the two controls never compete.
func TestOpenRouterStreamExcludesOpenAIReasoningModel(t *testing.T) {
	body := openRouterWireBody(t, "/openrouter.ai/api/v1", "openai/o3", func(req *Request) {
		req.ReasoningEffort = "high"
	})
	require.NotContains(t, string(body), `"reasoning":{`)
	require.Contains(t, string(body), `"reasoning_effort":"high"`)
}

// TestOpenRouterStreamOmitsReasoningWhenUnset proves reasoning stays off when no
// budget or effort is configured, leaving the model's own default in place.
func TestOpenRouterStreamOmitsReasoningWhenUnset(t *testing.T) {
	body := openRouterWireBody(t, "/openrouter.ai/api/v1", "anthropic/claude-sonnet-4-5", nil)
	require.NotContains(t, string(body), `"reasoning"`)
}

// TestNonOpenRouterStreamOmitsReasoning proves the unified reasoning object is
// OpenRouter-specific: a different openai_compatible backend never receives it,
// even with a thinking budget configured, since most would reject the field.
func TestNonOpenRouterStreamOmitsReasoning(t *testing.T) {
	body := openRouterWireBody(t, "/v1", "deepseek-chat", func(req *Request) {
		req.Thinking = &ThinkingConfig{BudgetTokens: 2048}
	})
	require.NotContains(t, string(body), `"reasoning"`)
}
