package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type fakeRemote struct {
	tools        []mcpsdk.Tool
	resources    []mcpsdk.Resource
	prompts      []mcpsdk.Prompt
	resourceOut  *mcpsdk.ReadResourceResult
	promptOut    *mcpsdk.GetPromptResult
	promptArgs   map[string]string
	callOut      *mcpsdk.CallToolResult
	callCount    int
	closeCount   int
	lost         func(error)
	sampling     mcpclient.SamplingHandler
	elicitation  mcpclient.ElicitationHandler
	notify       func(mcpsdk.JSONRPCNotification)
	subscribed   []string
	unsubscribed []string
	// roots captures the roots handler the client installs, letting the test
	// drive a server-issued roots/list request through it.
	roots *rootsHandler
	// rootsChangedMethods records the method of each roots-changed notification
	// the client pushed to this conn, in order, as a real server's read
	// goroutine would see them arrive off the wire.
	rootsChangedMethods []string
	// gotToken records the progress token attached to the most recent CallTool
	// request, as it arrived on the conn.
	gotToken any
	// progressUpdates, when set, are emitted synchronously mid-call by CallTool
	// as notifications/progress, each echoing the request's progress token, just
	// before the call returns its result. Emission is opt-in so other tests that
	// share CallTool are unaffected.
	progressUpdates []mcpsdk.ProgressNotificationParams
}

// setSamplingHandler records the handler the client installs, letting the test
// drive a server-issued sampling/createMessage request through it.
func (f *fakeRemote) setSamplingHandler(h mcpclient.SamplingHandler) {
	f.sampling = h
}

// setElicitationHandler records the handler the client installs, letting the
// test drive a server-issued elicitation/create request through it.
func (f *fakeRemote) setElicitationHandler(h mcpclient.ElicitationHandler) {
	f.elicitation = h
}

// setRootsHandler records the roots handler the client installs, letting the
// test drive a server-issued roots/list request through it.
func (f *fakeRemote) setRootsHandler(h *rootsHandler) {
	f.roots = h
}

// sendRootsListChanged records the method of the roots-changed notification the
// client pushed to this conn, as the client does when its advertised roots
// change, letting the test assert the right notification was sent.
func (f *fakeRemote) sendRootsListChanged(n mcpsdk.RootsListChangedNotification) {
	f.rootsChangedMethods = append(f.rootsChangedMethods, n.Method)
}

// listRoots drives a server-pushed roots/list request through the installed
// handler and returns the roots the client answered with, as a real server
// would receive them off the wire.
func (f *fakeRemote) listRoots(t *testing.T) []mcpsdk.Root {
	t.Helper()
	require.NotNil(t, f.roots, "client did not install a roots handler on the conn")
	result, err := f.roots.ListRoots(context.Background(), mcpsdk.ListRootsRequest{})
	require.NoError(t, err)
	return result.Roots
}

// OnNotification records the notification handler the client installs, letting
// the test drive a server-pushed notification through it.
func (f *fakeRemote) OnNotification(fn func(mcpsdk.JSONRPCNotification)) {
	f.notify = fn
}

// Subscribe records the subscribed URI as a real conn's resources/subscribe
// request would register interest server-side.
func (f *fakeRemote) Subscribe(_ context.Context, req mcpsdk.SubscribeRequest) error {
	f.subscribed = append(f.subscribed, req.Params.URI)
	return nil
}

// Unsubscribe records the unsubscribed URI.
func (f *fakeRemote) Unsubscribe(_ context.Context, req mcpsdk.UnsubscribeRequest) error {
	f.unsubscribed = append(f.unsubscribed, req.Params.URI)
	return nil
}

// emitResourceUpdated drives a server-pushed resources/updated notification for
// uri through the installed handler, as a real conn's read goroutine would on
// receiving the notification off the wire.
func (f *fakeRemote) emitResourceUpdated(uri string) {
	if f.notify == nil {
		return
	}
	f.notify(mcpsdk.JSONRPCNotification{
		Notification: mcpsdk.Notification{
			Method: mcpsdk.MethodNotificationResourceUpdated,
			Params: mcpsdk.NotificationParams{
				AdditionalFields: map[string]any{"uri": uri},
			},
		},
	})
}

func (f *fakeRemote) Close() error {
	f.closeCount++
	return nil
}

