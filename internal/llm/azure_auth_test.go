package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAzureOpenAI(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{
			name:    "public cloud deployment",
			baseURL: "https://my-resource.openai.azure.com/openai/deployments/gpt-4o?api-version=2024-06-01",
			want:    true,
		},
		{
			name:    "uppercase host still matches",
			baseURL: "https://My-Resource.OpenAI.Azure.Com/openai/deployments/gpt-4o",
			want:    true,
		},
		{
			name:    "azure government cloud",
			baseURL: "https://res.openai.azure.us/openai/deployments/gpt-4o",
			want:    true,
		},
		{
			name:    "azure china cloud",
			baseURL: "https://res.openai.azure.cn/openai/deployments/gpt-4o",
			want:    true,
		},
		{
			// AI Foundry resources default to a Cognitive Services host that serves
			// the OpenAI route with the same api-key auth scheme.
			name:    "azure ai foundry cognitive services host",
			baseURL: "https://my-resource.cognitiveservices.azure.com/openai/deployments/gpt-4o?api-version=2024-06-01",
			want:    true,
		},
		{
			name:    "cognitive services host is case-insensitive",
			baseURL: "https://My-Resource.CognitiveServices.Azure.Com/openai/deployments/gpt-4o",
			want:    true,
		},
		{
			name:    "openai public api is not azure",
			baseURL: "https://api.openai.com/v1",
			want:    false,
		},
		{
			name:    "openrouter is not azure",
			baseURL: "https://openrouter.ai/api/v1",
			want:    false,
		},
		{
			name:    "empty base url",
			baseURL: "",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isAzureOpenAI(tc.baseURL))
		})
	}
}

// TestPostOpenAIJSONAzureUsesAPIKeyHeader asserts that an Azure-hosted endpoint
// authenticates with the "api-key" header rather than the "Authorization: Bearer"
// scheme every other OpenAI-dialect provider uses, so an Azure deployment
// configured only with api_key_env authenticates without a manual header.
func TestPostOpenAIJSONAzureUsesAPIKeyHeader(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// The Azure-looking baseURL selects the auth scheme; the request itself is
	// sent to the local stub via url, mirroring how appendPath would target a
	// deployment path while the host stays Azure.
	resp, err := postOpenAIJSON(
		context.Background(),
		server.Client(),
		"https://res.openai.azure.com/openai/deployments/gpt-4o?api-version=2024-06-01",
		server.URL,
		"azure-secret",
		map[string]string{"hello": "world"},
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, "azure-secret", gotAPIKey)
	require.Empty(t, gotAuth, "Azure must not receive a Bearer Authorization header")
}

// TestPostOpenAIJSONAzureCognitiveServicesUsesAPIKeyHeader asserts an AI Foundry
// resource served on the ".cognitiveservices.azure.com" host authenticates with
// the "api-key" header too, so a deployment configured only with api_key_env works
// regardless of which of the two Azure host families it lives on.
func TestPostOpenAIJSONAzureCognitiveServicesUsesAPIKeyHeader(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := postOpenAIJSON(
		context.Background(),
		server.Client(),
		"https://res.cognitiveservices.azure.com/openai/deployments/gpt-4o?api-version=2024-06-01",
		server.URL,
		"azure-secret",
		map[string]string{"hello": "world"},
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, "azure-secret", gotAPIKey)
	require.Empty(t, gotAuth, "Azure must not receive a Bearer Authorization header")
}

// TestPostOpenAIJSONNonAzureUsesBearer asserts the default OpenAI auth scheme is
// unchanged for every non-Azure endpoint.
func TestPostOpenAIJSONNonAzureUsesBearer(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := postOpenAIJSON(
		context.Background(),
		server.Client(),
		server.URL,
		server.URL,
		"sk-secret",
		map[string]string{"hello": "world"},
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, "Bearer sk-secret", gotAuth)
	require.Empty(t, gotAPIKey, "non-Azure endpoints must not receive an api-key header")
}

// TestPostOpenAIJSONEmptyKeySendsNeitherAuth asserts a keyless local endpoint
// (Ollama, LM Studio) is sent no auth header even when its host would otherwise
// be classified as Azure.
func TestPostOpenAIJSONEmptyKeySendsNeitherAuth(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := postOpenAIJSON(
		context.Background(),
		server.Client(),
		"https://res.openai.azure.com/openai/deployments/gpt-4o",
		server.URL,
		"",
		map[string]string{"hello": "world"},
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.Empty(t, gotAuth)
	require.Empty(t, gotAPIKey)
}
