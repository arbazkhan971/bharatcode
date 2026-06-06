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
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/message"
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

func TestSlashCommand_Keys_OpensShortcutDialog(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.input.WriteString("/keys")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.dialogs.Contains("keybindings"),
		"/keys must open the keybindings dialog")
	out := m.dialogs.Render(100)
	// The Ctrl-key shortcuts have no slash equivalent, so the /keys overlay is
	// the one place they are documented in-app; it must surface them.
	for _, key := range []string{"Ctrl+T", "Ctrl+P", "Ctrl+D", "Ctrl+F", "Ctrl+/", "Esc"} {
		require.Contains(t, out, key)
	}
}

func TestSlashCommand_Keys_FilterNarrowsOverlay(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.input.WriteString("/keys scroll")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.dialogs.Contains("keybindings"),
		"/keys with a filter must still open the keybindings dialog")
	out := m.dialogs.Render(100)
	// The filter echoes in the title and the listing is narrowed to the matching
	// rows, dropping unrelated shortcuts.
	require.Contains(t, out, "scroll")
	require.NotContains(t, out, "open the model picker")
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

// TestModelPicker_MarksActiveModel proves the model picker flags the model the
// session is currently using with the active marker and leaves the others with
// the aligning blank, so an open picker shows at a glance which model is in use
// rather than listing them all alike.
func TestModelPicker_MarksActiveModel(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.Cfg.Models = []config.Model{
		{ID: "kimi-k2", Provider: "moonshot"},
		{ID: "gpt-5", Provider: "openai"},
	}
	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	require.Equal(t, "kimi-k2", m.status.Model, "the first agent's model is the active one")
	m.pushModelPicker()
	body := m.dialogs.Render(100)

	require.Contains(t, body, "● moonshot/kimi-k2", "the active model row must carry the marker")
	require.Contains(t, body, "  openai/gpt-5", "an inactive model row must keep the aligning blank")
	require.NotContains(t, body, "● openai/gpt-5", "only the active model may be marked")
}

// TestAgentList_MarksActiveAgent proves the agent picker flags the session's
// active agent the same way the model picker does, so the two pickers orient the
// reader identically.
func TestAgentList_MarksActiveAgent(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.Cfg.Agents = []config.Agent{
		{Name: "coder", Model: "kimi-k2"},
		{Name: "reviewer", Model: "kimi-k2"},
	}
	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	require.Equal(t, "coder", m.status.Agent, "the first agent is the active one")
	body := m.agentList()

	require.Contains(t, body, "● coder", "the active agent row must carry the marker")
	require.Contains(t, body, "  reviewer", "an inactive agent row must keep the aligning blank")
	require.NotContains(t, body, "● reviewer", "only the active agent may be marked")
}

// TestScrollStatus_OnlyWhenScrolledUp asserts the scroll segment is empty at the
// bottom (the common case keeps the status bar unchanged) and reports the count
// of newer lines hidden below once the view is scrolled up, with singular/plural
// agreement.
func TestScrollStatus_OnlyWhenScrolledUp(t *testing.T) {
	t.Parallel()

	require.Empty(t, scrollStatus(0), "an anchored view must add no scroll segment")
	require.Empty(t, scrollStatus(-3), "a clamped-negative offset must add no segment")
	require.Equal(t, "↓ 1 line below", scrollStatus(1))
	require.Equal(t, "↓ 12 lines below", scrollStatus(12))
}

// TestRunningStatus_OnlyWhileTurnInFlight asserts the working segment is empty
// when no turn is running (zero start time), names the elapsed turn time once a
// turn begins, and advances its spinner glyph as seconds pass.
func TestRunningStatus_OnlyWhileTurnInFlight(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	require.Empty(t, runningStatus(time.Time{}, start, ""),
		"an idle prompt (zero start) must add no working segment")

	require.Equal(t, spinnerFrames[3]+" working 3s", runningStatus(start, start.Add(3*time.Second), ""),
		"a running turn must report its elapsed time")

	// A negative elapsed (clock skew) must clamp to the first frame, not panic.
	require.True(t, strings.HasPrefix(runningStatus(start, start.Add(-time.Second), ""), spinnerFrames[0]+" working "),
		"a negative elapsed must clamp to the first frame without panicking")

	// The spinner advances one frame per whole second and wraps at the end.
	first := runningStatus(start, start.Add(time.Second), "")
	tenth := runningStatus(start, start.Add(time.Duration(len(spinnerFrames))*time.Second), "")
	require.True(t, strings.HasPrefix(first, spinnerFrames[1]))
	require.True(t, strings.HasPrefix(tenth, spinnerFrames[0]), "the spinner must wrap around")
}

