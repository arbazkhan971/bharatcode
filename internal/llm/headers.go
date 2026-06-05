package llm

import "net/http"

// headerTransport injects a fixed set of custom headers into every request a
// provider sends. It is layered over the provider's existing client transport so
// it works uniformly across every provider dialect (chat/completions, Anthropic
// messages, Gemini, Ollama, Responses) without each provider re-plumbing its own
// header map.
//
// A custom header is only applied when the outgoing request does not already
// carry that header, so a provider-set Authorization / Content-Type always wins
// and a misconfigured custom header can never break authentication. This matches
// the "additive, never override" semantics other agents use for per-provider
// headers (OpenRouter attribution, Azure api-key, corporate proxy tokens).
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

// RoundTrip implements http.RoundTripper. Per the contract a RoundTripper must
// not mutate the supplied request, and the retry loop in postJSONWithHeaders
// reuses requests across attempts, so the request is cloned before headers are
// added.
func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	for k, v := range t.headers {
		if clone.Header.Get(k) == "" {
			clone.Header.Set(k, v)
		}
	}
	return base.RoundTrip(clone)
}

// withExtraHeaders returns a shallow copy of client whose transport injects the
// given headers. The original client is left untouched. When headers is empty
// the client is returned unchanged so the common no-headers path is allocation
// free and behaviour-identical to before.
func withExtraHeaders(client *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return client
	}
	copied := make(map[string]string, len(headers))
	for k, v := range headers {
		copied[k] = v
	}
	clone := *client
	clone.Transport = &headerTransport{base: client.Transport, headers: copied}
	return &clone
}
