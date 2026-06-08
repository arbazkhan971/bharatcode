package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// chatgptOAuthProvider talks to OpenAI's ChatGPT-plan Codex backend using the
// subscription tokens obtained by BharatCode's own "Sign in with ChatGPT" flow
// (startChatGPTLogin). Unlike codexOAuthProvider — which is read-only and reuses
// the Codex CLI's on-disk token — this provider owns the full credential
// lifecycle: it loads the stored tokens, transparently refreshes the access
// token when it is near expiry, and writes the refreshed token back.
//
// EXPERIMENTAL and unsupported. It reuses a ChatGPT subscription via private,
// undocumented OpenAI endpoints, outside OpenAI's terms for third-party clients,
// and can break without notice. Personal single-account use only — no account
// pooling.
type chatgptOAuthProvider struct {
	name     string
	models   []Model
	client   *http.Client
	authPath string // Overridable for tests; defaults to the config-dir auth file.
	endpoint string // Overridable for tests; defaults to the Codex backend URL.
	tokenURL string // Overridable for tests; defaults to the OAuth token endpoint.

	// mu guards the on-disk refresh so two concurrent Streams do not both POST a
	// refresh grant and race each other's file write.
	mu  sync.Mutex
	now func() time.Time // Injectable clock for tests; defaults to time.Now.
}

// newChatGPTOAuthProvider builds a provider backed by BharatCode's stored
// ChatGPT subscription credentials. authPath may be empty to use the default
// config-dir location.
func newChatGPTOAuthProvider(name string, models []Model, client *http.Client, authPath string) Provider {
	if name == "" {
		name = "chatgpt"
	}
	return &chatgptOAuthProvider{
		name:     name,
		models:   append([]Model(nil), models...),
		client:   client,
		authPath: authPath,
	}
}

func (p *chatgptOAuthProvider) Name() string { return p.name }

func (p *chatgptOAuthProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

// SupportsTools reports whether the active model accepts function tools. The
// ChatGPT-plan Codex backend (chatgpt.com/backend-api/codex/responses) is the
// same surface the Codex CLI drives with tools, so tools are supported for
// tool-capable models — a coding agent is unusable without them.
func (p *chatgptOAuthProvider) SupportsTools() bool { return supportsTools(p.models) }

func (p *chatgptOAuthProvider) SupportsImages() bool { return supportsImages(p.models) }

func (p *chatgptOAuthProvider) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// credentials loads the stored tokens and refreshes the access token when it is
// at or past its skewed expiry, persisting any refreshed token. It returns the
// usable access token and the ChatGPT account id for the request headers.
func (p *chatgptOAuthProvider) credentials(ctx context.Context) (token, accountID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	path, err := chatgptAuthPath(p.authPath)
	if err != nil {
		return "", "", fmt.Errorf("locating chatgpt auth: %w", ErrChatGPTAuth)
	}
	auth, err := loadChatGPTAuth(path)
	if err != nil {
		return "", "", err
	}
	if !auth.expired(p.clock()) {
		return auth.Tokens.AccessToken, auth.Tokens.AccountID, nil
	}
	// The access token is expired (or unknown age). Refresh it when a refresh
	// token is available; otherwise the user must sign in again.
	if auth.Tokens.RefreshToken == "" {
		return "", "", fmt.Errorf("access token expired and no refresh token (run 'bharatcode auth chatgpt'): %w", ErrChatGPTAuth)
	}
	tokenURL := p.tokenURL
	if tokenURL == "" {
		tokenURL = chatgptTokenURL
	}
	resp, rerr := refreshChatGPTTokens(ctx, p.client, tokenURL, auth.Tokens.RefreshToken)
	if rerr != nil {
		return "", "", fmt.Errorf("refreshing chatgpt token: %w", rerr)
	}
	refreshed, ferr := authFromTokenResponse(resp, auth, p.clock())
	if ferr != nil {
		return "", "", ferr
	}
	if serr := saveChatGPTAuth(path, refreshed); serr != nil {
		// A failed write is non-fatal for this request (the in-memory token is
		// valid), but log nothing here — surface a usable token and let the next
		// run retry the write. The error is intentionally swallowed rather than
		// failing an otherwise-good request.
		_ = serr
	}
	return refreshed.Tokens.AccessToken, refreshed.Tokens.AccountID, nil
}

// Stream posts a Responses request to the ChatGPT-plan Codex backend with the
// OAuth bearer and account-id headers, reusing the shared Responses request
// builder and SSE parser. Tool calls are rejected on this path.
func (p *chatgptOAuthProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 && !modelSupportsTools(p.models, req.Model) {
		return nil, fmt.Errorf("model %q tools: %w", req.Model, ErrUnsupportedFeature)
	}
	if hasImages(req.Messages) && !modelSupportsImages(p.models, req.Model) {
		return nil, fmt.Errorf("model %q images: %w", req.Model, ErrUnsupportedFeature)
	}

	token, accountID, err := p.credentials(ctx)
	if err != nil {
		return nil, err
	}

	body, err := buildResponsesRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building chatgpt request: %w", err)
	}
	// The ChatGPT-plan backend requires store=false and stream=true, and only
	// returns reasoning content when it is explicitly included.
	storeFalse := false
	body.Store = &storeFalse
	body.Stream = true
	if isReasoningModel(req.Model) {
		body.Include = []string{"reasoning.encrypted_content"}
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Authorization": "Bearer " + token,
		// originator identifies the client to the backend; it must match the value
		// the Codex CLI sends or the request is rejected.
		"originator": codexOriginator,
		"User-Agent": codexOriginator,
		"version":    codexClientVersion,
	}
	if accountID != "" {
		headers["chatgpt-account-id"] = accountID
	}

	url := p.endpoint
	if url == "" {
		url = chatgptBackendURL + "/responses"
	}
	resp, err := postJSONWithHeaders(ctx, p.client, url, headers, body)
	if err != nil {
		// A 401/403 surfaces as ErrAuth; re-wrap so the user is told to sign in
		// again rather than to set an API key. (A refresh was already attempted in
		// credentials when the token looked expired; reaching here means the
		// backend rejected an apparently-live token, so re-auth is the fix.)
		if errors.Is(err, ErrAuth) {
			return nil, fmt.Errorf("chatgpt token rejected (run 'bharatcode auth chatgpt'): %w", ErrChatGPTAuth)
		}
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

// readResponse parses the Codex backend's Responses SSE stream into BharatCode
// events. The wire shape is identical to the Codex CLI path, so it reuses the
// same codexStreamEvent decoding via a shared helper.
func (p *chatgptOAuthProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.Name(), Model: model})
	// The ChatGPT-plan backend speaks the same Responses SSE wire format as the
	// public Responses API, including function-call events. Use the full
	// Responses stream parser (which decodes tool calls) rather than a text-only
	// reader — without it the model's tool calls are silently dropped and a
	// coding turn produces no file and no output.
	if err := emitResponsesStream(ctx, resp.Body, events); err != nil {
		send(ctx, events, ErrorEvent{Err: err})
	}
}
