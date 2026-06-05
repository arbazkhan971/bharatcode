package tui

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestRun_NilDependency_RejectsEarly(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	cases := map[string]func(*Dependencies){
		"agent":        func(d *Dependencies) { d.Agent = nil },
		"sessions":     func(d *Dependencies) { d.Sessions = nil },
		"config":       func(d *Dependencies) { d.Cfg = nil },
		"bus":          func(d *Dependencies) { d.Bus = nil },
		"permission":   func(d *Dependencies) { d.Permission = nil },
		"ledger":       func(d *Dependencies) { d.Ledger = nil },
		"file tracker": func(d *Dependencies) { d.FileTracker = nil },
		"logger":       func(d *Dependencies) { d.Logger = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			local := deps
			mutate(&local)
			require.Error(t, Run(context.Background(), local))
		})
	}
}

func TestRun_CancelledContext_RestoresTerminal(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	require.ErrorIs(t, runHeadlessForTest(ctx, testDeps(), &out), context.Canceled)
	require.Empty(t, out.String())
}

func TestPermissionDialog_BlocksInput(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	reply := make(chan pubsub.PermissionDecision, 1)
	m.dialogs.Push(&dialog.Permission{
		Theme: m.theme,
		Req: pubsub.PermissionRequest{
			Tool:   "bash",
			Reason: "Run command",
			Reply:  reply,
		},
	})
	_, _ = m.Update(keyText("x"))
	require.Empty(t, m.input.String())
	require.Equal(t, 1, m.dialogs.Len())

	_, _ = m.Update(keyText("y"))
	require.Equal(t, 0, m.dialogs.Len())
	require.True(t, (<-reply).Approved)
}

func TestLedgerFooter_UpdatesOnEvent(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(ledgerSummaryMsg(rootledger.Summary{
		SessionID:    "sess123456789",
		InputTokens:  44,
		OutputTokens: 55,
		CostUSD:      0.25,
		CostINR:      20.5,
	}))
	got := m.renderMain()
	require.Contains(t, got, "in 44")
	require.Contains(t, got, "out 55")
	require.Contains(t, got, "₹20.50")
}

func TestResize_RedrawsLayout(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.True(t, m.layout.validFor(120, 40))
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.True(t, m.layout.validFor(80, 24))
}

func TestMinimumSize_BelowFloor_GracefulFallback(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), testDeps())
	_, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	require.Equal(t, "terminal too small (need 80x24)", m.viewString())
}

func TestSlashCommand_Help_ListsAll(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.input.WriteString("/help")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	out := m.renderMain()
	for _, cmd := range []string{"/help", "/clear", "/sessions", "/model", "/agent", "/goal", "/budget", "/yolo", "/save", "/quit"} {
		require.Contains(t, out, cmd)
	}
}

func TestSlashCommand_Goal_ShowSetClear(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	m.input.WriteString("/goal")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.dialogs.Contains("goal"))
	require.Contains(t, m.dialogs.Render(100), "No active goal.")
	m.dialogs.Pop()

	m.input.WriteString("/goal ship slash commands")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, "ship slash commands", m.goal)
	require.Contains(t, m.dialogs.Render(100), "ship slash commands")
	m.dialogs.Pop()

	m.input.WriteString("/goal clear")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Empty(t, m.goal)
	require.Contains(t, m.dialogs.Render(100), "No active goal.")
}

func TestKeymap_CtrlP_OpensModelPicker(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	require.True(t, m.dialogs.Contains("model_picker"))
}

func TestStyles_NoHardcodedHex(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`"#[0-9a-fA-F]{3,6}"`)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			if path == "styles" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Falsef(t, re.Match(data), "hardcoded hex color in %s", path)
		return nil
	})
	require.NoError(t, err)
}

func newSizedModel(t *testing.T) *model {
	t.Helper()
	m := newModel(context.Background(), testDeps())
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func testDeps() Dependencies {
	cfg := &config.Config{
		Models: []config.Model{{ID: "kimi-k2", Provider: "moonshot"}},
		Agents: []config.Agent{{
			Name:  "coder",
			Model: "kimi-k2",
		}},
		Ledger: config.LedgerConfig{MaxInrPerMonth: 100},
	}
	coord, _ := agent.NewCoordinator(cfg, agent.Dependencies{})
	return Dependencies{
		Agent:       &agent.Loop{},
		Coordinator: coord,
		Sessions:    &session.Repo{},
		Cfg:         cfg,
		Bus:         pubsub.NewTopic[agent.Event]("test_agent", 256),
		Permission:  permission.New(cfg, pubsub.NewTopic[pubsub.PermissionRequest]("test_permission", 16)),
		Ledger:      &rootledger.Ledger{},
		FileTracker: &filetracker.Tracker{},
		Logger:      slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)),
	}
}

func keyText(text string) tea.KeyPressMsg {
	r := []rune(text)
	return tea.KeyPressMsg(tea.Key{Text: text, Code: r[0]})
}

func keySpecial(text string, code rune) tea.KeyPressMsg {
	_ = text
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func TestStatusbar_UptimeTickMonotonic(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	start := m.startedAt
	_, _ = m.Update(tickMsg(start.Add(time.Second)))
	first := m.status.Render(120)
	_, _ = m.Update(tickMsg(start.Add(2 * time.Second)))
	second := m.status.Render(120)
	require.Contains(t, first, "1s")
	require.Contains(t, second, "2s")
}
