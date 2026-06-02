package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// mcpSampler answers a server-requested LLM completion (sampling/createMessage)
// using one of the app's configured providers. It is installed on the MCP
// client before Start so the sampling capability is advertised, but it runs only
// when a server actually issues a sampling request, by which point the LLM
// registry is fully built. It selects a provider from the registry, runs a
// one-shot non-streaming completion by draining the provider's event stream, and
// returns the collected assistant text. A registry with no usable model yields a
// descriptive error rather than a hang, so the server's request fails cleanly.
func (a *App) mcpSampler(ctx context.Context, req mcp.SamplingRequest) (mcp.SamplingResponse, error) {
	if a == nil || a.LLM == nil {
		return mcp.SamplingResponse{}, fmt.Errorf("sampling: llm registry unavailable")
	}
	provider, model, err := a.samplingProvider()
	if err != nil {
		return mcp.SamplingResponse{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultSamplingMaxTokens
	}
	events, err := provider.Stream(ctx, llm.Request{
		Model:        model,
		Messages:     samplingMessages(req.Messages),
		SystemPrompt: req.SystemPrompt,
		Temperature:  req.Temperature,
		MaxTokens:    maxTokens,
	})
	if err != nil {
		return mcp.SamplingResponse{}, fmt.Errorf("sampling: streaming completion: %w", err)
	}
	return collectSamplingResponse(ctx, events, model)
}

// collectSamplingResponse drains a provider event stream into a SamplingResponse,
// concatenating assistant text deltas and surfacing the first stream error. It
// honors ctx cancellation so a sampling request never hangs on a stalled stream.
func collectSamplingResponse(ctx context.Context, events <-chan llm.Event, model string) (mcp.SamplingResponse, error) {
	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			return mcp.SamplingResponse{}, fmt.Errorf("sampling: %w", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				return mcp.SamplingResponse{
					Role:    "assistant",
					Content: text.String(),
					Model:   model,
				}, nil
			}
			switch e := ev.(type) {
			case llm.DeltaTextEvent:
				text.WriteString(e.Text)
			case llm.ErrorEvent:
				return mcp.SamplingResponse{}, fmt.Errorf("sampling: provider stream: %w", e.Err)
			}
		}
	}
}

// defaultSamplingMaxTokens caps a sampling completion when the requesting server
// supplies no positive limit, so a server that omits max_tokens does not request
// an unbounded generation.
const defaultSamplingMaxTokens = 1024

// samplingProvider resolves the provider and model id used to answer a sampling
// request. It uses the first configured model in the registry's stable order and
// looks up its provider, so the choice is deterministic across runs. It returns
// an error when no model or no provider for it is configured.
func (a *App) samplingProvider() (llm.Provider, string, error) {
	models := a.LLM.ListModels()
	if len(models) == 0 {
		return nil, "", fmt.Errorf("sampling: no models configured")
	}
	model := models[0]
	provider, err := a.LLM.Get(model.Provider)
	if err != nil {
		return nil, "", fmt.Errorf("sampling: resolving provider %q: %w", model.Provider, err)
	}
	return provider, model.ID, nil
}

// samplingMessages converts the MCP sampling transcript into the agent's message
// type. Each entry's plain-text content becomes a single text block; an empty or
// unknown role defaults to the user role so the provider always receives a valid
// conversation.
func samplingMessages(in []mcp.SamplingMessage) []message.Message {
	out := make([]message.Message, 0, len(in))
	for _, m := range in {
		role := message.RoleUser
		if strings.EqualFold(m.Role, string(message.RoleAssistant)) {
			role = message.RoleAssistant
		}
		out = append(out, message.Message{
			Role:    role,
			Content: []message.ContentBlock{message.TextBlock{Text: m.Content}},
		})
	}
	return out
}

// autoDeclineElicitation is the default MCP elicitation handler. BharatCode has
// no interactive surface wired for server-initiated structured-input prompts, so
// it declines every request rather than leaving the server's elicitation/create
// call hanging. Declining (rather than cancelling) tells the server the user
// chose not to provide the data, which is the safe, non-blocking default.
func autoDeclineElicitation(_ context.Context, _ mcp.ElicitationRequest) (mcp.ElicitationResponse, error) {
	return mcp.ElicitationResponse{Action: mcp.ElicitationDecline}, nil
}
