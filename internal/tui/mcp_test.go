package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestMCPStatusPanel_EmptyShowsGuidance(t *testing.T) {
	t.Parallel()

	require.Equal(t, "No MCP servers configured.", mcpStatusPanel(nil))
	require.Equal(t, "No MCP servers configured.", mcpStatusPanel([]mcpServerStatus{}))
}

func TestMCPStatusPanel_SortsAndFormats(t *testing.T) {
	t.Parallel()

	// Supplied out of order to prove the panel sorts by name for a stable view.
	body := mcpStatusPanel([]mcpServerStatus{
		{name: "weather", state: "connected", tools: 3, resources: 1, prompts: 2},
		{name: "github", state: "failed", tools: 0, resources: 0, prompts: 0},
	})
	lines := strings.Split(body, "\n")
	require.Len(t, lines, 2)
	require.Equal(t, "github — failed (0 tools, 0 resources, 0 prompts)", lines[0])
	require.Equal(t, "weather — connected (3 tools, 1 resources, 2 prompts)", lines[1])
}

func TestSlashMCP_NoClient_ShowsGuidance(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// testDeps leaves MCP nil, so the panel reports the empty state rather than
	// dereferencing a missing client.
	m.input.WriteString("/mcp")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.dialogs.Contains("mcp"), "/mcp must open the MCP panel dialog")
	require.Contains(t, plainText(m.dialogs.Render(200)), "No MCP servers configured.")
}
