package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRunJSONStreamsEventsInOrder(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Hello from the model."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 3}},
		},
	}}
	restore := installFakeApp(t, provider)
	defer restore()

	stdout, stderr, err := executeRoot(t, "run", "--json", "do the thing")
	require.NoError(t, err)
	require.Empty(t, stderr)

	events := parseNDJSON(t, stdout)
	types := eventTypes(events)
	// turn_started must precede the llm_response carrying the assistant text,
	// which must precede turn_finished.
	require.Equal(t, []string{"turn_started", "llm_response", "turn_finished"}, types)

	llmResp := findEvent(t, events, "llm_response")
	require.Equal(t, "Hello from the model.", llmResp.Text)
	require.NotEmpty(t, llmResp.SessionID)
	require.Equal(t, "coder", llmResp.Agent)

	// Every event must carry the same session id and agent name.
	for _, ev := range events {
		require.Equal(t, llmResp.SessionID, ev.SessionID)
		require.Equal(t, "coder", ev.Agent)
	}
}

func TestRunJSONStreamsToolCallEvents(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Checking the weather."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "echo_tool", Input: json.RawMessage(`{"text":"hi"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 6, OutputTokens: 2}},
		},
		{
			llm.DeltaTextEvent{Text: "All done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 2}},
		},
	}}
	restore := installFakeApp(t, provider, &echoTool{})
	defer restore()

	stdout, stderr, err := executeRoot(t, "run", "--json", "use the tool")
	require.NoError(t, err)
	require.Empty(t, stderr)

	events := parseNDJSON(t, stdout)
	types := eventTypes(events)
	require.Equal(t, []string{
		"turn_started",
		"llm_response",
		"tool_called",
		"tool_result",
		"llm_response",
		"turn_finished",
	}, types)

	called := findEvent(t, events, "tool_called")
	require.Equal(t, "echo_tool", called.Tool)
	result := findEvent(t, events, "tool_result")
	require.Equal(t, "echo_tool", result.Tool)

	// The first llm_response carries the pre-tool text, the second the closing text.
	var texts []string
	for _, ev := range events {
		if ev.Type == "llm_response" {
			texts = append(texts, ev.Text)
		}
	}
	require.Equal(t, []string{"Checking the weather.", "All done."}, texts)
}

func TestRunOutputLastMessageWritesFinalText(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Final answer is 42."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 2, OutputTokens: 2}},
		},
	}}
	restore := installFakeApp(t, provider)
	defer restore()

	outPath := filepath.Join(t.TempDir(), "last.txt")
	stdout, stderr, err := executeRoot(t, "run", "--output-last-message", outPath, "what is the answer")
	require.NoError(t, err)
	require.Empty(t, stderr)
	// Without --json the final text still prints to stdout.
	require.Equal(t, "Final answer is 42.\n", stdout)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	// The file holds exactly the final assistant text with no trailing newline.
	require.Equal(t, "Final answer is 42.", string(data))

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestRunJSONWithOutputLastMessage(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "JSON and file."},
			llm.EndEvent{},
		},
	}}
	restore := installFakeApp(t, provider)
	defer restore()

	outPath := filepath.Join(t.TempDir(), "last.txt")
	stdout, stderr, err := executeRoot(t, "run", "--json", "--output-last-message", outPath, "go")
	require.NoError(t, err)
	require.Empty(t, stderr)

	// In JSON mode stdout carries only NDJSON, never the bare final text line.
	events := parseNDJSON(t, stdout)
	require.Equal(t, []string{"turn_started", "llm_response", "turn_finished"}, eventTypes(events))
	require.Equal(t, "JSON and file.", findEvent(t, events, "llm_response").Text)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, "JSON and file.", string(data))
}

func TestNewRunEventMapsEveryKind(t *testing.T) {
	assistant := &message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "mapped text"}},
	}
	cases := []struct {
		name string
		in   agent.Event
		want runEvent
	}{
		{
			name: "turn_started",
			in:   agent.Event{Kind: agent.EventTurnStarted, SessionID: "s1", AgentName: "coder"},
			want: runEvent{Type: "turn_started", SessionID: "s1", Agent: "coder"},
		},
		{
			name: "llm_response",
			in:   agent.Event{Kind: agent.EventLLMResponse, SessionID: "s1", AgentName: "coder", Message: assistant},
			want: runEvent{Type: "llm_response", SessionID: "s1", Agent: "coder", Text: "mapped text"},
		},
		{
			name: "tool_called",
			in:   agent.Event{Kind: agent.EventToolCalled, SessionID: "s1", AgentName: "coder", ToolName: "grep"},
			want: runEvent{Type: "tool_called", SessionID: "s1", Agent: "coder", Tool: "grep"},
		},
		{
			name: "tool_result",
			in:   agent.Event{Kind: agent.EventToolResult, SessionID: "s1", AgentName: "coder", ToolName: "grep"},
			want: runEvent{Type: "tool_result", SessionID: "s1", Agent: "coder", Tool: "grep"},
		},
		{
			name: "loop_detected",
			in:   agent.Event{Kind: agent.EventLoopDetected, SessionID: "s1", AgentName: "coder", Message: assistant},
			want: runEvent{Type: "loop_detected", SessionID: "s1", Agent: "coder", Text: "mapped text"},
		},
		{
			name: "turn_finished",
			in:   agent.Event{Kind: agent.EventTurnFinished, SessionID: "s1", AgentName: "coder"},
			want: runEvent{Type: "turn_finished", SessionID: "s1", Agent: "coder"},
		},
		{
			name: "run_error",
			in:   agent.Event{Kind: agent.EventRunError, SessionID: "s1", AgentName: "coder", ToolName: "bash", Err: errors.New("boom")},
			want: runEvent{Type: "run_error", SessionID: "s1", Agent: "coder", Tool: "bash", Error: "boom"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, newRunEvent(tc.in))
		})
	}
}

