// Package tui is the Bubble Tea v2 program for BharatCode's interactive
// terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/chat"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tuiledger "github.com/arbazkhan971/bharatcode/internal/tui/ledger"
	"github.com/arbazkhan971/bharatcode/internal/tui/notification"
	"github.com/arbazkhan971/bharatcode/internal/tui/statusbar"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
)

const (
	minWidth  = 80
	minHeight = 24
)

// Dependencies is the full set of services the TUI consumes.
type Dependencies struct {
	// Agent is the agent loop that processes user prompts.
	Agent *agent.Loop
	// Sessions is the session repository used for save and restore.
	Sessions *session.Repo
	// Cfg is the merged user and project configuration.
	Cfg *config.Config
	// Bus is the in-process agent event topic the TUI subscribes to.
	Bus *pubsub.Topic[agent.Event]
	// Permission is the tool-permission checker.
	Permission *permission.Checker
	// Ledger is the INR/USD cost ledger.
	Ledger *rootledger.Ledger
	// FileTracker reports per-session file changes.
	FileTracker *filetracker.Tracker
	// Logger is the slog logger the TUI uses for diagnostics.
	Logger *slog.Logger
}

// Run launches the TUI and blocks until the program exits.
func Run(ctx context.Context, deps Dependencies) error {
	if err := validateDependencies(deps); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	model := newModel(ctx, deps)
	program := tea.NewProgram(model, tea.WithContext(ctx))
	_, err := program.Run()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("running tui program: %w", err)
}

func validateDependencies(deps Dependencies) error {
	if deps.Agent == nil {
		return fmt.Errorf("validating tui dependencies: agent is nil")
	}
	if deps.Sessions == nil {
		return fmt.Errorf("validating tui dependencies: sessions is nil")
	}
	if deps.Cfg == nil {
		return fmt.Errorf("validating tui dependencies: config is nil")
	}
	if deps.Bus == nil {
		return fmt.Errorf("validating tui dependencies: bus is nil")
	}
	if deps.Permission == nil {
		return fmt.Errorf("validating tui dependencies: permission is nil")
	}
	if deps.Ledger == nil {
		return fmt.Errorf("validating tui dependencies: ledger is nil")
	}
	if deps.FileTracker == nil {
		return fmt.Errorf("validating tui dependencies: file tracker is nil")
	}
	if deps.Logger == nil {
		return fmt.Errorf("validating tui dependencies: logger is nil")
	}
	return nil
}

type focusState int

const (
	focusInput focusState = iota
	focusChat
)

type (
	tickMsg              time.Time
	ledgerSummaryMsg     rootledger.Summary
	permissionRequestMsg pubsub.PermissionRequest
)

type model struct {
	ctx              context.Context
	deps             Dependencies
	theme            styles.Theme
	chat             *chat.List
	dialogs          dialog.Stack
	footer           tuiledger.Footer
	status           statusbar.Bar
	notifications    *notification.FocusAware
	input            strings.Builder
	focus            focusState
	width            int
	height           int
	layout           layout
	startedAt        time.Time
	now              time.Time
	sessionID        string
	sessionPersisted bool
	goal             string
	helpVisible      bool
	quitting         bool

	// Agent run state.
	running     bool
	turn        int
	eventCh     <-chan agent.Event
	eventCancel func()

	// Autonomous goal-loop state (CHANGE 2).
	goalActive    bool
	goalIteration int
}

func newModel(ctx context.Context, deps Dependencies) *model {
	theme := styles.Default()
	now := time.Now()
	modelName, agentName := initialIdentity(deps.Cfg)
	sessionID := "new"
	footer := tuiledger.Footer{
		Theme:            theme,
		SessionID:        sessionID,
		MonthlyBudgetINR: deps.Cfg.Ledger.MaxInrPerMonth,
	}
	m := &model{
		ctx:           ctx,
		deps:          deps,
		theme:         theme,
		chat:          chat.New(),
		footer:        footer,
		status:        statusbar.Bar{Theme: theme, Model: modelName, Agent: agentName, SessionID: sessionID, StartedAt: now, Now: now},
		notifications: notification.NewFocusAware(notification.Noop{}),
		focus:         focusInput,
		width:         minWidth,
		height:        minHeight,
		startedAt:     now,
		now:           now,
		sessionID:     sessionID,
	}
	if deps.Permission != nil {
		m.applyApprovalMode(deps.Permission.GetApprovalMode())
	}
	return m
}

