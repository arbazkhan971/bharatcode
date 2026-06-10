package agent

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/extension"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// TestCoordinatorFoldsExtensionTools asserts that a tool an extension registers
// on the host is folded into every agent's effective tool set, so the model can
// call it like any built-in tool.
func TestCoordinatorFoldsExtensionTools(t *testing.T) {
	host := extension.NewHost(nil)
	require.NoError(t, host.RegisterTool(&recordingTool{name: "ext_tool", result: "ok"}))

	providers := map[string]llm.Provider{
		"fake": &namedProvider{name: "fake", models: []llm.Model{{ID: "m1", Provider: "fake"}}},
	}
	coord, err := NewCoordinator(nil, Dependencies{
		Tools:      tools.NewRegistry(tools.Dependencies{}),
		Sessions:   testRepo(t),
		Providers:  providers,
		Extensions: host,
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	loop, err := coord.Agent("coder")
	require.NoError(t, err)

	tool, ok := loop.cfg.Tools.Get("ext_tool")
	require.True(t, ok, "extension tool must be reachable from the agent's tool set")
	require.Equal(t, "ext_tool", tool.Name())

	// The loop must also carry the dispatcher so lifecycle hooks reach the host.
	require.NotNil(t, loop.cfg.Extensions, "agent loop must receive the extension dispatcher")
}

// TestCoordinatorNilExtensionsLeavesDispatcherUnset asserts that with no host
// loaded the dispatcher is an untyped nil, preserving the Loop's no-extensions
// fast path.
func TestCoordinatorNilExtensionsLeavesDispatcherUnset(t *testing.T) {
	providers := map[string]llm.Provider{
		"fake": &namedProvider{name: "fake", models: []llm.Model{{ID: "m1", Provider: "fake"}}},
	}
	coord := newCoordinatorWithProviders(t, providers)
	loop, err := coord.Agent("coder")
	require.NoError(t, err)
	require.Nil(t, loop.cfg.Extensions, "no host loaded must leave the dispatcher nil")
}