// installFakeApp overrides newApp so the run command uses an App backed by an
// in-memory session repo and the supplied scripted provider. It returns a
// restore func that reinstates the original newApp.
func installFakeApp(t *testing.T, provider llm.Provider, extraTools ...tools.Tool) func() {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "run.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	repo := session.NewRepo(database)
	bus := fullBus()

	registry := tools.NewRegistry(tools.Dependencies{WorkDir: t.TempDir()})
	for _, tool := range extraTools {
		registry.Register(tool)
	}

	coord, err := agent.NewCoordinator(nil, agent.Dependencies{
		Tools:     registry,
		Sessions:  repo,
		Bus:       bus.Agent,
		Providers: map[string]llm.Provider{"stub": provider},
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	fake := &app.App{
		DB:       database,
		Bus:      bus,
		Sessions: repo,
		Agent:    coord,
	}

	old := newApp
	newApp = func(ctx context.Context, opts app.Options) (*app.App, error) {
		_ = ctx
		_ = opts
		return fake, nil
	}
	return func() { newApp = old }
}

// fullBus builds an app.Bus with every topic populated so App.Close is safe.
func fullBus() *app.Bus {
	return &app.Bus{
		Ledger:      pubsub.NewTopic[ledger.Summary]("test_ledger", 8),
		FileChanges: pubsub.NewTopic[filetracker.Change]("test_files", 8),
		LSP:         pubsub.NewTopic[lsp.Diagnostic]("test_lsp", 8),
		MCP:         pubsub.NewTopic[mcp.Event]("test_mcp", 8),
		Agent:       pubsub.NewTopic[agent.Event]("test_agent", 64),
		Permission:  pubsub.NewTopic[pubsub.PermissionRequest]("test_perm", 8),
		Shell:       pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell", 8),
		ToolCalls:   pubsub.NewTopic[pubsub.ToolCallPayload]("test_tool_calls", 8),
		Todo:        pubsub.NewTopic[tools.TodoEvent]("test_todo", 8),
	}
}

func parseNDJSON(t *testing.T, raw string) []runEvent {
	t.Helper()
	var out []runEvent
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var ev runEvent
		require.NoErrorf(t, json.Unmarshal([]byte(line), &ev), "line %q", line)
		out = append(out, ev)
	}
	return out
}

func eventTypes(events []runEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func findEvent(t *testing.T, events []runEvent, typ string) runEvent {
	t.Helper()
	for _, ev := range events {
		if ev.Type == typ {
			return ev
		}
	}
	t.Fatalf("no %q event in %v", typ, eventTypes(events))
	return runEvent{}
}

// scriptedProvider returns a fixed sequence of event scripts, one per Stream
// call, mirroring the agent package's own provider stub but offline.
type scriptedProvider struct {
	mu      sync.Mutex
	scripts [][]llm.Event
}

func (p *scriptedProvider) Name() string { return "stub" }

func (p *scriptedProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	_ = req
	p.mu.Lock()
	var events []llm.Event
	if len(p.scripts) > 0 {
		events = p.scripts[0]
		p.scripts = p.scripts[1:]
	}
	p.mu.Unlock()

	ch := make(chan llm.Event, len(events))
	go func() {
		defer close(ch)
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (p *scriptedProvider) Models() []llm.Model {
	return []llm.Model{{ID: "stub-model", Provider: "stub", ContextWindow: 8192, SupportsTools: true}}
}

func (p *scriptedProvider) SupportsTools() bool  { return true }
func (p *scriptedProvider) SupportsImages() bool { return false }

// echoTool is a trivial tool used to exercise the tool_called/tool_result path.
type echoTool struct{}

func (echoTool) Name() string            { return "echo_tool" }
func (echoTool) Description() string     { return "Echoes its input." }
func (echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (echoTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	return tools.Result{Content: "echoed: " + string(args)}, nil
}