// Init starts lightweight subscriptions, the agent-event listen loop, and the
// uptime ticker.
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.waitLedger(), m.waitPermission(), m.ensureListening(), tick())
}

// Update applies one Bubble Tea message.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout = computeLayout(msg.Width, msg.Height)
		return m, nil
	case tea.FocusMsg:
		m.notifications.SetFocused(true)
		return m, nil
	case tea.BlurMsg:
		m.notifications.SetFocused(false)
		return m, nil
	case tickMsg:
		m.now = time.Time(msg)
		m.status.Now = m.now
		return m, tick()
	case ledgerSummaryMsg:
		m.footer.ApplySummary(rootledger.Summary(msg))
		return m, nil
	case permissionRequestMsg:
		m.dialogs.Push(&dialog.Permission{Theme: m.theme, Req: pubsub.PermissionRequest(msg)})
		return m, nil
	case runDoneMsg:
		return m.handleRunDone(msg)
	case agentEventMsg:
		return m.handleAgentEvent(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	default:
		return m, nil
	}
}

// View renders the current screen.
func (m *model) View() tea.View {
	view := tea.NewView(m.viewString())
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m *model) viewString() string {
	if m.width < minWidth || m.height < minHeight {
		return "terminal too small (need 80x24)"
	}

	body := m.renderMain()
	if m.dialogs.Len() > 0 {
		body += "\n" + m.dialogs.Render(m.width)
	}
	return body
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if top := m.dialogs.Top(); top != nil {
		_, pop := top.HandleKey(msg)
		if pop {
			m.dialogs.Pop()
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		if m.input.Len() == 0 {
			m.quitting = true
			return m, tea.Quit
		}
		m.deps.Agent.Interrupt()
		return m, nil
	case "tab":
		if m.focus == focusInput {
			m.focus = focusChat
		} else {
			m.focus = focusInput
		}
		return m, nil
	case "ctrl+p":
		m.pushModelPicker()
		return m, nil
	case "ctrl+a":
		m.dialogs.Push(&dialog.Text{DialogID: "agent_picker", Title: "Agents", Body: m.agentList(), Theme: m.theme})
		return m, nil
	case "ctrl+s":
		m.dialogs.Push(&dialog.Text{DialogID: "settings", Title: "Settings", Body: "No editable settings in this first pass.", Theme: m.theme})
		return m, nil
	case "ctrl+d":
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "No edit diff is available yet.", Theme: m.theme})
		return m, nil
	case "esc":
		m.helpVisible = false
		return m, nil
	case "enter":
		return m.submitInput()
	case "backspace":
		s := m.input.String()
		if s != "" {
			r := []rune(s)
			m.input.Reset()
			m.input.WriteString(string(r[:len(r)-1]))
		}
		return m, nil
	default:
		if msg.Key().Text != "" {
			m.input.WriteString(msg.Key().Text)
		}
		return m, nil
	}
}

func (m *model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.String())
	m.input.Reset()
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}
	return m.startRun(text)
}

func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	if text == "/goal" || strings.HasPrefix(text, "/goal ") {
		return m.handleGoalCommand(text)
	}
	if text == "/permissions" || strings.HasPrefix(text, "/permissions ") {
		return m.handlePermissionsCommand(text), nil
	}

	switch text {
	case "/help":
		m.helpVisible = true
	case "/clear":
		m.chat.Clear()
		m.helpVisible = false
	case "/sessions":
		m.dialogs.Push(&dialog.Text{DialogID: "sessions", Title: "Sessions", Body: "Session picker is ready for repository wiring.", Theme: m.theme})
	case "/model":
		m.pushModelPicker()
	case "/agent":
		m.dialogs.Push(&dialog.Text{DialogID: "agent_picker", Title: "Agents", Body: m.agentList(), Theme: m.theme})
	case "/budget":
		m.dialogs.Push(&dialog.Text{DialogID: "budget", Title: "Budget", Body: m.footer.Render(m.width), Theme: m.theme})
	case "/yolo":
		m.status.Yolo = !m.status.Yolo
		m.deps.Permission.SetYolo(m.status.Yolo)
	case "/save":
		m.dialogs.Push(&dialog.Text{DialogID: "save", Title: "Saved", Body: "Session save requested.", Theme: m.theme})
	case "/quit":
		m.quitting = true
		return m, tea.Quit
	default:
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Unknown command", Body: text, Theme: m.theme})
	}
	return m, nil
}

