package tui

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestSlashCommand_Permissions_SetsModeLive is the CHANGE 3 contract test:
// issuing /permissions full must call through to the real Checker so its
// approval mode actually changes, and the status bar must reflect it.
func TestSlashCommand_Permissions_SetsModeLive(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Models: []config.Model{{ID: "kimi-k2", Provider: "moonshot"}},
		Agents: []config.Agent{{Name: "coder", Model: "kimi-k2"}},
		Ledger: config.LedgerConfig{MaxInrPerMonth: 100},
	}
	checker := permission.New(cfg, pubsub.NewTopic[pubsub.PermissionRequest]("perm_live_test", 16))
	require.Equal(t, permission.ApprovalAuto, checker.GetApprovalMode(), "default mode is auto")

	deps := testDeps()
	deps.Cfg = cfg
	deps.Permission = checker
	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.input.WriteString("/permissions full")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Equal(t, permission.ApprovalFull, checker.GetApprovalMode(), "/permissions full must change the real checker mode")
	require.Contains(t, m.status.Render(120), "full", "status bar must reflect the live mode")
	m.dialogs.Pop() // Dismiss the confirmation dialog so the next command reaches input.

	m.input.WriteString("/permissions read-only")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, permission.ApprovalReadOnly, checker.GetApprovalMode(), "/permissions read-only must change the checker")
	require.Contains(t, m.status.Render(120), "read-only")
	m.dialogs.Pop()

	// Bare /permissions reports the current mode without changing it.
	m.input.WriteString("/permissions")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, permission.ApprovalReadOnly, checker.GetApprovalMode(), "showing the mode must not change it")
	require.Contains(t, m.dialogs.Render(120), "read-only")
}

// TestSlashCommand_Permissions_Cycle advances the mode through the cycle.
func TestSlashCommand_Permissions_Cycle(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Models: []config.Model{{ID: "kimi-k2", Provider: "moonshot"}},
		Agents: []config.Agent{{Name: "coder", Model: "kimi-k2"}},
	}
	checker := permission.New(cfg, pubsub.NewTopic[pubsub.PermissionRequest]("perm_cycle_test", 16))

	deps := testDeps()
	deps.Cfg = cfg
	deps.Permission = checker
	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// auto -> full -> read-only -> auto.
	m.input.WriteString("/permissions cycle")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, permission.ApprovalFull, checker.GetApprovalMode())
	m.dialogs.Pop()

	m.input.WriteString("/permissions cycle")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, permission.ApprovalReadOnly, checker.GetApprovalMode())
	m.dialogs.Pop()

	m.input.WriteString("/permissions cycle")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, permission.ApprovalAuto, checker.GetApprovalMode())
}