// TestCurrentActivity_TracksToolLifecycle asserts that handling agent events
// sets the status-bar activity to the running tool's name when a tool is called
// and clears it once the tool returns or the model produces fresh text, so the
// "working" segment names the active step only while a tool is in flight.
func TestCurrentActivity_TracksToolLifecycle(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Empty(t, m.currentActivity, "a fresh model must report no activity")

	m.handleAgentEvent(agentEventMsg{Kind: agent.EventToolCalled, ToolName: "Bash"})
	require.Equal(t, "Bash", m.currentActivity, "a called tool must become the activity")

	m.handleAgentEvent(agentEventMsg{Kind: agent.EventToolResult, ToolName: "Bash"})
	require.Empty(t, m.currentActivity, "a returned tool must clear the activity")

	m.handleAgentEvent(agentEventMsg{Kind: agent.EventToolCalled, ToolName: "Edit"})
	require.Equal(t, "Edit", m.currentActivity, "a second tool must replace the activity")

	// Fresh model text means the agent is thinking again between tools.
	m.handleAgentEvent(agentEventMsg{Kind: agent.EventLLMResponse})
	require.Empty(t, m.currentActivity, "model output must clear the activity")
}

// TestRunningStatus_NamesActiveTool asserts the working segment shows the name
// of the tool the agent is currently running, falling back to "working" when no
// tool is active, so a long turn reads as the step it is on rather than a bare
// "working".
func TestRunningStatus_NamesActiveTool(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0)
	require.Equal(t, spinnerFrames[3]+" Bash 3s", runningStatus(start, start.Add(3*time.Second), "Bash"),
		"a running tool must name itself in the working segment")
	require.Equal(t, spinnerFrames[3]+" working 3s", runningStatus(start, start.Add(3*time.Second), ""),
		"an empty activity must fall back to the generic working label")
	// The elapsed and spinner cadence are unchanged by the activity label.
	require.True(t, strings.HasPrefix(runningStatus(start, start.Add(time.Second), "Edit"), spinnerFrames[1]+" Edit "),
		"the spinner frame must still advance independently of the activity label")
}

// TestRunningStatus_SurfacesInStatusBar drives the rendered view: at rest the
// status bar shows no working segment, and once a turn is in flight it reports
// that the agent is working so the user has live progress feedback.
func TestRunningStatus_SurfacesInStatusBar(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	rendered := func() string { return stripANSI(m.renderMain()) }
	require.NotContains(t, rendered(), "working",
		"an idle prompt must show no working indicator")

	m.running = true
	m.turnStartedAt = m.now.Add(-5 * time.Second)
	require.Contains(t, rendered(), "working 5s",
		"a turn in flight must surface its elapsed working time")
}

// TestCtrlC_InterruptsRunningTurnInsteadOfQuitting locks the interrupt-first
// behavior: while a turn is in flight, Ctrl+C must stop the run rather than tear
// down the session, even though the prompt is empty (the usual state while
// watching the agent work). When idle, the empty-prompt Ctrl+C still quits.
func TestCtrlC_InterruptsRunningTurnInsteadOfQuitting(t *testing.T) {
	t.Parallel()

	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})

	// In flight with an empty prompt: Ctrl+C interrupts, not quits.
	m := newSizedModel(t)
	m.running = true
	require.Equal(t, 0, m.input.Len())
	_, _ = m.Update(ctrlC)
	require.False(t, m.quitting,
		"Ctrl+C during a run must interrupt the turn, not quit the app")

	// Idle with an empty prompt: Ctrl+C still quits, so the exit path is intact.
	idle := newSizedModel(t)
	require.False(t, idle.running)
	require.Equal(t, 0, idle.input.Len())
	_, cmd := idle.Update(ctrlC)
	require.True(t, idle.quitting,
		"Ctrl+C on an idle empty prompt must still quit")
	require.NotNil(t, cmd, "quitting must return a command")
}