func (m *model) handleGoalCommand(text string) (tea.Model, tea.Cmd) {
	args := strings.TrimSpace(strings.TrimPrefix(text, "/goal"))
	switch {
	case args == "":
		if m.goalActive {
			return m.startGoal()
		}
		body := "No active goal."
		if m.goal != "" {
			body = m.goal
		}
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal", Body: body, Theme: m.theme})
		return m, nil
	case strings.EqualFold(args, "run"):
		return m.startGoal()
	case strings.EqualFold(args, "stop"):
		m.stopGoal()
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal stopped", Body: "Autonomous loop halted.", Theme: m.theme})
		return m, nil
	case strings.EqualFold(args, "clear"):
		m.stopGoal()
		m.goal = ""
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal cleared", Body: "No active goal.", Theme: m.theme})
		return m, nil
	default:
		m.goal = args
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal set", Body: m.goal, Theme: m.theme})
		return m, nil
	}
}

func (m *model) renderMain() string {
	header := m.theme.Header.Render("BharatCode")
	chatBody := m.chat.Render(max(1, m.layout.chat.W))
	if m.helpVisible {
		if chatBody != "" {
			chatBody += "\n\n"
		}
		chatBody += slashHelp()
	}
	input := "> " + m.input.String()
	if m.focus == focusInput {
		input += "▌"
	}

	parts := []string{
		header,
		clampHeight(chatBody, m.layout.chat.H),
		clampHeight(input, m.layout.input.H),
		m.status.Render(m.width),
		m.footer.Render(m.width),
	}
	return strings.Join(parts, "\n")
}

func (m *model) pushModelPicker() {
	var lines []string
	for _, model := range m.deps.Cfg.Models {
		lines = append(lines, model.Provider+"/"+model.ID)
	}
	if len(lines) == 0 {
		lines = append(lines, "No configured models")
	}
	m.dialogs.Push(&dialog.Text{DialogID: "model_picker", Title: "Models", Body: strings.Join(lines, "\n"), Theme: m.theme})
}

func (m *model) agentList() string {
	var lines []string
	for _, agent := range m.deps.Cfg.Agents {
		lines = append(lines, agent.Name)
	}
	if len(lines) == 0 {
		return "No configured agents"
	}
	return strings.Join(lines, "\n")
}

func (m *model) waitLedger() tea.Cmd {
	return func() tea.Msg {
		if m.deps.Ledger == nil {
			return nil
		}
		summary, err := m.deps.Ledger.Summary(m.ctx, m.sessionID, rootledger.WindowSession)
		if err != nil {
			if !errors.Is(err, context.Canceled) && m.deps.Logger != nil {
				m.deps.Logger.Debug("Ledger summary unavailable", "err", err)
			}
			return nil
		}
		return ledgerSummaryMsg(summary)
	}
}

func (m *model) waitPermission() tea.Cmd {
	return func() tea.Msg {
		events, cancel := pubsub.PermissionRequests.Subscribe()
		defer cancel()
		select {
		case <-m.ctx.Done():
			return nil
		case req, ok := <-events:
			if !ok {
				return nil
			}
			return permissionRequestMsg(req)
		}
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func initialIdentity(cfg *config.Config) (string, string) {
	if cfg == nil {
		return "unknown", "coder"
	}
	if len(cfg.Agents) > 0 {
		agent := cfg.Agents[0]
		model := agent.Model
		if model == "" && len(cfg.Models) > 0 {
			model = cfg.Models[0].ID
		}
		return emptyDefault(model, "unknown"), emptyDefault(agent.Name, "coder")
	}
	if len(cfg.Models) > 0 {
		return cfg.Models[0].ID, "coder"
	}
	return "unknown", "coder"
}

func emptyDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func slashHelp() string {
	return strings.Join([]string{
		"/help - list commands",
		"/clear - clear visible chat",
		"/sessions - open session picker",
		"/model - open model picker",
		"/agent - open agent picker",
		"/goal [text|run|stop|clear] - show, set, run, stop, or clear the goal",
		"/permissions [read-only|auto|full] - show or set approval mode",
		"/budget - show ledger and budget settings",
		"/yolo - toggle permission bypass",
		"/save - persist session",
		"/quit - exit",
	}, "\n")
}

func clampHeight(s string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[len(lines)-height:], "\n")
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

// runHeadlessForTest executes the program without terminal input or renderer.
func runHeadlessForTest(ctx context.Context, deps Dependencies, output io.Writer) error {
	if err := validateDependencies(deps); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p := tea.NewProgram(newModel(ctx, deps), tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output), tea.WithoutRenderer())
	_, err := p.Run()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("running tui program: %w", err)
}
