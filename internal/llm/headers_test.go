package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// recordingTransport captures the headers of the request it sees and returns a
// canned 200 so RoundTrip exercises the injection path without a real server.
type recordingTransport struct {
	seen http.Header
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.seen = req.Header.Clone()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func TestHeaderTransportInjectsAndPreserves(t *testing.T) {
	rec := &recordingTransport{}
	tr := &headerTransport{
		base: rec,
		headers: map[string]string{
			"HTTP-Referer":  "https://bharatcode.dev",
			"X-Title":       "BharatCode",
			"Authorization": "Bearer custom-should-not-override",
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.test", http.NoBody)
	require.NoError(t, err)
	// A header the provider already set must win over the custom one.
	req.Header.Set("Authorization", "Bearer provider-key")

	_, err = tr.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, "https://bharatcode.dev", rec.seen.Get("HTTP-Referer"))
	require.Equal(t, "BharatCode", rec.seen.Get("X-Title"))
	require.Equal(t, "Bearer provider-key", rec.seen.Get("Authorization"))

	// RoundTrip must not mutate the caller's request.
	require.Empty(t, req.Header.Get("HTTP-Referer"))
	require.Equal(t, "Bearer provider-key", req.Header.Get("Authorization"))
}

func TestWithExtraHeadersNoopWhenEmpty(t *testing.T) {
	base := &http.Client{}
	require.Same(t, base, withExtraHeaders(base, nil))
	require.Same(t, base, withExtraHeaders(base, map[string]string{}))
}

// TestRegistryAppliesProviderHeaders drives a real provider through the registry
// and asserts the configured custom headers reach the wire, while the
// provider's own Authorization header is preserved.
func TestRegistryAppliesProviderHeaders(t *testing.T) {
	t.Setenv("BHARAT_TEST_KEY", "secret-key")

	var gotReferer, gotTitle, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := testConfig("openrouter", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "BHARAT_TEST_KEY"
	cfg.Providers[0].Headers = map[string]string{
		"HTTP-Referer": "https://bharatcode.dev",
		"X-Title":      "BharatCode",
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openrouter")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)
	for range events {
	}

	require.Equal(t, "https://bharatcode.dev", gotReferer)
	require.Equal(t, "BharatCode", gotTitle)
	require.Equal(t, "Bearer secret-key", gotAuth)
}