// TestScrollStatus_SurfacesInStatusBar drives the rendered view: at rest the
// status bar shows no scroll segment, and after wheeling up into history it
// reports how many newer lines sit below, so a scrolled-up reader sees they are
// not viewing the latest output.
func TestScrollStatus_SurfacesInStatusBar(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	lines := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		lines = append(lines, uniqueLine(i))
	}
	appendMsg(m, "u1", message.RoleUser, strings.Join(lines, "\n"))

	rendered := func() string { return stripANSI(m.renderMain()) }
	require.NotContains(t, rendered(), "below",
		"a bottom-anchored view must show no scroll indicator")

	for i := 0; i < len(lines); i++ {
		_, _ = m.Update(wheel(tea.MouseWheelUp))
		if m.chatScroll > 0 {
			break
		}
	}
	require.Greater(t, m.chatScroll, 0, "wheel-up must scroll into history")
	require.Contains(t, rendered(), "below",
		"a scrolled-up view must report the newer lines hidden below")
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

func TestInputPlaceholder_ShownOnEmptyFocusedPrompt(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, focusInput, m.focus)
	require.Equal(t, 0, m.input.Len())
	require.Contains(t, m.renderMain(), "/keys for shortcuts")
}

func TestInputPlaceholder_HiddenOnceUserTypes(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.input.WriteString("h")
	require.NotContains(t, m.renderMain(), "/keys for shortcuts")
}

func TestInputPlaceholder_HiddenWhenChatFocused(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.focus = focusChat
	require.NotContains(t, m.renderMain(), "/keys for shortcuts")
}

// TestCtrlU_ClearsInputLine proves Ctrl+U wipes the whole prompt buffer in one
// stroke, the readline clear-line binding, rather than removing a single
// character the way Backspace does.
func TestCtrlU_ClearsInputLine(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "a long mistyped prompt")
	require.NotEqual(t, 0, m.input.Len())

	_, _ = m.Update(keyCtrl('u'))
	require.Equal(t, 0, m.input.Len(), "Ctrl+U must clear the entire prompt")
}

// TestCtrlU_EmptyBufferIsNoop proves Ctrl+U on an already-empty prompt is inert,
// so it never disturbs an idle input line.
func TestCtrlU_EmptyBufferIsNoop(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, 0, m.input.Len())

	_, _ = m.Update(keyCtrl('u'))
	require.Equal(t, 0, m.input.Len())
}

// TestCtrlU_ResetsRecallWalk proves clearing the line with Ctrl+U ends an active
// history recall, mirroring how editing the buffer with Backspace reseeds recall
// to the newest entry rather than leaving a stale cursor mid-walk.
func TestCtrlU_ResetsRecallWalk(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(2))
	m := h.model

	submitPrompt(t, h, "alpha")
	submitPrompt(t, h, "beta")

	_, _ = m.Update(keyUp())
	require.Equal(t, "beta", m.input.String(), "Up recalls the newest entry")

	_, _ = m.Update(keyCtrl('u'))
	require.Equal(t, 0, m.input.Len(), "Ctrl+U clears the recalled entry")

	// With recall reset, the next Up starts the walk over from the newest entry
	// rather than stepping past it to an older one.
	_, _ = m.Update(keyUp())
	require.Equal(t, "beta", m.input.String(), "recall restarts at the newest entry after Ctrl+U")
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

func TestFirstNonEmptyLine_SkipsBlankLeadingLines(t *testing.T) {
	t.Parallel()

	require.Equal(t, "real content", firstNonEmptyLine("\n  \n\treal content\nsecond"))
	require.Equal(t, "", firstNonEmptyLine("\n   \n\t\n"))
}

func TestFirstNonEmptyLine_TruncatesByRuneWithoutSplitting(t *testing.T) {
	t.Parallel()

	// A line of multi-byte runes whose byte length exceeds the cap but whose
	// rune count does not must pass through untouched, and a genuinely over-long
	// multi-byte line must be cut on a rune boundary into valid UTF-8 with an
	// ellipsis — never sliced mid-rune into a replacement character.
	short := strings.Repeat("é", 40) // 40 runes, 80 bytes
	require.Equal(t, short, firstNonEmptyLine(short))

	long := strings.Repeat("é", 80) // 80 runes, 160 bytes
	got := firstNonEmptyLine(long)
	require.True(t, utf8.ValidString(got), "truncation must produce valid UTF-8")
	require.True(t, strings.HasSuffix(got, "…"))
	require.Equal(t, 60, utf8.RuneCountInString(got), "result should be capped at the rune limit")
	require.NotContains(t, got, "�", "no rune should be split into the replacement character")
}
