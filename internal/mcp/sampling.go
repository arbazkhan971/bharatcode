package mcp

import (
	"context"
	"fmt"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// SamplingMessage is one message in a sampling conversation. Content holds the
// message text; non-text content (images, audio) is rendered to a placeholder
// string so the sampler sees a plain-text transcript.
type SamplingMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SamplingRequest is a server's request for the client to perform an LLM
// completion. It mirrors the MCP sampling/createMessage parameters in
// package-local types so callers need not depend on the MCP SDK.
type SamplingRequest struct {
	Messages         []SamplingMessage `json:"messages"`
	SystemPrompt     string            `json:"system_prompt,omitempty"`
	Temperature      float64           `json:"temperature,omitempty"`
	MaxTokens        int               `json:"max_tokens"`
	StopSequences    []string          `json:"stop_sequences,omitempty"`
	ModelPreferences []string          `json:"model_preferences,omitempty"`
}

// SamplingResponse is the completion the sampler produces for a SamplingRequest.
// Role defaults to "assistant" when empty.
type SamplingResponse struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason,omitempty"`
}

// Sampler performs an LLM completion on behalf of a server that requested
// sampling. It is injected by the caller (for example, backed by the agent's
// provider) so the mcp package stays decoupled from any concrete LLM client.
type Sampler func(ctx context.Context, req SamplingRequest) (SamplingResponse, error)

// samplingHandler adapts a Sampler to the MCP SDK's client.SamplingHandler
// interface. It is the client-side request-handling path for a server's
// sampling/createMessage request: it translates the SDK request into a
// package-local SamplingRequest, invokes the sampler, and translates the
// SamplingResponse back into the SDK result the server receives.
type samplingHandler struct {
	sample Sampler
}

var _ mcpclient.SamplingHandler = (*samplingHandler)(nil)

// CreateMessage handles a server's sampling/createMessage request by invoking
// the injected sampler and returning its completion to the server.
func (h *samplingHandler) CreateMessage(ctx context.Context, request mcpsdk.CreateMessageRequest) (*mcpsdk.CreateMessageResult, error) {
	if h.sample == nil {
		return nil, fmt.Errorf("mcp sampling: no sampler configured")
	}

	req := SamplingRequest{
		Messages:         make([]SamplingMessage, 0, len(request.Messages)),
		SystemPrompt:     request.SystemPrompt,
		Temperature:      request.Temperature,
		MaxTokens:        request.MaxTokens,
		StopSequences:    request.StopSequences,
		ModelPreferences: modelPreferenceHints(request.ModelPreferences),
	}
	for _, msg := range request.Messages {
		req.Messages = append(req.Messages, SamplingMessage{
			Role:    string(msg.Role),
			Content: contentText([]mcpsdk.Content{toContent(msg.Content)}),
		})
	}

	resp, err := h.sample(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp sampling: %w", err)
	}

	role := resp.Role
	if role == "" {
		role = string(mcpsdk.RoleAssistant)
	}
	return &mcpsdk.CreateMessageResult{
		SamplingMessage: mcpsdk.SamplingMessage{
			Role:    mcpsdk.Role(role),
			Content: mcpsdk.NewTextContent(resp.Content),
		},
		Model:      resp.Model,
		StopReason: resp.StopReason,
	}, nil
}

// modelPreferenceHints flattens a server's model preference hints into the
// model names the server suggested, in order. A nil preference yields nil.
func modelPreferenceHints(prefs *mcpsdk.ModelPreferences) []string {
	if prefs == nil || len(prefs.Hints) == 0 {
		return nil
	}
	hints := make([]string, 0, len(prefs.Hints))
	for _, hint := range prefs.Hints {
		if hint.Name != "" {
			hints = append(hints, hint.Name)
		}
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

// toContent normalizes a SamplingMessage's Content (typed as any by the SDK)
// into an mcpsdk.Content so it can be rendered by contentText. Content already
// of a concrete type is returned unchanged; content delivered over the wire
// arrives as a decoded JSON object (map[string]any) and is parsed by type.
// Anything else becomes empty text.
func toContent(content any) mcpsdk.Content {
	switch v := content.(type) {
	case mcpsdk.Content:
		return v
	case map[string]any:
		if parsed, err := mcpsdk.ParseContent(v); err == nil {
			return parsed
		}
	}
	return mcpsdk.NewTextContent("")
}
