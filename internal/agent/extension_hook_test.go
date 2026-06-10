package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/extension"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// captureExtensions is an extensionDispatcher fake that records every dispatch
// and can be configured to veto a named tool or every provider request, or to
// surface a dispatch error on the provider-request event.
type captureExtensions struct {
	mu            sync.Mutex
	events        []extension.Event
	payloads      []extension.HookPayload
	blockTool     string
	blockProvider bool
	providerErr   error
}

func (c *captureExtensions) Dispatch(_ context.Context, event extension.Event, payload extension.HookPayload) (extension.HookResult, error) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.payloads = append(c.payloads, payload)
	c.mu.Unlock()

	switch event {
	case extension.BeforeToolCall:
		if c.blockTool != "" && payload.ToolName == c.blockTool {
			return extension.HookResult{Block: true, Reason: "vetoed"}, nil
		}
	case extension.BeforeProviderRequest:
		if c.providerErr != nil {
			return extension.HookResult{}, c.providerErr
		}
		if c.blockProvider {
			return extension.HookResult{Block: true, Reason: "circuit open"}, nil
		}
	}
	return extension.HookResult{}, nil
}

func (c *captureExtensions) count(event extension.Event) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e == event {
			n++
		}
	}
	return n
}

func (c *captureExtensions) payloadFor(event extension.Event) (extension.HookPayload, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, e := range c.events {
		if e == event {
			return c.payloads[i], true
		}
	}
	return extension.HookPayload{}, false
}

// TestRunDispatchesExtensionLifecycle asserts the loop dispatches session_start,
// before_provider_request, and before_tool_call to the extension host with the
// correct payloads over a turn that calls a tool.
func TestRunDispatchesExtensionLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: json.RawMessage(`{"path":"src/main.go"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	ext := &captureExtensions{}
	loop := New(Config{
		Name:       "coder",
		Model:      "fake-model",
		Provider:   provider,
		Tools:      registry,
		Sessions:   repo,
		Extensions: ext,
		Bus:        pubsub.NewTopic[Event]("agent-ext-test", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit it")))

	require.Equal(t, 1, ext.count(extension.SessionStart), "session_start must fire once")
	start, ok := ext.payloadFor(extension.SessionStart)
	require.True(t, ok)
	require.Equal(t, sessionID, start.SessionID)
	require.Equal(t, "coder", start.AgentName)

	// before_provider_request fires for each provider turn (two here).
	require.Equal(t, 2, ext.count(extension.BeforeProviderRequest), "before_provider_request fires per provider turn")
	req, ok := ext.payloadFor(extension.BeforeProviderRequest)
	require.True(t, ok)
	require.Equal(t, "fake-model", req.Model)
	require.Equal(t, "fake", req.Provider)
	require.Equal(t, sessionID, req.SessionID)
	require.Positive(t, req.MessageCount)

	// before_tool_call fired once with the tool name and its repaired input.
	require.Equal(t, 1, ext.count(extension.BeforeToolCall), "before_tool_call must fire for the edit tool")
	call, ok := ext.payloadFor(extension.BeforeToolCall)
	require.True(t, ok)
	require.Equal(t, "edit", call.ToolName)
	require.JSONEq(t, `{"path":"src/main.go"}`, string(call.ToolInput))
}

// TestExtensionVetoesToolCall asserts a before_tool_call veto prevents the tool
// from running and surfaces an error result to the model.
func TestExtensionVetoesToolCall(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	tool := &recordingTool{name: "edit", result: "edited"}
	registry := newFakeRegistry()
	registry.Register(tool)

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: json.RawMessage(`{"path":"x"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Understood."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	ext := &captureExtensions{blockTool: "edit"}
	loop := New(Config{
		Name:       "coder",
		Model:      "fake-model",
		Provider:   provider,
		Tools:      registry,
		Sessions:   repo,
		Extensions: ext,
		Bus:        pubsub.NewTopic[Event]("agent-ext-veto", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit it")))

	tool.mu.Lock()
	calls := len(tool.calls)
	tool.mu.Unlock()
	require.Equal(t, 0, calls, "a vetoed tool must not run")

	// The recorded transcript must carry the veto as an error tool result.
	msgs, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	found := false
	for _, m := range msgs {
		for _, block := range m.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.IsError &&
				strings.Contains(b.Content, "blocked by extension") {
				found = true
			}
		}
	}
	require.True(t, found, "the vetoed tool result must explain the block")
}

// TestExtensionVetoesProviderRequest asserts a before_provider_request veto
// fails the turn before the provider is ever called.
func TestExtensionVetoesProviderRequest(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "should not run"}, llm.EndEvent{}},
	}}

	ext := &captureExtensions{blockProvider: true}
	loop := New(Config{
		Name:       "coder",
		Model:      "fake-model",
		Provider:   provider,
		Tools:      registry,
		Sessions:   repo,
		Extensions: ext,
		Bus:        pubsub.NewTopic[Event]("agent-ext-provblock", 16),
	})

	err := loop.Run(ctx, sessionID, userMessage("hi"))
	require.Error(t, err, "a provider-request veto must fail the turn")

	provider.mu.Lock()
	reqs := len(provider.reqs)
	provider.mu.Unlock()
	require.Equal(t, 0, reqs, "the provider must not be called after a veto")
}
