package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// codexBaseURL is the ChatGPT Codex backend root. The provider appends
// /responses. This is OpenAI's private subscription backend, not the public
// API; see the package note on codexOAuthProvider.
const codexBaseURL = "https://chatgpt.com/backend-api/codex"

// codexOriginator identifies the client to the Codex backend, matching the
// value the Codex CLI sends.
const codexOriginator = "codex_cli_rs"

// codexClientVersion is sent as the version header. It need not match a real
// Codex release; the backend accepts the request regardless.
const codexClientVersion = "0.0.0-bharatcode"

// ErrCodexAuth indicates the local Codex credentials are missing or expired.
// Callers should advise the user to run the Codex CLI to (re)authenticate;
// BharatCode never writes or refreshes those credentials itself.
var ErrCodexAuth = fmt.Errorf("codex credentials unavailable: %w", ErrAuth)

// codexOAuthProvider talks to OpenAI's ChatGPT Codex backend using the
// subscription OAuth access token that the Codex CLI stores on disk.
//
// EXPERIMENTAL and unsupported. It reuses a ChatGPT subscription via a private,
// undocumented endpoint, which is outside OpenAI's terms for third-party
// clients and can break without notice. It is intended only for a developer's
// own local use.
//
// Credential handling is deliberately READ-ONLY: the provider reads the access
// token and account id from ~/.codex/auth.json and never writes the file or
// calls the token-refresh endpoint. The Codex CLI remains the sole owner of the
// token lifecycle, so BharatCode cannot invalidate the user's working Codex
// login. When the token is expired the backend returns 401 and the provider
// surfaces ErrCodexAuth with guidance to run the Codex CLI to refresh.
type codexOAuthProvider struct {
	name     string
	models   []Model
	client   *http.Client
	authPath string // Overridable for tests; defaults to ~/.codex/auth.json.
	endpoint string // Overridable for tests; defaults to the Codex backend URL.
}

// codexAuthFile is the subset of ~/.codex/auth.json the provider reads.
type codexAuthFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// newCodexOAuthProvider builds a provider backed by the Codex CLI's stored
// subscription credentials. authPath may be empty to use the default
// ~/.codex/auth.json location.
func newCodexOAuthProvider(name string, models []Model, client *http.Client, authPath string) Provider {
	if name == "" {
		name = "codex-oauth"
	}
	return &codexOAuthProvider{
		name:     name,
		models:   append([]Model(nil), models...),
		client:   client,
		authPath: authPath,
	}
}

func (p *codexOAuthProvider) Name() string { return p.name }

func (p *codexOAuthProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *codexOAuthProvider) SupportsTools() bool { return false }

func (p *codexOAuthProvider) SupportsImages() bool { return supportsImages(p.models) }

// readAuth loads the Codex access token and account id, read-only. It returns
// ErrCodexAuth when the file is missing or the token is absent.
func (p *codexOAuthProvider) readAuth() (token, accountID string, err error) {
	path := p.authPath
	if path == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("resolving home dir: %w", ErrCodexAuth)
		}
		path = filepath.Join(home, ".codex", "auth.json")
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		return "", "", fmt.Errorf("reading %s (run the Codex CLI to log in): %w", path, ErrCodexAuth)
	}
	var auth codexAuthFile
	if jerr := json.Unmarshal(data, &auth); jerr != nil {
		return "", "", fmt.Errorf("parsing codex auth: %w", ErrCodexAuth)
	}
	if auth.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("no access token in codex auth (run the Codex CLI to log in): %w", ErrCodexAuth)
	}
	return auth.Tokens.AccessToken, auth.Tokens.AccountID, nil
}

// Stream posts a non-streaming Responses request to the Codex backend and emits
// the parsed assistant text as Start/DeltaText/End events. Tool calls are not
// supported on this path and are rejected so callers do not silently lose them.
func (p *codexOAuthProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 {
		return nil, fmt.Errorf("codex responses tools: %w", ErrUnsupportedFeature)
	}
	if hasImages(req.Messages) && !modelSupportsImages(p.models, req.Model) {
		return nil, fmt.Errorf("model %q images: %w", req.Model, ErrUnsupportedFeature)
	}

	token, accountID, err := p.readAuth()
	if err != nil {
		return nil, err
	}

	body, err := buildResponsesRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building codex request: %w", err)
	}
	// The Codex backend requires store=false and stream=true, and returns
	// reasoning content only when explicitly included.
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
		"originator":    codexOriginator,
		"User-Agent":    codexOriginator,
		"version":       codexClientVersion,
	}
	if accountID != "" {
		headers["ChatGPT-Account-ID"] = accountID
	}

	url := p.endpoint
	if url == "" {
		url = codexBaseURL + "/responses"
	}
	resp, err := postJSONWithHeaders(ctx, p.client, url, headers, body)
	if err != nil {
		// A 401/403 surfaces as ErrAuth; re-wrap so the user is told to refresh
		// via the Codex CLI rather than to set an API key.
		if errors.Is(err, ErrAuth) {
			return nil, fmt.Errorf("codex token rejected (run the Codex CLI to refresh): %w", ErrCodexAuth)
		}
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

// codexStreamEvent is one Responses SSE event from the Codex backend. The event
// type lives inside the JSON data payload, not the SSE event name.
type codexStreamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func (p *codexOAuthProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.Name(), Model: model})
	if err := emitCodexBackendStream(ctx, resp.Body, events); err != nil {
		send(ctx, events, ErrorEvent{Err: err})
	}
}

// emitCodexBackendStream parses the ChatGPT Codex backend's Responses SSE stream
// and emits DeltaText/Thinking events as they arrive, followed by a terminal
// EndEvent carrying the reported usage. Start is emitted by the caller. The
// backend's event shape is identical for both the Codex-CLI-token path
// (codexOAuthProvider) and the BharatCode-login path (chatgptOAuthProvider), so
// both share this helper.
func emitCodexBackendStream(ctx context.Context, body io.Reader, events chan<- Event) error {
	var usage Usage
	err := readSSE(ctx, body, func(ev sseEvent) error {
		if ev.Data == "" || ev.Data == "[DONE]" {
			return nil
		}
		var e codexStreamEvent
		if jerr := json.Unmarshal([]byte(ev.Data), &e); jerr != nil {
			return nil // Ignore keep-alives and non-JSON lines.
		}
		switch e.Type {
		case "response.output_text.delta":
			if e.Delta != "" {
				send(ctx, events, DeltaTextEvent{Text: e.Delta})
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if e.Delta != "" {
				send(ctx, events, ThinkingEvent{Text: e.Delta})
			}
		case "response.completed":
			if u := e.Response.Usage; u != nil {
				usage = Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
			}
		case "response.failed", "response.incomplete":
			msg := "codex stream failed"
			if e.Response.Error != nil && e.Response.Error.Message != "" {
				msg = e.Response.Error.Message
			}
			return fmt.Errorf("%s: %w", msg, ErrServer)
		}
		return nil
	})
	if err != nil {
		return err
	}
	send(ctx, events, EndEvent{Usage: usage})
	return nil
}
