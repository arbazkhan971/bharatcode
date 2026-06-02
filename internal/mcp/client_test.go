package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type fakeRemote struct {
	tools       []mcpsdk.Tool
	resources   []mcpsdk.Resource
	resourceOut *mcpsdk.ReadResourceResult
	callOut     *mcpsdk.CallToolResult
	callCount   int
	closeCount  int
	lost        func(error)
}

func (f *fakeRemote) Close() error {
	f.closeCount++
	return nil
}

func (f *fakeRemote) CallTool(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	f.callCount++
	if f.callOut != nil {
		return f.callOut, nil
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{mcpsdk.TextContent{Type: "text", Text: "ok"}},
	}, nil
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

func TestToolNameTruncation(t *testing.T) {
	name := joinedToolName("filesystem", "this_tool_name_is_long_enough_to_need_truncation_because_providers_limit_names")
	require.LessOrEqual(t, len([]rune(name)), maxToolNameRunes)
	require.Contains(t, name, "…")
	require.Equal(t, name, joinedToolName("filesystem", "this_tool_name_is_long_enough_to_need_truncation_because_providers_limit_names"))
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
