package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsReadOnlyReadOnlyToolsReturnTrue verifies that the tools explicitly
// declared as read-only implement ReadOnlyTool and report true.
func TestIsReadOnlyReadOnlyToolsReturnTrue(t *testing.T) {
	deps := Dependencies{}
	readOnlyTools := []Tool{
		newViewTool(deps),
		newGrepTool(deps),
		newGlobTool(deps),
		newLSTool(deps),
		newDiagnosticsTool(deps),
		newSymbolsTool(deps),
		newNavigateTool(deps),
		newJobListTool(deps),
		newJobOutputTool(deps),
	}
	for _, tool := range readOnlyTools {
		t.Run(tool.Name(), func(t *testing.T) {
			require.True(t, IsReadOnly(tool),
				"expected %s to implement ReadOnlyTool and return true", tool.Name())
		})
	}
}

// TestIsReadOnlyWriteClassToolsReturnFalse verifies that write-class tools
// do NOT implement ReadOnlyTool (IsReadOnly returns false).
func TestIsReadOnlyWriteClassToolsReturnFalse(t *testing.T) {
	deps := Dependencies{}
	writeTools := []Tool{
		newEditTool(deps),
		newMultiEditTool(deps),
		newWriteTool(deps),
		newBashTool(deps),
	}
	for _, tool := range writeTools {
		t.Run(tool.Name(), func(t *testing.T) {
			require.False(t, IsReadOnly(tool),
				"expected %s to NOT implement ReadOnlyTool", tool.Name())
		})
	}
}

// TestIsReadOnlyFallbackForUnknownTool verifies that a plain Tool (no
// ReadOnlyTool implementation) reports false via the package helper.
func TestIsReadOnlyFallbackForUnknownTool(t *testing.T) {
	var plain Tool = &minimalTool{}
	require.False(t, IsReadOnly(plain))
}

// minimalTool satisfies Tool but not ReadOnlyTool, simulating a third-party
// or MCP-backed tool that predates the ReadOnlyTool interface.
type minimalTool struct{}

func (m *minimalTool) Name() string            { return "minimal" }
func (m *minimalTool) Description() string     { return "a minimal tool" }
func (m *minimalTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (m *minimalTool) Run(_ context.Context, _ json.RawMessage) (Result, error) {
	return Result{Content: "ok"}, nil
}