func (f *fakeRemote) CallTool(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	f.callCount++
	if req.Params.Meta != nil {
		f.gotToken = req.Params.Meta.ProgressToken
	}
	// Emit any configured progress updates mid-call: synchronously, before the
	// result returns, each echoing this request's progress token, exactly as a
	// real server pushes notifications/progress during a long-running call. This
	// makes "mid-call" deterministic without goroutines or sleeps.
	for _, update := range f.progressUpdates {
		update.ProgressToken = f.gotToken
		f.emitProgress(update)
	}
	if f.callOut != nil {
		return f.callOut, nil
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{mcpsdk.TextContent{Type: "text", Text: "ok"}},
	}, nil
}

// emitProgress drives a server-pushed notifications/progress through the
// installed handler. The params are JSON round-tripped so they arrive in the
// handler's AdditionalFields with the same float64/string types the real wire
// produces, not as already-typed Go values.
func (f *fakeRemote) emitProgress(params mcpsdk.ProgressNotificationParams) {
	if f.notify == nil {
		return
	}
	raw, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(err)
	}
	f.notify(mcpsdk.JSONRPCNotification{
		Notification: mcpsdk.Notification{
			Method: methodNotificationProgress,
			Params: mcpsdk.NotificationParams{AdditionalFields: fields},
		},
	})
}

func (f *fakeRemote) ListTools(context.Context, mcpsdk.ListToolsRequest) (*mcpsdk.ListToolsResult, error) {
	return &mcpsdk.ListToolsResult{Tools: f.tools}, nil
}

func (f *fakeRemote) ListResources(context.Context, mcpsdk.ListResourcesRequest) (*mcpsdk.ListResourcesResult, error) {
	return &mcpsdk.ListResourcesResult{Resources: f.resources}, nil
}

func (f *fakeRemote) ReadResource(context.Context, mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	if f.resourceOut != nil {
		return f.resourceOut, nil
	}
	return &mcpsdk.ReadResourceResult{}, nil
}

func (f *fakeRemote) ListPrompts(context.Context, mcpsdk.ListPromptsRequest) (*mcpsdk.ListPromptsResult, error) {
	return &mcpsdk.ListPromptsResult{Prompts: f.prompts}, nil
}

func (f *fakeRemote) GetPrompt(_ context.Context, req mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	f.promptArgs = req.Params.Arguments
	if f.promptOut != nil {
		return f.promptOut, nil
	}
	return &mcpsdk.GetPromptResult{}, nil
}

func (f *fakeRemote) OnConnectionLost(fn func(error)) {
	f.lost = fn
}

func withFakeConnector(t *testing.T, fn connector) {
	t.Helper()
	old := newRemote
	newRemote = fn
	t.Cleanup(func() {
		newRemote = old
	})
}

func TestValidateServerConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  ServerConfig
	}{
		{
			name: "bad name",
			cfg:  ServerConfig{Name: "Bad", Transport: TransportStdio, Command: []string{"server"}},
		},
		{
			name: "bad transport",
			cfg:  ServerConfig{Name: "good", Transport: "pipe", Command: []string{"server"}},
		},
		{
			name: "missing command",
			cfg:  ServerConfig{Name: "good", Transport: TransportStdio},
		},
		{
			name: "missing url",
			cfg:  ServerConfig{Name: "good", Transport: TransportHTTP},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, ValidateServerConfig(tt.cfg))
		})
	}

	require.NoError(t, ValidateServerConfig(ServerConfig{
		Name:      "good_1",
		Transport: TransportStdio,
		Command:   []string{"server"},
	}))
}

