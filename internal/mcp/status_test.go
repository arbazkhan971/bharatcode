package mcp

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestStateString(t *testing.T) {
	t.Parallel()

	cases := map[State]string{
		StateDisconnected: "disconnected",
		StateConnecting:   "connecting",
		StateConnected:    "connected",
		StateFailed:       "failed",
		State(99):         "unknown",
	}
	for state, want := range cases {
		require.Equal(t, want, state.String())
	}
}

func TestServerCounts(t *testing.T) {
	t.Parallel()

	// A freshly constructed server advertises nothing until it connects.
	empty := &Server{name: "empty"}
	gotTools, gotResources, gotPrompts := empty.Counts()
	require.Equal(t, 0, gotTools)
	require.Equal(t, 0, gotResources)
	require.Equal(t, 0, gotPrompts)

	server := &Server{
		name:      "weather",
		tools:     make([]tools.Tool, 2),
		resources: make([]Resource, 1),
		prompts:   make([]Prompt, 3),
	}
	gotTools, gotResources, gotPrompts = server.Counts()
	require.Equal(t, 2, gotTools)
	require.Equal(t, 1, gotResources)
	require.Equal(t, 3, gotPrompts)
}
