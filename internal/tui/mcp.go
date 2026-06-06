package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// mcpServerStatus is a renderable snapshot of one configured MCP server: its
// name, connection state, and the number of tools, resources, and prompts it
// advertises. It decouples the /mcp panel rendering from the live *mcp.Client
// so the layout can be unit-tested without a running server.
type mcpServerStatus struct {
	name      string
	state     string
	tools     int
	resources int
	prompts   int
}

// handleMCP pushes a panel listing the configured MCP servers with their
// connection state and capability counts, mirroring how Claude Code, goose, and
// opencode let users inspect their MCP wiring without leaving the session.
func (m *model) handleMCP() (tea.Model, tea.Cmd) {
	m.dialogs.Push(&dialog.Text{DialogID: "mcp", Title: "MCP servers", Body: m.mcpPanel(), Theme: m.theme})
	return m, nil
}

// mcpPanel collects a status snapshot from the live MCP client and renders it.
// A nil client (no servers configured) yields the empty-state message.
func (m *model) mcpPanel() string {
	if m.deps.MCP == nil {
		return mcpStatusPanel(nil)
	}
	var servers []mcpServerStatus
	for _, s := range m.deps.MCP.Servers() {
		t, r, p := s.Counts()
		servers = append(servers, mcpServerStatus{
			name:      s.Name(),
			state:     s.State().String(),
			tools:     t,
			resources: r,
			prompts:   p,
		})
	}
	return mcpStatusPanel(servers)
}

// mcpStatusPanel renders the /mcp dialog body from server snapshots, one line
// per server sorted by name so the listing is stable across redraws. Each line
// reports the state and capability counts; an empty slice yields a guidance
// message so the panel never renders blank.
func mcpStatusPanel(servers []mcpServerStatus) string {
	if len(servers) == 0 {
		return "No MCP servers configured."
	}
	sorted := append([]mcpServerStatus(nil), servers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	lines := make([]string, 0, len(sorted))
	for _, s := range sorted {
		lines = append(lines, fmt.Sprintf("%s — %s (%d tools, %d resources, %d prompts)",
			s.name, s.state, s.tools, s.resources, s.prompts))
	}
	return strings.Join(lines, "\n")
}