func TestClientStartsAndDiscoversToolsAndResources(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	remote := &fakeRemote{
		tools: []mcpsdk.Tool{{
			Name:           "read_file",
			Description:    "Read a file.",
			RawInputSchema: schema,
		}},
		resources: []mcpsdk.Resource{{
			URI:         "filesystem:///tmp/a.txt",
			Name:        "a.txt",
			Description: "fixture",
			MIMEType:    "text/plain",
		}},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})

	bus := pubsub.NewTopic[Event]("mcp_test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{
			Name:      "filesystem",
			Transport: "stdio",
			Command:   "server",
		}},
		Permissions: config.PermConfig{AllowAll: true},
	}, permission.New(&config.Config{Permissions: config.PermConfig{AllowAll: true}}, nil), bus)

	require.NoError(t, client.Start(context.Background()))
	tools := client.Tools()
	require.Len(t, tools, 1)
	require.Equal(t, "filesystem__read_file", tools[0].Name())
	require.True(t, json.Valid(tools[0].Schema()))
	require.True(t, string(schema) == string(tools[0].Schema()))
	require.Equal(t, []Resource{{
		Server:      "filesystem",
		URI:         "filesystem:///tmp/a.txt",
		Name:        "a.txt",
		Description: "fixture",
		MimeType:    "text/plain",
	}}, client.Resources())

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			return event.State == StateConnected && len(event.ToolNames) == 1
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestRefreshRejectsInvalidRemoteToolSchema(t *testing.T) {
	client := &Client{}
	server := &Server{
		name:   "bad",
		cfg:    ServerConfig{Name: "bad", Transport: TransportStdio, Command: []string{"server"}},
		logger: slog.Default(),
	}
	remote := &fakeRemote{
		tools: []mcpsdk.Tool{{
			Name:           "broken",
			RawInputSchema: json.RawMessage(`{"type":"object"`),
		}},
	}

	err := client.refresh(context.Background(), server, remote)

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid input schema")
}

func TestToolRunPermissionDeniedDoesNotCallServer(t *testing.T) {
	remote := &fakeRemote{tools: []mcpsdk.Tool{{Name: "danger", RawInputSchema: json.RawMessage(`{"type":"object"}`)}}}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	cfg := &config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
		Permissions: config.PermConfig{
			Deny: []string{"server__danger"},
		},
	}
	client := NewClient(cfg, permission.New(cfg, nil), nil)
	require.NoError(t, client.Start(context.Background()))

	result, err := client.Tools()[0].Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Zero(t, remote.callCount)
}

func TestToolRunUnavailable(t *testing.T) {
	server := &Server{name: "offline", state: StateDisconnected}
	tool := &toolAdapter{server: server, name: "offline__tool", remoteName: "tool"}
	result, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrToolUnavailable)
	require.True(t, result.IsError)
}

func TestReadResource(t *testing.T) {
	remote := &fakeRemote{
		tools: []mcpsdk.Tool{},
		resourceOut: &mcpsdk.ReadResourceResult{
			Contents: []mcpsdk.ResourceContents{
				mcpsdk.TextResourceContents{
					URI:      "filesystem:///tmp/a.txt",
					MIMEType: "text/plain",
					Text:     "hello",
				},
			},
		},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "filesystem", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	data, mimeType, err := client.ReadResource(context.Background(), "filesystem:///tmp/a.txt")
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	require.Equal(t, "text/plain", mimeType)
}

func TestResourceSubscriptionDeliversUpdatesAndUnsubscribeStops(t *testing.T) {
	const uri = "filesystem:///tmp/watched.txt"
	remote := &fakeRemote{tools: []mcpsdk.Tool{}}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "filesystem", Transport: "stdio", Command: "server"}},
	}, nil, nil)

	var updates []ResourceUpdate
	client.SetResourceUpdateHandler(func(u ResourceUpdate) {
		updates = append(updates, u)
	})
	require.NoError(t, client.Start(context.Background()))

	// An update that arrives with no active subscription is dropped, even though
	// a handler is installed: delivery is gated on the client-side set.
	remote.emitResourceUpdated(uri)
	require.Empty(t, updates, "update delivered before any subscription")

	// Subscribe registers interest: the request reaches the conn with the URI.
	require.NoError(t, client.Subscribe(context.Background(), uri))
	require.Equal(t, []string{uri}, remote.subscribed)

	// A server-pushed update for the subscribed URI is delivered to the handler.
	remote.emitResourceUpdated(uri)
	require.Equal(t, []ResourceUpdate{{Server: "filesystem", URI: uri}}, updates)

	// Updates for a different, unsubscribed URI on the same server are dropped.
	remote.emitResourceUpdated("filesystem:///tmp/other.txt")
	require.Len(t, updates, 1, "update delivered for an unsubscribed URI")

	// Unsubscribe cancels the subscription on the conn and stops delivery.
	require.NoError(t, client.Unsubscribe(context.Background(), uri))
	require.Equal(t, []string{uri}, remote.unsubscribed)

	// The server keeps pushing updates, but the client now drops them: delivery
	// is gated on the subscription set, which Unsubscribe cleared.
	remote.emitResourceUpdated(uri)
	require.Len(t, updates, 1, "update delivered after Unsubscribe stopped the subscription")
}

func TestToolCallDeliversProgressAndReturnsResult(t *testing.T) {
	remote := &fakeRemote{
		tools: []mcpsdk.Tool{{
			Name:           "build",
			RawInputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		// The server reports progress mid-call: two updates, then completion.
		progressUpdates: []mcpsdk.ProgressNotificationParams{
			{Progress: 1.0, Total: 3.0, Message: "compiling"},
			{Progress: 3.0, Total: 3.0, Message: "done"},
		},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	cfg := &config.Config{
		MCP:         []config.MCPServer{{Name: "builder", Transport: "stdio", Command: "server"}},
		Permissions: config.PermConfig{AllowAll: true},
	}
	client := NewClient(cfg, permission.New(cfg, nil), nil)

	var updates []ToolProgress
	client.SetToolProgressHandler(func(p ToolProgress) {
		updates = append(updates, p)
	})
	require.NoError(t, client.Start(context.Background()))

	// Run the tool. The fake emits progress synchronously mid-call, so by the
	// time Run returns the handler has already received every update.
	result, err := client.Tools()[0].Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)

	// The final tool result is still returned alongside the progress stream.
	require.False(t, result.IsError)
	require.Equal(t, "ok", result.Content)

	// The client attached a non-empty progress token to the CallTool request,
	// and that exact token threaded back out on every progress notification.
	token, ok := remote.gotToken.(string)
	require.True(t, ok, "progress token was not a string")
	require.NotEmpty(t, token, "no progress token attached to the tool call")

	// Both mid-call updates reached the handler, in order, each carrying the
	// originating server, the round-tripped token, and the reported progress.
	require.Equal(t, []ToolProgress{
		{Server: "builder", Token: token, Progress: 1.0, Total: 3.0, Message: "compiling"},
		{Server: "builder", Token: token, Progress: 3.0, Total: 3.0, Message: "done"},
	}, updates)
}

func TestToolProgressDroppedWithoutHandler(t *testing.T) {
	remote := &fakeRemote{
		tools: []mcpsdk.Tool{{
			Name:           "build",
			RawInputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		progressUpdates: []mcpsdk.ProgressNotificationParams{{Progress: 1.0}},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	cfg := &config.Config{
		MCP:         []config.MCPServer{{Name: "builder", Transport: "stdio", Command: "server"}},
		Permissions: config.PermConfig{AllowAll: true},
	}
	client := NewClient(cfg, permission.New(cfg, nil), nil)
	require.NoError(t, client.Start(context.Background()))

	// With no handler installed, a mid-call progress notification is dropped and
	// the call still returns its result. The handler invocation must not panic.
	result, err := client.Tools()[0].Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Equal(t, "ok", result.Content)
}

func TestSubscribeUnknownServer(t *testing.T) {
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return &fakeRemote{}, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "filesystem", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	err := client.Subscribe(context.Background(), "missing:///tmp/a.txt")
	require.Error(t, err)

	err = client.Unsubscribe(context.Background(), "missing:///tmp/a.txt")
	require.Error(t, err)
}

func TestClientDiscoversPrompts(t *testing.T) {
	remote := &fakeRemote{
		prompts: []mcpsdk.Prompt{{
			Name:        "summarize",
			Description: "Summarize a document.",
			Arguments: []mcpsdk.PromptArgument{
				{Name: "doc", Description: "the document", Required: true},
				{Name: "tone", Description: "the tone"},
			},
		}},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "prompts", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	require.Equal(t, []Prompt{{
		Server:      "prompts",
		Name:        "summarize",
		Description: "Summarize a document.",
		Arguments: []PromptArgument{
			{Name: "doc", Description: "the document", Required: true},
			{Name: "tone", Description: "the tone", Required: false},
		},
	}}, client.Prompts())
}

func TestGetPrompt(t *testing.T) {
	remote := &fakeRemote{
		prompts: []mcpsdk.Prompt{{Name: "summarize", Description: "Summarize a document."}},
		promptOut: &mcpsdk.GetPromptResult{
			Description: "Summarize a document.",
			Messages: []mcpsdk.PromptMessage{
				{
					Role:    mcpsdk.RoleUser,
					Content: mcpsdk.TextContent{Type: "text", Text: "Please summarize: hello world"},
				},
				{
					Role:    mcpsdk.RoleAssistant,
					Content: mcpsdk.TextContent{Type: "text", Text: "On it."},
				},
			},
		},
	}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "prompts", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	messages, err := client.GetPrompt(
		context.Background(),
		"prompts",
		"summarize",
		map[string]string{"doc": "hello world"},
	)
	require.NoError(t, err)
	require.Equal(t, []PromptMessage{
		{Role: "user", Content: "Please summarize: hello world"},
		{Role: "assistant", Content: "On it."},
	}, messages)
	// The arguments reach the remote verbatim.
	require.Equal(t, map[string]string{"doc": "hello world"}, remote.promptArgs)
}

func TestPromptsAggregateAcrossServers(t *testing.T) {
	remotes := map[string]*fakeRemote{
		"alpha": {prompts: []mcpsdk.Prompt{{Name: "a_prompt", Description: "from alpha"}}},
		"beta":  {prompts: []mcpsdk.Prompt{{Name: "b_prompt", Description: "from beta"}}},
	}
	withFakeConnector(t, func(_ context.Context, cfg ServerConfig) (remoteClient, error) {
		return remotes[cfg.Name], nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{
			{Name: "alpha", Transport: "stdio", Command: "server"},
			{Name: "beta", Transport: "stdio", Command: "server"},
		},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	// Prompts surface in config order, each stamped with its origin server.
	require.Equal(t, []Prompt{
		{Server: "alpha", Name: "a_prompt", Description: "from alpha", Arguments: []PromptArgument{}},
		{Server: "beta", Name: "b_prompt", Description: "from beta", Arguments: []PromptArgument{}},
	}, client.Prompts())
}

func TestGetPromptUnknownServer(t *testing.T) {
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return &fakeRemote{}, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "prompts", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	_, err := client.GetPrompt(context.Background(), "missing", "summarize", nil)
	require.Error(t, err)
}

func TestSamplingHandlesServerRequest(t *testing.T) {
	remote := &fakeRemote{}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})

	var gotReq SamplingRequest
	var calls int
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	client.SetSampler(func(_ context.Context, req SamplingRequest) (SamplingResponse, error) {
		calls++
		gotReq = req
		return SamplingResponse{
			Content:    "sampled reply",
			Model:      "test-model",
			StopReason: "endTurn",
		}, nil
	})

	require.NoError(t, client.Start(context.Background()))

	// The client installed its sampling handler on the conn at connect time.
	require.NotNil(t, remote.sampling, "client did not install a sampling handler on the conn")

	// The server issues a sampling/createMessage request through that handler.
	// Parameters are round-tripped through JSON first so the message content
	// arrives as a decoded JSON object (map[string]any), exactly as it does over
	// the wire — not as already-typed Go content.
	params := mcpsdk.CreateMessageParams{
		SystemPrompt: "be terse",
		MaxTokens:    256,
		Temperature:  0.5,
		Messages: []mcpsdk.SamplingMessage{
			{Role: mcpsdk.RoleUser, Content: mcpsdk.NewTextContent("hello from server")},
			{Role: mcpsdk.RoleAssistant, Content: mcpsdk.NewTextContent("prior turn")},
		},
		ModelPreferences: &mcpsdk.ModelPreferences{
			Hints: []mcpsdk.ModelHint{{Name: "sonnet"}, {Name: "haiku"}},
		},
	}
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	var wireParams mcpsdk.CreateMessageParams
	require.NoError(t, json.Unmarshal(raw, &wireParams))
	// Sanity check that this exercises the wire shape, not typed Go content.
	require.IsType(t, map[string]any{}, wireParams.Messages[0].Content)

	serverReq := mcpsdk.CreateMessageRequest{
		Request:             mcpsdk.Request{Method: string(mcpsdk.MethodSamplingCreateMessage)},
		CreateMessageParams: wireParams,
	}
	result, err := remote.sampling.CreateMessage(context.Background(), serverReq)
	require.NoError(t, err)

	// The sampler was invoked once with the server's messages and parameters.
	require.Equal(t, 1, calls)
	require.Equal(t, []SamplingMessage{
		{Role: "user", Content: "hello from server"},
		{Role: "assistant", Content: "prior turn"},
	}, gotReq.Messages)
	require.Equal(t, "be terse", gotReq.SystemPrompt)
	require.Equal(t, 256, gotReq.MaxTokens)
	require.InDelta(t, 0.5, gotReq.Temperature, 1e-9)
	require.Equal(t, []string{"sonnet", "haiku"}, gotReq.ModelPreferences)

	// The sampled response is returned to the server in the SDK result.
	require.Equal(t, mcpsdk.RoleAssistant, result.Role)
	text, ok := mcpsdk.AsTextContent(result.Content)
	require.True(t, ok)
	require.Equal(t, "sampled reply", text.Text)
	require.Equal(t, "test-model", result.Model)
	require.Equal(t, "endTurn", result.StopReason)
}

func TestElicitationHandlesServerRequest(t *testing.T) {
	remote := &fakeRemote{}
	var connectCfg ServerConfig
	withFakeConnector(t, func(_ context.Context, cfg ServerConfig) (remoteClient, error) {
		connectCfg = cfg
		return remote, nil
	})

	var gotReq ElicitationRequest
	var calls int
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	client.SetElicitationHandler(func(_ context.Context, req ElicitationRequest) (ElicitationResponse, error) {
		calls++
		gotReq = req
		return ElicitationResponse{
			Action:  ElicitationAccept,
			Content: map[string]any{"name": "Ada", "age": float64(36)},
		}, nil
	})

	require.NoError(t, client.Start(context.Background()))

	// The elicitation capability is advertised at connect time via the injected
	// handler, and the client installed its handler on the conn.
	require.NotNil(t, connectCfg.Elicit, "elicitation handler not injected at connect time")
	require.NotNil(t, remote.elicitation, "client did not install an elicitation handler on the conn")

	// The server issues an elicitation/create request through that handler.
	// Parameters are round-tripped through JSON first so the requested schema
	// arrives as a decoded JSON object (map[string]any), exactly as it does over
	// the wire — not as an already-typed Go value.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	params := mcpsdk.ElicitationParams{
		Message:         "Please provide your name and age.",
		RequestedSchema: schema,
	}
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	var wireParams mcpsdk.ElicitationParams
	require.NoError(t, json.Unmarshal(raw, &wireParams))
	// Sanity check that this exercises the wire shape, not a typed Go value.
	require.IsType(t, map[string]any{}, wireParams.RequestedSchema)

	serverReq := mcpsdk.ElicitationRequest{
		Request: mcpsdk.Request{Method: string(mcpsdk.MethodElicitationCreate)},
		Params:  wireParams,
	}
	result, err := remote.elicitation.Elicit(context.Background(), serverReq)
	require.NoError(t, err)

	// The handler was invoked once with the server's prompt and schema. The
	// schema reaches the handler as the raw JSON the server sent, unchanged.
	require.Equal(t, 1, calls)
	require.Equal(t, "Please provide your name and age.", gotReq.Message)
	schemaJSON, err := json.Marshal(schema)
	require.NoError(t, err)
	require.JSONEq(t, string(schemaJSON), string(gotReq.Schema))

	// The user's response is returned to the server in the SDK result: the
	// accept action and the structured content, conforming to the schema.
	require.Equal(t, mcpsdk.ElicitationResponseActionAccept, result.Action)
	require.Equal(t, map[string]any{"name": "Ada", "age": float64(36)}, result.Content)
}

func TestElicitationNotInstalledWithoutHandler(t *testing.T) {
	remote := &fakeRemote{}
	var connectCfg ServerConfig
	withFakeConnector(t, func(_ context.Context, cfg ServerConfig) (remoteClient, error) {
		connectCfg = cfg
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	// Without a handler, none is injected at connect time and none is installed
	// on the conn, so the elicitation capability is never advertised.
	require.Nil(t, connectCfg.Elicit)
	require.Nil(t, remote.elicitation)
}

func TestElicitationHandlerDeclinePassesActionWithoutContent(t *testing.T) {
	h := &elicitationHandler{elicit: func(context.Context, ElicitationRequest) (ElicitationResponse, error) {
		// A decline carries no content even if some is set; content is only
		// meaningful on accept.
		return ElicitationResponse{
			Action:  ElicitationDecline,
			Content: map[string]any{"ignored": true},
		}, nil
	}}
	result, err := h.Elicit(context.Background(), mcpsdk.ElicitationRequest{})
	require.NoError(t, err)
	require.Equal(t, mcpsdk.ElicitationResponseActionDecline, result.Action)
	require.Nil(t, result.Content, "content returned to server on a non-accept action")
}

func TestElicitationHandlerPropagatesHandlerError(t *testing.T) {
	h := &elicitationHandler{elicit: func(context.Context, ElicitationRequest) (ElicitationResponse, error) {
		return ElicitationResponse{}, errors.New("user-facing UI unavailable")
	}}
	_, err := h.Elicit(context.Background(), mcpsdk.ElicitationRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user-facing UI unavailable")
}

func TestElicitationHandlerRequiresHandler(t *testing.T) {
	h := &elicitationHandler{}
	_, err := h.Elicit(context.Background(), mcpsdk.ElicitationRequest{})
	require.Error(t, err)
}

func TestRootsAnsweredFromConfiguredRoots(t *testing.T) {
	remote := &fakeRemote{}
	var connectCfg ServerConfig
	withFakeConnector(t, func(_ context.Context, cfg ServerConfig) (remoteClient, error) {
		connectCfg = cfg
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)

	// Roots set before Start are advertised at init: the connect config carries
	// the capability flag the connector turns into a declared roots capability.
	roots := []Root{
		{URI: "file:///workspace/api", Name: "api"},
		{URI: "file:///workspace/web", Name: "web"},
	}
	client.SetRoots(roots)
	require.NoError(t, client.Start(context.Background()))
	require.True(t, connectCfg.RootsEnabled, "roots capability not advertised at init")

	// The client installed its roots handler on the conn at connect time, and a
	// server-issued roots/list is answered with exactly the configured roots.
	got := remote.listRoots(t)
	require.Equal(t, []mcpsdk.Root{
		{URI: "file:///workspace/api", Name: "api"},
		{URI: "file:///workspace/web", Name: "web"},
	}, got)
}

func TestSetRootsUpdatesRootsAndNotifiesListChanged(t *testing.T) {
	remote := &fakeRemote{}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)

	client.SetRoots([]Root{{URI: "file:///workspace/old", Name: "old"}})
	require.NoError(t, client.Start(context.Background()))

	// The initial roots are what a server's roots/list returns. No list-changed
	// signal has reached the conn yet: the only SetRoots ran before connect.
	require.Equal(t, []mcpsdk.Root{{URI: "file:///workspace/old", Name: "old"}}, remote.listRoots(t))
	require.Empty(t, remote.rootsChangedMethods, "list-changed signaled before any post-connect SetRoots")

	// SetRoots replaces the roots and signals the connected server that the list
	// changed, via exactly one notifications/roots/list_changed, prompting it to
	// re-issue roots/list.
	client.SetRoots([]Root{
		{URI: "file:///workspace/new", Name: "new"},
		{URI: "file:///workspace/extra"},
	})
	require.Equal(t, []string{methodNotificationRootsListChanged}, remote.rootsChangedMethods,
		"SetRoots did not signal the connected server with the right notification")

	// A roots/list issued after the change is answered with the updated roots:
	// the handler reads the client's live roots, so no re-install was needed.
	require.Equal(t, []mcpsdk.Root{
		{URI: "file:///workspace/new", Name: "new"},
		{URI: "file:///workspace/extra"},
	}, remote.listRoots(t))
}

func TestRootsNotAdvertisedWithoutRoots(t *testing.T) {
	remote := &fakeRemote{}
	var connectCfg ServerConfig
	withFakeConnector(t, func(_ context.Context, cfg ServerConfig) (remoteClient, error) {
		connectCfg = cfg
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	// With no roots configured, the capability is not advertised, and a server's
	// roots/list (which the handler still answers) returns an empty list rather
	// than nil entries.
	require.False(t, connectCfg.RootsEnabled, "roots capability advertised without configured roots")
	require.Empty(t, remote.listRoots(t))
}

func TestClientCapabilitiesDeclaresRoots(t *testing.T) {
	// With roots enabled, the init capabilities declare roots with list-changed
	// support, telling the server it may issue roots/list and expect a
	// notifications/roots/list_changed when they change.
	caps := clientCapabilities(ServerConfig{RootsEnabled: true})
	require.NotNil(t, caps.Roots, "roots capability not declared when enabled")
	require.True(t, caps.Roots.ListChanged, "roots list-changed support not declared")

	// With roots disabled, the capability is absent so the server never expects
	// the client to answer roots/list.
	require.Nil(t, clientCapabilities(ServerConfig{}).Roots)
}

func TestSamplingNotInstalledWithoutSampler(t *testing.T) {
	remote := &fakeRemote{}
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return remote, nil
	})
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	// Without a sampler, no handler is installed on the conn.
	require.Nil(t, remote.sampling)
}

func TestSamplingHandlerPropagatesSamplerError(t *testing.T) {
	h := &samplingHandler{sample: func(context.Context, SamplingRequest) (SamplingResponse, error) {
		return SamplingResponse{}, errors.New("provider unavailable")
	}}
	_, err := h.CreateMessage(context.Background(), mcpsdk.CreateMessageRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider unavailable")
}

func TestSamplingHandlerRequiresSampler(t *testing.T) {
	h := &samplingHandler{}
	_, err := h.CreateMessage(context.Background(), mcpsdk.CreateMessageRequest{})
	require.Error(t, err)
}

func TestToolNameTruncation(t *testing.T) {
	name := joinedToolName("filesystem", "this_tool_name_is_long_enough_to_need_truncation_because_providers_limit_names")
	require.LessOrEqual(t, len([]rune(name)), maxToolNameRunes)
	require.Contains(t, name, "…")
	require.Equal(t, name, joinedToolName("filesystem", "this_tool_name_is_long_enough_to_need_truncation_because_providers_limit_names"))
}

// hangingRemote is a remote whose Close blocks until released, used to verify
// Stop does not hang and takes the force-kill fallback past its deadline.
type hangingRemote struct {
	fakeRemote
	release chan struct{}
	killed  atomic.Bool
}

func (h *hangingRemote) Close() error {
	<-h.release // Block past Stop's deadline.
	return nil
}

func (h *hangingRemote) forceKill() bool {
	h.killed.Store(true)
	return true
}

func TestStopForceKillsServerWhenCloseHangs(t *testing.T) {
	remote := &hangingRemote{release: make(chan struct{})}
	// Ensure the blocked Close goroutine can exit at test teardown.
	t.Cleanup(func() { close(remote.release) })

	server := &Server{
		name:   "stuck",
		state:  StateConnected,
		conn:   remote,
		logger: slog.Default(),
	}
	client := &Client{servers: []*Server{server}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	returned := make(chan error, 1)
	go func() {
		returned <- client.Stop(ctx)
	}()

	select {
	case err := <-returned:
		// Stop must return within the deadline (well under the 2s guard).
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung instead of force-killing the server past its deadline")
	}

	require.True(t, remote.killed.Load(), "Stop did not take the force-kill fallback")
	require.Equal(t, StateDisconnected, server.State())
}

func TestStopReturnsWhenCloseSucceeds(t *testing.T) {
	remote := &fakeRemote{}
	server := &Server{
		name:   "ok",
		state:  StateConnected,
		conn:   remote,
		logger: slog.Default(),
	}
	client := &Client{servers: []*Server{server}}

	require.NoError(t, client.Stop(context.Background()))
	require.Equal(t, 1, remote.closeCount)
	require.Equal(t, StateDisconnected, server.State())
}

func TestStartReportsConnectionFailureAsEvent(t *testing.T) {
	withFakeConnector(t, func(context.Context, ServerConfig) (remoteClient, error) {
		return nil, errors.New("dial failed")
	})
	bus := pubsub.NewTopic[Event]("mcp_test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()
	client := NewClient(&config.Config{
		MCP: []config.MCPServer{{Name: "server", Transport: "stdio", Command: "server"}},
	}, nil, bus)
	require.NoError(t, client.Start(context.Background()))
	require.Equal(t, StateFailed, client.Servers()[0].State())

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			return event.State == StateFailed && event.Err != nil
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}
