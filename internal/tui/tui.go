// Package tui is the Bubble Tea v2 program for BharatCode's interactive
// terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/chat"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tuiledger "github.com/arbazkhan971/bharatcode/internal/tui/ledger"
	"github.com/arbazkhan971/bharatcode/internal/tui/notification"
	"github.com/arbazkhan971/bharatcode/internal/tui/statusbar"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/charmbracelet/bubbles/v2/help"
	"github.com/charmbracelet/bubbles/v2/key"
	"github.com/charmbracelet/bubbles/v2/list"
	"github.com/charmbracelet/bubbles/v2/spinner"
	"github.com/charmbracelet/bubbles/v2/textarea"
	"github.com/charmbracelet/bubbles/v2/viewport"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
)

const (
	minWidth  = 80
	minHeight = 24
)

// inputPlaceholder is the muted hint shown on an empty, focused prompt. It
// names the three things a newcomer is least likely to discover on their own —
// "/" for the command palette, "@" for file mentions, and "/keys" for the
// keyboard-shortcut listing — keeping the line short enough not to wrap on the
// minimum 80-column terminal.
const inputPlaceholder = "/ commands · @ files · /keys for shortcuts"

// Dependencies is the full set of services the TUI consumes.
type Dependencies struct {
	// Agent is the agent loop that processes user prompts.
	Agent *agent.Loop
	// Coordinator manages configured named agents and plan state.
	Coordinator *agent.Coordinator
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
	// Prompts is the optional custom-prompt registry backing registry-based
	// slash commands. It may be nil; the TUI loads a registry from the
	// configured prompt directories at startup when one is not supplied.
	Prompts *config.PromptRegistry
	// Recipes is the optional recipe registry backing /recipename slash
	// commands. It may be nil; the TUI loads a registry from the configured
	// recipe directories at startup when one is not supplied.
	Recipes *recipe.Registry
	// MCP is the optional Model Context Protocol client backing the /mcp
	// listing. It may be nil (no MCP servers configured), in which case /mcp
	// reports that none are connected.
	MCP *mcp.Client
	// InitialSessionID, when non-empty, causes the TUI to restore this session
	// at startup instead of beginning a fresh one. Wired by --continue / -c to
	// resume the most recently updated session for the current project.
	InitialSessionID string
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
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if shouldDisableRenderer() {
		opts = append(opts, tea.WithoutRenderer())
	}
	program := tea.NewProgram(model, opts...)
	_, err := program.Run()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("running tui program: %w", err)
}

// shouldDisableRenderer selects the quiet path for headless or CI-style runs.
// A real interactive terminal keeps the live renderer; explicit headless
// overrides, CI, and dumb terminals fall back to the non-rendering program
// mode so PTY captures do not accumulate redraw noise.
func shouldDisableRenderer() bool {
	switch {
	case envTruthy("BHARATCODE_HEADLESS"):
		return true
	case envTruthy("CI"):
		return true
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	return term == "" || term == "dumb"
}

func envTruthy(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch v {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func validateDependencies(deps Dependencies) error {
	if deps.Agent == nil {
		return fmt.Errorf("validating tui dependencies: agent is nil")
	}
	if deps.Coordinator == nil {
		return fmt.Errorf("validating tui dependencies: coordinator is nil")
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
	ctx       context.Context
	deps      Dependencies
	theme     styles.Theme
	themeName string
	chat      *chat.List
	// vp is the scrollable transcript container. The viewport is sized
	// (SetWidth/SetHeight) exclusively in Update's WindowSizeMsg handler so the
	// render path stays free of dimension mutations. Per-frame content (including
	// search highlights) is loaded via SetContent inside clampChat, which also
	// drives the scroll offset and returns m.vp.View() — replacing the old
	// hand-rolled strings.Split window that broke on ANSI-escaped lines.
	vp            viewport.Model
	dialogs       dialog.Stack
	footer        tuiledger.Footer
	status        statusbar.Bar
	notifications *notification.FocusAware
	input         strings.Builder
	// textInput is the bubbles textarea that renders the prompt (the "› " glyph,
	// placeholder, cursor, and word-wrap). The input buffer above remains the
	// canonical prompt text — driving history, undo/redo, recall, completion, and
	// reverse search — and is mirrored into textInput before each render via
	// syncPromptInput. The textarea is the view layer; the buffer is the model.
	textInput textarea.Model
	// keys is the global keymap surfaced in the footer help bar and matched in
	// handleKey via key.Matches.
	keys keyMap
	// help renders the muted footer help bar from keys.
	help help.Model
	// streamSpinner animates the MiniDot braille glyph while the agent is
	// producing a turn. It runs only while m.running so the per-frame tick loop
	// terminates cleanly at turn end.
	streamSpinner spinner.Model
	// modelPickerList is the bubbles/list used for the /model picker overlay. It
	// is (re)built when the picker opens and resized on every WindowSizeMsg.
	modelPickerList list.Model
	// sessionPickerList is the bubbles/list used for the /sessions picker
	// overlay. It is (re)built when the picker opens and resized on every
	// WindowSizeMsg.
	sessionPickerList list.Model
	inputHistory      inputState
	focus             focusState
	width             int
	height            int
	layout            layout
	startedAt         time.Time
	now               time.Time
	sessionID         string
	sessionPersisted  bool
	// tabFirstPrompt is the first user prompt submitted in the active tab, used
	// to title the tab in the /tabs listing. It mirrors the active tab's
	// firstPrompt field the way sessionID mirrors the tab's session (see
	// snapshotTab/loadTab); it is empty until the tab's first turn.
	tabFirstPrompt string
	goal           string
	quitting       bool

	// Agent run state.
	running bool
	// turnStartedAt marks when the in-flight turn began, so the status bar can
	// show how long the agent has been working. It is the zero time while idle.
	turnStartedAt time.Time
	// currentActivity names the tool the agent is currently running (e.g.
	// "Bash", "Edit"), so the status bar can show what the turn is doing rather
	// than a bare "working". It is set when a tool is called and cleared when the
	// tool returns (and at turn end), so an empty value means the agent is
	// thinking between tools.
	currentActivity string
	// turnToolCount is the number of tool calls made so far in the in-flight
	// turn. It is reset to zero at turn start and incremented on each
	// EventToolCalled, so the running status can show "[N]" when the agent has
	// made multiple calls — giving the user a sense of progress without having
	// to scroll up to count "[tool: ...]" lines in the chat.
	turnToolCount int
	// turnErrShown records that the in-flight turn already surfaced a run error
	// inline (via EventRunError) so handleRunDone does not re-report the same
	// failure as a dialog. It is reset at turn start and set when the error
	// event renders, letting handleRunDone cover only the error paths that
	// return without publishing an event (which would otherwise be silent).
	turnErrShown bool
	// lastTurnTokens is the formatted token-count segment for the most recently
	// completed turn (e.g. "1.2k in · 234 out"). It is cleared when a new turn
	// starts and set once the turn finishes, so the bar shows idle-turn stats
	// rather than stale counts from a previous run.
	lastTurnTokens string
	// lastContextPct is the context-window fill percentage (1–100) after the
	// most recently completed turn. Zero means no data yet; it is cleared when
	// a new turn starts and set once the turn finishes.
	lastContextPct int
	turn           int
	queueCounter   int
	// toolTurnSeq is a monotonic counter that gives every appended tool turn
	// (invocation or result) a unique chat-list id. A monotonic seq avoids
	// collisions when read-only tool calls run concurrently and their events
	// interleave, since each appended turn is its own discrete item.
	toolTurnSeq int
	eventCh     <-chan agent.Event
	eventCancel func()

	// Autonomous goal-loop state (CHANGE 2).
	goalActive    bool
	goalIteration int

	// Command palette state. paletteCursor is the highlighted row index within
	// the visible (filtered) command list; paletteFilter is the live
	// type-to-filter query narrowing the palette by command name or description.
	// paletteRecent holds the names of recently-executed palette commands,
	// most-recent first, capped at paletteMaxRecent; they bubble to the top of
	// the palette when no filter is active.
	paletteCursor int
	paletteFilter string
	paletteRecent []string

	// Session picker state. sessionCandidates holds the listed sessions while
	// the /sessions picker is open; sessionCursor is the highlighted row within
	// the currently visible (filtered) rows; sessionFilter is the live
	// type-to-filter query narrowing the list by title or id.
	sessionCandidates []session.Session
	sessionCursor     int
	sessionFilter     string

	// Model picker state. modelCandidates holds the configured models while the
	// /model picker is open; modelCursor is the highlighted row within the
	// visible (filtered) rows; modelFilter is the live type-to-filter query
	// narrowing the list by model ID or provider name.
	modelCandidates []config.Model
	modelCursor     int
	modelFilter     string

	// exportDir is the directory /export writes transcript files into. It is
	// empty by default, in which case exports land in the current working
	// directory (the workspace). Tests set it to a temp directory.
	exportDir string

	// copyToClipboard writes text to the system clipboard. It defaults to a
	// shell-out implementation (pbcopy/wl-copy/xclip/xsel) that degrades
	// gracefully when no utility is installed; tests inject a stub.
	copyToClipboard copyFunc

	// recipeCollector is the in-progress recipe parameter collector, if any.
	// It is non-nil while the user is answering parameter dialogs for a recipe
	// invocation. handleKey reads it after a recipeParamDialog pops to advance
	// the collection sequence.
	recipeCollector *recipeParamCollector

	// chatScroll is the number of lines the chat viewport is scrolled up from the
	// bottom. Zero anchors the view to the newest content (the default, unchanged
	// behavior); a positive value reveals older lines. The mouse wheel adjusts it.
	chatScroll int

	// chatMaxScroll is the furthest the chat can be scrolled up — the number of
	// lines hidden above the window when anchored to the very top — as computed by
	// clampChat for the most recent render. It lets the scroll indicator report a
	// reading position relative to the whole scrollback (e.g. "30% back") rather
	// than only the raw count of newer lines below. Zero when nothing is scrollable.
	chatMaxScroll int

	// search is the scrollback-search state for /search and the next/prev match
	// keys. Its zero value is inert (no term, no matches), so a model that has
	// never searched renders and scrolls exactly as before.
	search searchState

	// filetree is the togglable file-tree + diff side panel. It is hidden by
	// default, so the default render is unchanged.
	filetree filetree
	// workspaceRoot is the directory the file-tree panel enumerates. It defaults
	// to the process working directory; tests set it to a temp workspace.
	workspaceRoot string
	// editDiffSource, when non-nil, supplies the messages the file-tree panel
	// scans for per-file edit diffs. It defaults to nil, in which case the
	// persisted session messages are loaded (the same source as /diff). Tests
	// set it to return a fixed slice, mirroring the exportDir test seam.
	editDiffSource func() []message.Message

	// onboarding holds the first-run setup flow state. Its zero value is inert,
	// so a session that never onboards renders exactly as before. It is populated
	// by openOnboarding and cleared when setup finishes or is skipped.
	onboarding onboardingState
	// onboardingChecked guards the one-shot first-run check so onboarding is
	// offered at most once per session — on the first WindowSizeMsg, when a dialog
	// width is known — and never re-opens after the user dismisses or completes it.
	onboardingChecked bool

	// tabs holds the open session tabs. Each tab owns its own chat List and
	// session identity; the active tab's per-session fields are mirrored onto
	// the model above (m.chat, m.sessionID, ...) so the rest of the TUI reads
	// them unchanged. A default launch holds exactly one tab, in which case the
	// tab bar is hidden and behavior is identical to before the feature.
	tabs      []tab
	activeTab int
}

func newModel(ctx context.Context, deps Dependencies) *model {
	// Pick the theme from the terminal's actual background so assistant prose and
	// chrome render at full contrast on both light and dark terminals, rather than
	// always using the dark theme (whose light-grey body text washes out on a
	// light terminal).
	theme := styles.DefaultForBackground()
	now := time.Now()
	modelName, agentName := initialIdentity(deps.Cfg)
	sessionID := "new"
	footer := tuiledger.Footer{
		Theme:            theme,
		SessionID:        sessionID,
		MonthlyBudgetINR: deps.Cfg.Ledger.MaxInrPerMonth,
	}
	chatList := chat.New()
	// Render assistant markdown with syntax-highlighted code blocks. The glamour
	// style follows the active theme so light/dark stay consistent.
	chatList.EnableMarkdown(theme.Markdown)
	// Render edit/write tool turns as inline unified diffs (line numbers, red/green
	// tinting) through a theme-built viewer, matching the /diff command's look.
	chatList.EnableDiff(theme)

	// When --continue is used, pre-load the requested session so the TUI opens
	// with its history intact instead of starting blank. The load happens here
	// (before the tea program starts) to keep Init() pure and to surface any
	// load error via the chat pane rather than a startup failure.
	var (
		continueMsgs    []message.Message
		continueSession *session.Session
	)
	if deps.InitialSessionID != "" && deps.Sessions != nil {
		if sess, err := deps.Sessions.Get(ctx, deps.InitialSessionID); err == nil {
			if msgs, err := deps.Sessions.Messages(ctx, sess.ID); err == nil {
				continueSession = sess
				continueMsgs = msgs
				sessionID = sess.ID
				footer.SessionID = sess.ID
				if sess.Model != "" {
					modelName = sess.Model
				}
				if sess.Agent != "" {
					agentName = sess.Agent
				}
			}
		}
	}

	m := &model{
		ctx:               ctx,
		deps:              deps,
		theme:             theme,
		themeName:         theme.Name,
		chat:              chatList,
		vp:                viewport.New(viewport.WithWidth(minWidth), viewport.WithHeight(1)),
		footer:            footer,
		status:            statusbar.Bar{Theme: theme, Model: modelName, Agent: agentName, SessionID: sessionID, StartedAt: now, Now: now},
		notifications:     notification.NewFocusAware(notification.SystemNotifier{}),
		textInput:         newPromptInput(),
		keys:              defaultKeyMap(),
		help:              newHelpModel(),
		streamSpinner:     newStreamSpinner(),
		modelPickerList:   newModelPicker(deps.Cfg.Models, modelName),
		sessionPickerList: newSessionPicker(nil, sessionID, false, now),
		focus:             focusInput,
		width:             minWidth,
		height:            minHeight,
		startedAt:         now,
		now:               now,
		sessionID:         sessionID,
		workspaceRoot:     workingDir(),
		copyToClipboard:   systemClipboardCopy,
	}
	if continueSession != nil {
		m.sessionPersisted = true
		for _, msg := range continueMsgs {
			m.chat.Append(msg)
		}
	}
	if deps.Permission != nil {
		m.applyApprovalMode(deps.Permission.GetApprovalMode())
	}
	if m.deps.Prompts == nil {
		m.deps.Prompts = loadPromptRegistry(deps.Cfg)
	}
	if m.deps.Recipes == nil {
		m.deps.Recipes = loadRecipeRegistry(deps.Cfg)
	}
	// Surface the user's recipes and custom prompts in Tab completion, the slash
	// hint dropdown, and the did-you-mean suggester, so the /name commands /help
	// already documents are also completable as you type — not just discoverable
	// after the fact.
	m.inputHistory.setDynamicCommands(dynamicSlashNames(m.deps))
	m.inputHistory.setDynamicDescriptions(dynamicSlashDescriptions(m.deps))
	// Seed the single default tab from the freshly wired active state. With one
	// tab the tab bar stays hidden, so the default render is unchanged.
	m.initTabs()
	return m
}

// Init starts lightweight subscriptions, the agent-event listen loop, and the
// uptime ticker. The streaming spinner is NOT started here; it begins only when
// a turn is launched (startRun/continueRun batch its first Tick alongside the
// run command) so it does not leak a 12fps timer while the session is idle.
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
		// Resize the bubbles viewport to the chat pane. The tab bar borrows one
		// row when more than one tab is open; chatViewportHeight already accounts
		// for that, so use it as the definitive height.
		m.vp.SetWidth(m.layout.chat.W)
		m.vp.SetHeight(m.chatViewportHeight())
		// Resize the prompt textarea to the input column.
		if w := promptInputWidth(msg.Width); w > 0 {
			m.textInput.SetWidth(w)
		}
		// Keep the help bar bound to the terminal width.
		m.help.Width = msg.Width
		// Resize the bubbles list pickers so their modals fit the new size.
		lw, lh := pickerListSize(msg.Width, msg.Height)
		m.modelPickerList.SetSize(lw, lh)
		m.sessionPickerList.SetSize(lw, lh)
		// Offer first-run onboarding once a width is known so its dialog renders
		// correctly. A dialog pushed in newModel would render before any size
		// arrives; gating on the first WindowSizeMsg is the safe place. The check
		// runs at most once per session and is a no-op when setup already resolves.
		if !m.onboardingChecked {
			m.onboardingChecked = true
			m.maybeStartOnboarding()
		}
		return m, nil
	case spinner.TickMsg:
		// Forward tick to the streaming spinner only while a turn is in flight;
		// once idle, stepStreamSpinner returns nil so the per-frame loop ends.
		sp, cmd := stepStreamSpinner(m.streamSpinner, msg, m.running)
		m.streamSpinner = sp
		return m, cmd
	case startChatGPTLoginMsg:
		return m.handleStartChatGPTLogin()
	case chatgptLoginDoneMsg:
		return m.handleChatGPTLoginDone(msg)
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
	case tea.PasteMsg:
		// While onboarding is collecting an API key, route a bracketed paste into
		// the masked key buffer — pasting a key is the common case — stripping any
		// stray surrounding whitespace/newlines so a copied key is stored cleanly.
		if top := m.dialogs.Top(); top != nil && top.ID() == onboardingDialogID && m.onboarding.step == onboardingKeyEntry {
			m.onboarding.keyInput.WriteString(strings.TrimSpace(string(msg)))
			m.onboarding.errMsg = ""
			m.refreshOnboarding()
			return m, nil
		}
		// Bracketed-paste delivers the clipboard content as one PasteMsg.
		// Append it verbatim (preserving embedded newlines) so users can paste
		// multi-line text — code snippets, bullet lists, error traces — into
		// the prompt without losing structure. Mirrors Claude Code / goose.
		if m.focus == focusInput {
			s := string(msg)
			m.inputHistory.pushUndo(m.input.String())
			m.input.WriteString(s)
			m.inputHistory.resetRecall()
			m.inputHistory.resetCompletion()
		}
		return m, nil
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
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

	// Render any open dialog or picker as a centered overlay using lipgloss.Place
	// so it floats over the transcript at the correct terminal position. The
	// picker types (model_picker, sessions) use their bubbles/list rendering;
	// all others fall through to the dialog stack's plain-text render.
	if m.dialogs.Len() > 0 {
		top := m.dialogs.Top()
		switch {
		case top != nil && top.ID() == "model_picker" && len(m.modelCandidates) > 0:
			overlay := pickerModal(m.modelPickerList, "Model", m.width, m.height)
			body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		case top != nil && top.ID() == "sessions" && len(m.sessionCandidates) > 0:
			overlay := pickerModal(m.sessionPickerList, "Sessions", m.width, m.height)
			body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		default:
			// Center the dialog over the transcript using lipgloss.Place, the same
			// way the picker types do, so permission dialogs, error dialogs, and the
			// onboarding dialog float at the correct terminal position rather than
			// being appended below the transcript via raw "\n" concatenation.
			overlay := m.dialogs.Render(m.width)
			body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
	}
	return body
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if top := m.dialogs.Top(); top != nil {
		// The command palette carries selection state in the model, so it must
		// intercept navigation, filter-typing, and execution keys before the
		// generic dialog handler (which only dismisses on enter/esc).
		if top.ID() == "palette" {
			if consumed, cmd := m.handlePaletteKey(msg); consumed {
				return m, cmd
			}
		}
		// The session picker carries selection state in the model, so it must
		// intercept navigation and selection keys before the generic dialog
		// handler (which only dismisses on enter/esc).
		if top.ID() == "sessions" && len(m.sessionCandidates) > 0 {
			if consumed, cmd := m.handleSessionPickerKey(msg); consumed {
				return m, cmd
			}
		}
		// The model picker carries selection state in the model (modelCandidates,
		// modelCursor, modelFilter) and must intercept navigation and selection
		// keys before the generic dialog handler, mirroring the session picker.
		if top.ID() == "model_picker" && len(m.modelCandidates) > 0 {
			if consumed, cmd := m.handleModelPickerKey(msg); consumed {
				return m, cmd
			}
		}
		// The onboarding dialog carries its menu/key-entry state on the model and
		// must intercept navigation and entry keys before the generic dialog
		// handler, mirroring the model picker. An unconsumed key (esc on the menu)
		// falls through so the generic handler dismisses the dialog.
		if top.ID() == onboardingDialogID {
			if consumed, cmd := m.handleOnboardingKey(msg); consumed {
				return m, cmd
			}
		}
		_, pop := top.HandleKey(msg)
		if pop {
			popped := m.dialogs.Pop()
			// When a recipe parameter dialog is popped and there is an active
			// collector, advance it with the submitted value (or cancellation).
			if rpd, ok := popped.(*recipeParamDialog); ok && m.recipeCollector != nil {
				return m.recipeCollector.advanceFromDialog(m, rpd.param.Name, rpd.result, rpd.cancelled)
			}
		}
		return m, nil
	}

	// When the file-tree panel holds focus, it intercepts navigation and
	// selection keys before the input/chat handling below, so Up/Down move the
	// panel cursor instead of walking input history.
	if m.filetree.visible && m.filetree.focused {
		if consumed, cmd := m.handleFiletreeKey(msg); consumed {
			return m, cmd
		}
	}

	// While Ctrl+R reverse-history search is active, most keystrokes refine
	// the search query instead of editing the buffer directly.
	if m.inputHistory.histSearchActive {
		return m.handleHistSearchKey(msg)
	}

	// Global bindings that open a picker or panel are matched through the shared
	// keymap (the same bindings the footer help bar advertises) so the dispatch
	// and the help text read from one source. They are context-free — unlike
	// Enter/Tab/Ctrl+C below, whose behavior depends on focus and run state — so
	// matching them here keeps the string switch for the stateful keys.
	switch {
	case key.Matches(msg, m.keys.Palette):
		// The interactive command palette — a filterable, executable list of every
		// slash command. Always available; not blocked by the running state so a
		// user can open it mid-turn without interrupting the agent.
		return m.openCommandPalette()
	case key.Matches(msg, m.keys.Model):
		m.pushModelPicker()
		return m, nil
	case key.Matches(msg, m.keys.Diff):
		return m.handleDiff()
	case key.Matches(msg, m.keys.Files):
		m.filetree.toggle(m.workspaceRoot)
		return m, nil
	case key.Matches(msg, m.keys.NewTab):
		return m, m.newTab()
	case key.Matches(msg, m.keys.Help) && m.focus == focusChat:
		// Toggle the expanded footer help. It is gated on chat focus so "?" typed
		// at the prompt is still inserted as text; the full /keys overlay lists the
		// complete keymap regardless of focus.
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		// While a turn is in flight, Ctrl+C interrupts it rather than quitting, so a
		// user watching the agent work — who typically has an empty prompt — can stop
		// the run without tearing down the whole session by accident, matching how
		// Claude Code and opencode treat the interrupt key during a run. Only when
		// idle does Ctrl+C quit on an empty prompt; with text in the prompt it stays
		// an interrupt (a harmless no-op when nothing is running) as before.
		if m.running {
			m.deps.Agent.Interrupt()
			return m, nil
		}
		if m.input.Len() == 0 {
			m.quitting = true
			return m, tea.Quit
		}
		m.deps.Agent.Interrupt()
		return m, nil
	case "ctrl+r":
		// Enter (or re-enter) reverse history search, the classic readline
		// "bck-i-search:" mode. Subsequent keystrokes narrow the query; another
		// Ctrl+R steps to the next older match. Enter accepts; Esc/Ctrl+G cancels
		// and restores the buffer that was in place before the search began.
		if m.focus == focusInput {
			m.inputHistory.startHistSearch(m.input.String())
		}
		return m, nil
	case "tab":
		// On the input line, Tab completes/cycles a slash command when the
		// buffer is a slash prefix, or an @-file mention when the buffer ends with
		// one; otherwise it toggles focus to the chat.
		if m.focus == focusInput && strings.HasPrefix(m.input.String(), "/") {
			if completed, ok := m.inputHistory.completeSlash(m.input.String()); ok {
				m.setInput(completed)
			}
			return m, nil
		}
		if m.focus == focusInput {
			if _, ok := activeMention(m.input.String()); ok {
				if completed, ok := m.inputHistory.completeMention(m.input.String(), m.workspaceRoot); ok {
					m.setInput(completed)
				}
				return m, nil
			}
		}
		if m.focus == focusInput {
			m.focus = focusChat
		} else {
			m.focus = focusInput
		}
		return m, nil
	case "shift+tab":
		// Shift+Tab steps a slash-command or @-file completion cycle backward,
		// the reverse of Tab, so a user who overshoots the match they wanted can
		// step back instead of cycling the whole list around. Outside a completion
		// context it does nothing — Tab already toggles focus, and with only two
		// focuses a reverse toggle would be identical — so the key is reserved for
		// the one place a direction matters.
		if m.focus == focusInput && strings.HasPrefix(m.input.String(), "/") {
			if completed, ok := m.inputHistory.completeSlashPrev(m.input.String()); ok {
				m.setInput(completed)
			}
			return m, nil
		}
		if m.focus == focusInput {
			if _, ok := activeMention(m.input.String()); ok {
				if completed, ok := m.inputHistory.completeMentionPrev(m.input.String(), m.workspaceRoot); ok {
					m.setInput(completed)
				}
			}
		}
		return m, nil
	case "up":
		if m.focus == focusInput {
			if recalled, ok := m.inputHistory.recallPrev(m.input.String()); ok {
				m.setInputForRecall(recalled)
			}
		}
		return m, nil
	case "down":
		if m.focus == focusInput {
			if recalled, ok := m.inputHistory.recallNext(m.input.String()); ok {
				m.setInputForRecall(recalled)
			}
		}
		return m, nil
	case "ctrl+w":
		// Close the active tab (the last tab is kept).
		return m, m.closeTab()
	case "ctrl+tab", "ctrl+right":
		// Cycle to the next tab (wraps); no-op with a single tab.
		return m, m.nextTab()
	case "ctrl+shift+tab", "ctrl+left":
		// Cycle to the previous tab (wraps); no-op with a single tab.
		return m, m.prevTab()
	case "ctrl+a":
		m.dialogs.Push(&dialog.Text{DialogID: "agent_picker", Title: "Agents", Body: m.agentList(), Theme: m.theme})
		return m, nil
	case "ctrl+s":
		m.dialogs.Push(&dialog.Text{DialogID: "settings", Title: "Settings", Body: "No editable settings in this first pass.", Theme: m.theme})
		return m, nil
	case "shift+up":
		// Scroll the scrollback one line at a time, the finest keyboard step —
		// plain Up/Down are taken by prompt-history recall, so Shift pairs with
		// them for line scrolling the way a pager offers a single-line nudge
		// alongside its page keys. The offset is clamped at render.
		m.scrollChatLineUp()
		return m, nil
	case "shift+down":
		m.scrollChatLineDown()
		return m, nil
	case "pgup":
		// Page through the scrollback from the keyboard, mirroring the mouse
		// wheel. PageUp reveals an older page; the offset is clamped at render.
		m.scrollChatPageUp()
		return m, nil
	case "pgdown":
		m.scrollChatPageDown()
		return m, nil
	case "home":
		// Jump to the very top (oldest content) and bottom (newest), the way a
		// pager binds Home/End, so a long transcript is reachable without paging.
		m.scrollChatTop()
		return m, nil
	case "end":
		m.scrollChatBottom()
		return m, nil
	case "ctrl+/":
		// Advance to the next scrollback-search match. With no active search it
		// is inert, so the binding never disturbs an un-searched view.
		return m.searchNext(), nil
	case "ctrl+\\":
		// Step to the previous scrollback-search match (inert when no search is
		// active).
		return m.searchPrev(), nil
	case "esc":
		if m.filetree.visible {
			m.filetree.visible = false
			m.filetree.focused = false
			return m, nil
		}
		// Clear an active scrollback search so the viewport is unpinned and the
		// "search N/M" status segment disappears, the way an editor or pager
		// cancels its search on Esc.
		if m.search.active() {
			m.search.reset()
			m.status.Search = m.search.statusSegment()
		}
		return m, nil
	case "enter":
		return m.submitInput()
	case "alt+enter":
		// Alt+Enter inserts a literal newline so users can compose multi-line
		// prompts — useful for code blocks, numbered steps, or context that
		// benefits from structure. Mirrors Shift+Enter in Claude Code / goose.
		if m.focus == focusInput {
			m.inputHistory.pushUndo(m.input.String())
			m.input.WriteByte('\n')
			m.inputHistory.resetRecall()
			m.inputHistory.resetCompletion()
		}
		return m, nil
	case "ctrl+z":
		// Undo the most recent input edit, walking the buffer back one step at a
		// time. Ctrl+Z is the universal undo key in editors and CLIs (readline,
		// vim insert-mode, VS Code terminal), so it is the expected way to recover
		// from an accidental Ctrl+U, stray backspace, or mistyped burst. It is a
		// no-op when nothing has been edited yet and, like other edits, ends any
		// active recall walk and completion cycle.
		if restored, ok := m.inputHistory.undoInput(m.input.String()); ok {
			m.setInput(restored)
			m.inputHistory.resetRecall()
			m.inputHistory.resetCompletion()
		}
		return m, nil
	case "ctrl+y":
		// Redo reinstates the most recently undone edit, the standard counterpart
		// to Ctrl+Z. It is a no-op when there is nothing to redo (no prior undo,
		// or a new edit has already cleared the redo history).
		if restored, ok := m.inputHistory.redoInput(m.input.String()); ok {
			m.setInput(restored)
			m.inputHistory.resetRecall()
			m.inputHistory.resetCompletion()
		}
		return m, nil
	case "backspace":
		s := m.input.String()
		if s != "" {
			m.inputHistory.pushUndo(s)
			r := []rune(s)
			m.input.Reset()
			m.input.WriteString(string(r[:len(r)-1]))
		}
		// Editing the buffer cancels any recall walk and completion cycle.
		m.inputHistory.resetRecall()
		m.inputHistory.resetCompletion()
		return m, nil
	case "ctrl+u":
		// Clear the whole prompt in one stroke, the readline "delete to start of
		// line" binding. The input is an append-only buffer (no cursor), so there
		// is nothing before a cursor to spare — Ctrl+U wipes it entirely, sparing
		// the user from holding Backspace to discard a long mistyped prompt the way
		// Claude Code and opencode let one clear the line. It is a no-op on an empty
		// buffer and, like Backspace, cancels any recall walk and completion cycle.
		if m.input.Len() == 0 {
			return m, nil
		}
		m.inputHistory.pushUndo(m.input.String())
		m.input.Reset()
		m.inputHistory.resetRecall()
		m.inputHistory.resetCompletion()
		return m, nil
	case "alt+backspace", "ctrl+backspace":
		// Delete the trailing word, the readline "unix-word-rubout" edit that sits
		// between Backspace (one character) and Ctrl+U (the whole line) — so a user
		// can discard a single mistyped word without holding Backspace, matching the
		// word-delete editing Claude Code and opencode support at the prompt. The
		// append-only buffer has no cursor, so "word" means the last one. It is a
		// no-op on an empty buffer and, like the other edits, cancels any recall walk
		// and completion cycle.
		s := m.input.String()
		if s == "" {
			return m, nil
		}
		m.inputHistory.pushUndo(s)
		m.input.Reset()
		m.input.WriteString(deleteLastWord(s))
		m.inputHistory.resetRecall()
		m.inputHistory.resetCompletion()
		return m, nil
	default:
		if msg.Key().Text != "" {
			m.inputHistory.pushUndo(m.input.String())
			m.input.WriteString(msg.Key().Text)
			// Typing cancels any recall walk and completion cycle.
			m.inputHistory.resetRecall()
			m.inputHistory.resetCompletion()
		}
		return m, nil
	}
}

// handleHistSearchKey handles a keystroke while Ctrl+R reverse-history search
// is active. Printable characters refine the query; Backspace shrinks it;
// Ctrl+R steps to the next older match; Enter accepts the match; Esc/Ctrl+G
// cancel and restore the saved buffer.
func (m *model) handleHistSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+r":
		if match, ok := m.inputHistory.histSearchNext(); ok {
			m.setInput(match)
		}
	case "enter":
		m.inputHistory.commitHistSearch()
		return m.submitInput()
	case "esc", "ctrl+g":
		restored := m.inputHistory.cancelHistSearch()
		m.setInput(restored)
	case "backspace":
		if match, ok := m.inputHistory.histSearchBackspace(); ok {
			m.setInput(match)
		}
	default:
		if msg.Key().Text != "" {
			if match, ok := m.inputHistory.histSearchAcceptChar(msg.Key().Text); ok {
				m.setInput(match)
			}
		}
	}
	return m, nil
}

// renderInputArea renders the prompt input with "> " on the first line and
// "  " continuation indent on subsequent lines so a multi-line prompt aligns
// correctly. cursor (e.g. "▌") is appended at the end; pass "" when unfocused.
// A single-line input renders identically to the previous "> " + s + cursor
// code, so this is a pure extension.
func renderInputArea(s, cursor string) string {
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString("> ")
		} else {
			b.WriteByte('\n')
			b.WriteString("  ")
		}
		b.WriteString(line)
	}
	b.WriteString(cursor)
	return b.String()
}

// approxTokens returns a rough token estimate for s using the common
// 4-chars-per-token heuristic for mixed prose and code. The estimate is
// intentionally coarse — it helps the user gauge prompt size at a glance,
// not measure it precisely. A non-empty string always returns at least 1.
func approxTokens(s string) int {
	r := len([]rune(s))
	if r == 0 {
		return 0
	}
	t := r / 4
	if t == 0 {
		return 1
	}
	return t
}

// deleteLastWord removes the trailing word from an append-only prompt buffer:
// first any trailing whitespace, then the run of non-whitespace before it,
// leaving the whitespace that precedes that word in place (so "go test ./..."
// becomes "go test "). This is the readline "unix-word-rubout" edit bound to
// Ctrl+W / Alt+Backspace in a shell, the same word-at-a-time delete Claude Code
// and opencode offer at the prompt. A buffer with no word before the trailing
// whitespace — empty, or all spaces — deletes down to "".
func deleteLastWord(s string) string {
	trimmed := strings.TrimRight(s, " \t")
	if i := strings.LastIndexAny(trimmed, " \t"); i >= 0 {
		return trimmed[:i+1]
	}
	return ""
}

// setInput replaces the input buffer with s. It is used by Tab completion,
// which manages its own cycle state and so must not reset it here.
func (m *model) setInput(s string) {
	m.input.Reset()
	m.input.WriteString(s)
}

// setInputForRecall replaces the input buffer with a recalled history entry.
// Recall is a non-editing move, so it ends any in-progress completion cycle but
// leaves the recall cursor (managed by the caller) untouched.
func (m *model) setInputForRecall(s string) {
	m.setInput(s)
	m.inputHistory.resetCompletion()
}

func (m *model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.String())
	m.input.Reset()
	// Record the submission for Up/Down recall and reset navigation, even for
	// slash commands, mirroring shell history. record ignores blank text.
	m.inputHistory.record(text)
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}
	// A bare "exit" or "quit" typed at an idle prompt quits gracefully, matching
	// the muscle memory of a shell or REPL. It is gated on an idle prompt so the
	// words can still be sent verbatim to the agent mid-turn (where a user might
	// genuinely be asking about "exit codes"); the /exit and /quit slash forms
	// always quit regardless of run state.
	if !m.running {
		switch strings.ToLower(text) {
		case "exit", "quit":
			m.quitting = true
			return m, tea.Quit
		}
	}
	// While a turn is in flight, a plain message is queued as steering for the
	// running agent instead of starting a second concurrent Run (which would
	// panic on the loop's run mutex). It is delivered at the next safe boundary.
	if m.running {
		return m.steerRun(text)
	}
	return m.startRun(text)
}

// steerRun queues text as a steering message for the in-flight agent turn and
// surfaces it in the chat as a queued user message. If the run finished between
// the running check and Steer (a narrow race), Steer reports it was not queued
// and the text is started as a fresh turn instead.
func (m *model) steerRun(text string) (tea.Model, tea.Cmd) {
	if !m.deps.Agent.Steer(text) {
		return m.startRun(text)
	}
	m.queueCounter++
	id := fmt.Sprintf("%s-%d", queuedStreamPrefix, m.queueCounter)
	m.chat.Stream(id, queuedPrefix+text)
	m.chat.SetRole(id, message.RoleUser)
	m.chat.FinishStream(id)
	m.chat.Reindex(id)
	return m, nil
}

func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	if text == "/goal" || strings.HasPrefix(text, "/goal ") {
		return m.handleGoalCommand(text)
	}
	if text == "/permissions" || strings.HasPrefix(text, "/permissions ") {
		return m.handlePermissionsCommand(text), nil
	}
	if text == "/export" || strings.HasPrefix(text, "/export ") {
		return m.handleExport(text)
	}
	if text == "/share" || strings.HasPrefix(text, "/share ") {
		return m.handleExport(text)
	}
	if text == "/theme" || strings.HasPrefix(text, "/theme ") {
		return m.handleTheme(text), nil
	}
	if text == "/copy" || strings.HasPrefix(text, "/copy ") {
		return m.handleCopy(text)
	}
	if text == "/search" || strings.HasPrefix(text, "/search ") {
		return m.handleSearch(text)
	}
	if text == "/keys" || strings.HasPrefix(text, "/keys ") {
		return m.handleKeys(text), nil
	}
	if text == "/help" || strings.HasPrefix(text, "/help ") {
		return m.handleHelp(text), nil
	}
	if text == "/tab" || strings.HasPrefix(text, "/tab ") {
		return m.handleTabCommand(text)
	}
	if text == "/revert" || strings.HasPrefix(text, "/revert ") {
		return m.handleRevert(text)
	}
	if text == "/rename" || strings.HasPrefix(text, "/rename ") {
		return m.handleRename(text), nil
	}

	switch text {
	case "/tabs":
		return m.handleTabsList()
	case "/clear":
		m.chat.Clear()
		m.search.reset()
		m.chatScroll = 0
	case "/sessions":
		return m.openSessionPicker()
	case "/compact":
		return m.handleCompact()
	case "/fork":
		return m.handleFork()
	case "/diff":
		return m.handleDiff()
	case "/status":
		return m.handleStatus()
	case "/mcp":
		return m.handleMCP()
	case "/plan":
		return m.handlePlan()
	case "/approve":
		return m.handleApprove()
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
	case "/quit", "/exit":
		m.quitting = true
		return m, tea.Quit
	default:
		return m.handleUnknownSlash(text)
	}
	return m, nil
}

// handleKeys runs the /keys slash command, opening the keyboard-shortcut
// overlay. A bare "/keys" lists every shortcut; "/keys <filter>" narrows the
// overlay to the shortcuts whose key, description, or section title matches the
// filter, so a user hunting for one binding ("/keys tab", "/keys scroll") sees
// just the relevant rows instead of scanning the whole keymap — the way the
// Claude Code and opencode command palettes filter as you type. The active
// filter is echoed in the dialog title so it is clear the listing is a subset,
// and a filter matching nothing shows a quiet "no shortcuts match" note rather
// than an empty overlay.
func (m *model) handleKeys(text string) tea.Model {
	_, filter := splitSlash(text)
	title := "Keyboard shortcuts"
	if filter != "" {
		title += " · " + filter
	}
	m.dialogs.Push(&dialog.ScrollableText{
		DialogID: "keybindings",
		Title:    title,
		Body:     keybindingHelpBodyFiltered(filter),
		Theme:    m.theme,
		Height:   m.height,
	})
	return m
}

// handleHelp runs the /help slash command, opening the command-listing overlay
// as a scrollable dialog. A bare "/help" lists every command; "/help <filter>"
// narrows the overlay to the commands whose name or description contains the
// filter, so a user hunting for one command ("/help tab", "/help diff") sees
// just the relevant lines instead of scrolling the whole list — matching how
// "/keys [filter]" narrows the keyboard-shortcut overlay. The active filter is
// echoed in the dialog title so it is clear the listing is a subset, and a
// filter matching nothing shows a quiet "no commands match" note.
func (m *model) handleHelp(text string) tea.Model {
	_, filter := splitSlash(text)
	title := "Commands"
	if filter != "" {
		title += " · " + filter
	}
	m.dialogs.Push(&dialog.ScrollableText{
		DialogID: "help",
		Title:    title,
		Body:     m.slashHelpBodyFiltered(filter),
		Theme:    m.theme,
		Height:   m.height,
	})
	return m
}

// handleUnknownSlash resolves a slash command that is not built in. It first
// tries the prompt registry: a "/name rest" line whose name is registered is
// rendered with rest spliced into {{input}} and submitted to the agent. Next
// it tries the recipe registry: a "/name args" whose name matches a recipe
// collects parameters interactively (for user_prompt params) and then
// submits the rendered recipe to the agent. A name that is not in either
// registry falls back to the unknown-command dialog.
func (m *model) handleUnknownSlash(text string) (tea.Model, tea.Cmd) {
	name, args := splitSlash(text)
	if handled, model, cmd := m.handleRegistryPrompt(name, args); handled {
		return model, cmd
	}
	if handled, model, cmd := m.handleRegistryRecipe(name, args); handled {
		return model, cmd
	}
	body := text
	if s := suggestSlash(m.inputHistory.candidates(), name); s != "" {
		// Point a likely typo at its closest command so the user can fix it
		// without reopening /help, matching how git and the Claude Code /
		// opencode command palettes suggest the nearest command.
		body += "\n\nDid you mean " + s + "?"
	}
	m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Unknown command", Body: body, Theme: m.theme})
	return m, nil
}

// splitSlash splits a slash line "/name rest" into the command name (without
// the leading slash) and the trimmed remaining arguments.
func splitSlash(text string) (name string, args string) {
	trimmed := strings.TrimPrefix(text, "/")
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		return trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}
	return trimmed, ""
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

// handleTheme shows or switches the active TUI theme. With no argument it lists
// the available themes and the current selection; with a known theme name it
// switches live (chat, footer, status, and the glamour markdown style follow)
// and persists the choice on the model. An unknown name surfaces an error
// dialog without changing the theme.
func (m *model) handleTheme(text string) tea.Model {
	arg := strings.TrimSpace(strings.TrimPrefix(text, "/theme"))
	if arg == "" {
		m.dialogs.Push(&dialog.Text{DialogID: "theme", Title: "Theme", Body: m.themeListBody(), Theme: m.theme})
		return m
	}
	name := strings.ToLower(arg)
	if !m.applyTheme(name) {
		m.dialogs.Push(&dialog.Text{
			DialogID: "error",
			Title:    "Unknown theme",
			Body:     fmt.Sprintf("No theme named %q.\n\n%s", name, m.themeListBody()),
			Theme:    m.theme,
		})
		return m
	}
	// Push the confirmation AFTER applyTheme so the dialog adopts the new theme.
	m.dialogs.Push(&dialog.Text{
		DialogID: "theme",
		Title:    "Theme",
		Body:     "Switched to the " + name + " theme.",
		Theme:    m.theme,
	})
	return m
}

// themeListBody renders the selectable themes with a marker on the active one.
func (m *model) themeListBody() string {
	lines := make([]string, 0, len(styles.Names())+2)
	for _, name := range styles.Names() {
		marker := "  "
		if name == m.themeName {
			marker = "> "
		}
		lines = append(lines, marker+name)
	}
	lines = append(lines, "", "usage: /theme <name>")
	return strings.Join(lines, "\n")
}

// applyTheme switches the active theme to the one named name and propagates it
// to every component that holds its own theme copy: the footer, the status bar,
// and the chat markdown renderer (whose glamour style follows light/dark). It
// persists the selection on the model and reports whether name was a known
// theme; an unknown name leaves the current theme untouched.
func (m *model) applyTheme(name string) bool {
	theme, ok := styles.ByName(name)
	if !ok {
		return false
	}
	m.theme = theme
	m.themeName = theme.Name
	m.footer.Theme = theme
	m.status.Theme = theme
	// The glamour markdown style follows the theme; EnableMarkdown also resets
	// the chat render cache so already-shown messages re-render in the new style.
	m.chat.EnableMarkdown(theme.Markdown)
	// Re-point the diff viewer at the new theme so inline edit diffs re-tint to
	// match; like EnableMarkdown it resets the render cache.
	m.chat.EnableDiff(theme)
	return true
}

func (m *model) renderMain() string {
	// Header: BharatCode wordmark (saffron "Bharat" + green "Code") with a thin
	// tricolor accent rule underneath — the one tasteful brand moment on screen.
	// The rule is drawn at the full terminal width so it reads as a page-top
	// boundary rather than a floating element.
	wordmark := styles.Wordmark()
	triRule := styles.TricolorRule(m.width)
	header := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Header.Render(wordmark),
		triRule,
	)

	// When the side panel is visible, carve its column out of the chat width
	// here in the render rather than in computeLayout, so the persistent layout
	// rects still span the full width (keeping the resize invariant intact).
	chatW := m.layout.chat.W
	if m.filetree.visible {
		chatW = max(1, chatW-filetreeWidth-1)
	}
	chatBody := m.chat.Render(max(1, chatW))
	// Mark every search hit, the current one emphasized, so the reader sees what
	// matched. This addresses the same line space as the rendered body above
	// (chatW matches renderedChatBody), so it must run before the file-tree join
	// which shifts or wraps lines.
	chatBody = m.highlightMatches(chatBody)
	// Effective chat height: the layout's chat rows minus the rows the header's
	// tricolor rule and an optional tab bar borrow. The file-tree panel must be
	// rendered at THIS height (not the raw layout height) so its header isn't
	// clipped when the joined panel+chat is later clamped to the same height.
	tabBar := m.renderTabBar(m.width)
	chatH := m.layout.chat.H
	if tabBar != "" {
		chatH = max(0, chatH-1)
	}
	chatH = max(0, chatH-1) // header tricolor rule
	if m.filetree.visible {
		panel := m.renderFiletree(filetreeWidth, chatH)
		chatBody = joinPanels(panel, chatBody, filetreeWidth, chatH)
	}
	// Render the prompt through the bubbles textarea, mirroring the canonical
	// input buffer into it first. The textarea owns the "› " glyph, the muted
	// placeholder (shown on an empty focused buffer), the block cursor, and
	// word-wrap, replacing the hand-rolled renderInputArea + "▌" glyph.
	focused := m.focus == focusInput
	m.textInput = syncPromptInput(m.textInput, m.input.String(), focused, m.width)
	input := renderPromptInput(m.textInput)
	// Surface the slash-completion menu beneath the prompt so the commands Tab
	// would cycle through are discoverable without pressing it. It occupies one
	// of the input region's spare rows, so the layout height is unchanged, and
	// renders nothing for a non-slash buffer. The reverse history search hint
	// takes priority when Ctrl+R search is active, hiding the other menus.
	hinted := false
	if focused {
		if hint := m.inputHistory.histSearchHint(); hint != "" {
			input += "\n" + m.theme.Muted.Render(hint)
			hinted = true
		} else if hint := m.renderSlashHint(m.width); hint != "" {
			input += "\n" + hint
			hinted = true
		} else if hint := m.renderMentionHint(m.width); hint != "" {
			input += "\n" + hint
			hinted = true
		}
	}
	// Fill a spare input row with the muted footer help bar (bubbles/help) when no
	// completion hint is occupying it, so the core bindings stay visible without
	// adding a row to the rigid layout. A hint takes that row when active; the
	// /keys overlay always lists the full keymap regardless.
	if !hinted {
		if bar := renderHelpBar(m.help, m.keys, m.width); bar != "" {
			input += "\n" + bar
		}
	}
	// Wrap the textarea in the branded InputPanel bordered box: saffron rounded
	// border when focused, muted when blurred. The border + internal padding give
	// the prompt a raised, framed feel that makes the active input unmistakable.
	// Width is set to the full terminal width so the border spans the pane; the
	// textarea's own content width is set narrower (promptInputWidth) so the text
	// never sits against the frame. The layout.input.H=3 budget accommodates the
	// top border (1) + content (1) + bottom border (1).
	inputPanelStyle := styles.InputPanelBlurred.Width(m.width - 2)
	if focused {
		inputPanelStyle = styles.InputPanel.Width(m.width - 2)
	}
	inputPanel := inputPanelStyle.Render(input)

	spinnerView := ""
	if m.running {
		spinnerView = m.streamSpinner.View()
	}
	m.status.Working = runningStatus(m.turnStartedAt, m.now, m.currentActivity, m.turnToolCount, spinnerView)
	m.status.TurnTokens = m.lastTurnTokens
	m.status.ContextPct = m.lastContextPct
	m.status.Search = m.search.statusSegment()
	if n := approxTokens(m.input.String()); n > 0 {
		m.status.InputTokens = fmt.Sprintf("~%d tok", n)
	} else {
		m.status.InputTokens = ""
	}
	// The bordered input panel has a dynamic height (the slash/mention hint and
	// help-bar rows come and go), so its full height is measured here and the
	// transcript is shrunk to absorb the difference from the static budget. The
	// panel itself is rendered WHOLE — never clamped — because clamping would drop
	// its top border row and leave the box open at the top.
	inputH := lipgloss.Height(inputPanel)
	if extra := inputH - m.layout.input.H; extra > 0 {
		chatH = max(0, chatH-extra)
	}

	// clampChat pushes the transcript into the scrollable viewport and finalizes
	// m.chatScroll (clamping it to the scrollable range), so the scroll indicator
	// is computed from it afterwards to reflect the window actually shown. The
	// chat width sizes the viewport so over-long lines clip to the pane.
	chatView := m.clampChat(chatBody, chatW, chatH)
	m.status.Scroll = scrollStatus(m.chatScroll, m.chatMaxScroll)

	// Compose the screen top-to-bottom with lipgloss.JoinVertical so each zone
	// is a discrete block rather than a hand-joined string. Header (wordmark +
	// tricolor rule) and optional tab bar come first, then the scrollable
	// transcript, the bordered input panel, the status bar, and the footer row.
	// Drop the cost/budget footer segments when the active model is served by a
	// flat-rate subscription provider (ChatGPT login) — there every request is
	// billed to the plan, so "$0.00 · ₹0.00" is meaningless noise.
	m.footer.Subscription = m.activeModelIsSubscription()

	zones := []string{header}
	if tabBar != "" {
		zones = append(zones, tabBar)
	}
	zones = append(zones,
		chatView,
		inputPanel,
		m.status.Render(m.width),
		m.footer.Render(m.width),
	)
	return lipgloss.JoinVertical(lipgloss.Left, zones...)
}

// activeModelIsSubscription reports whether the active model's provider bills a
// flat-rate subscription (the ChatGPT-login providers) rather than per token.
func (m *model) activeModelIsSubscription() bool {
	if m.deps.Cfg == nil {
		return false
	}
	var providerName string
	for _, mod := range m.deps.Cfg.Models {
		if mod.ID == m.status.Model {
			providerName = mod.Provider
			break
		}
	}
	if providerName == "" {
		return false
	}
	for _, p := range m.deps.Cfg.Providers {
		if p.Name == providerName {
			return p.Type == config.ProviderChatGPT || p.Type == config.ProviderCodexOAuth
		}
	}
	return false
}

// clampChat loads the rendered transcript s into the viewport, applies the
// model's scroll intent (chatScroll lines up from the bottom), and returns
// m.vp.View() — the viewport's own ANSI-safe window into the content.
//
// Viewport sizing (SetWidth/SetHeight) is done exclusively in Update handlers
// (WindowSizeMsg) so this function never changes the viewport's dimensions:
// it only calls SetContent (content may be search-highlighted each frame) and
// SetYOffset/GotoBottom (to apply the scroll intent) before delegating the
// actual rendering to the viewport component. Replacing the old hand-rolled
// strings.Split slice with m.vp.View() is the key correctness fix: the
// viewport correctly counts ANSI-escaped wrapped lines whereas the byte-split
// approach undercounts them and corrupts the displayed window.
func (m *model) clampChat(s string, _, height int) string {
	if height <= 0 {
		m.chatScroll = 0
		m.chatMaxScroll = 0
		return ""
	}

	// Load the per-frame content (may include search highlights) into the
	// viewport. Width/height are already set in Update; this call only refreshes
	// the text, so the viewport's line accounting is current for this frame.
	m.vp.SetContent(s)

	lines := strings.Split(s, "\n")
	maxScroll := len(lines) - height
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.chatMaxScroll = maxScroll
	if m.chatScroll > maxScroll {
		m.chatScroll = maxScroll
	}
	if m.chatScroll < 0 {
		m.chatScroll = 0
	}

	// Drive the viewport from the clamped intent. chatScroll counts lines up from
	// the bottom, so the viewport's top offset is its complement. At the bottom we
	// call GotoBottom explicitly so the AtBottom contract holds and freshly
	// appended output is followed; otherwise pin the exact offset.
	if m.chatScroll == 0 {
		m.vp.GotoBottom()
	} else {
		m.vp.SetYOffset(maxScroll - m.chatScroll)
	}

	// When the whole transcript fits in the viewport (nothing to scroll), return
	// it at its NATURAL height rather than the viewport's full padded height, so
	// the input panel rises to sit just under the last message instead of leaving
	// a dead band of blank rows. Once content overflows, fall back to the
	// fixed-height scrollable viewport so scrolling and line accounting are exact.
	// Skip the shrink when the file-tree panel is visible: it is joined to the
	// chat body at the full pane height, so trimming to the transcript's natural
	// height would clip the panel.
	if maxScroll == 0 && !m.filetree.visible {
		return strings.TrimRight(s, "\n")
	}

	return m.vp.View()
}

// scrollStatus returns the status-bar segment describing scrollback position
// when the chat view is scrolled up from the newest output, e.g.
// "↓ 12 lines below · 30% back". scroll is m.chatScroll: the number of lines
// hidden below the window (0 when anchored to the bottom). maxScroll is
// m.chatMaxScroll: the furthest the view can be scrolled up, i.e. the total
// number of lines that can be hidden below once the user reaches the very top.
// It is empty at the bottom, so the segment appears only while the user is
// reading history and signals that newer lines exist below — the cue Claude Code
// and opencode give so a scrolled-up reader knows they are not viewing the
// latest output.
//
// The "N% back" suffix reports how far the view has scrolled into the history —
// scroll as a fraction of maxScroll — so the raw line count is contextualized
// against the whole scrollback the way a pager (less, vim) prints its position:
// "12 lines below" alone cannot tell a 12-of-20 view from a 12-of-5000 one. The
// percentage rounds to the nearest whole, never reading 0% while scrolled (a
// non-zero scroll is at least 1%) so the cue never implies the view is anchored
// when it is not, and is dropped when maxScroll is unknown (zero) so a degenerate
// state shows only the count.
func scrollStatus(scroll, maxScroll int) string {
	if scroll <= 0 {
		return ""
	}
	noun := "lines"
	if scroll == 1 {
		noun = "line"
	}
	seg := fmt.Sprintf("↓ %d %s below", scroll, noun)
	if maxScroll > 0 {
		pct := (scroll*100 + maxScroll/2) / maxScroll
		if pct < 1 {
			pct = 1
		}
		if pct > 100 {
			pct = 100
		}
		seg += fmt.Sprintf(" · %d%% back", pct)
	}
	return seg
}

// formatTurnTokens returns the compact status-bar segment shown after a turn
// completes, e.g. "1.2k in · 234 out". It summarises the provider-reported
// input and output token counts so the user can see context consumption at a
// glance. An empty string is returned when both counts are zero, so the segment
// is absent until real usage arrives.
func formatTurnTokens(inputTokens, outputTokens int) string {
	if inputTokens == 0 && outputTokens == 0 {
		return ""
	}
	return compactTokenCount(inputTokens) + " in · " + compactTokenCount(outputTokens) + " out"
}

// formatTurnCostUSD returns a compact dollar-cost string for the status bar
// (e.g. "$0.0045", "$0.032", "$1.23"). An empty string is returned when cost
// is zero so the segment is absent when no pricing is configured for the model.
func formatTurnCostUSD(usd float64) string {
	if usd <= 0 {
		return ""
	}
	switch {
	case usd < 0.01:
		return fmt.Sprintf("$%.4f", usd)
	case usd < 1:
		return fmt.Sprintf("$%.3f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}

// contextWindowForModel returns the context-window size (in tokens) for the
// named model from the config model list, or 0 when the model is not found
// (no context-window data available). The caller uses it to compute the fill
// percentage shown in the status bar so users can see how full the window is
// before the agent runs into ErrContextOverflow.
func contextWindowForModel(models []config.Model, modelID string) int {
	for _, m := range models {
		if m.ID == modelID {
			return m.ContextWindow
		}
	}
	return 0
}

// turnCostUSD computes the USD cost for one turn using the per-model pricing in
// the config. It returns 0 when the model is not found (no pricing configured).
func turnCostUSD(models []config.Model, modelID string, inputTokens, outputTokens int) float64 {
	for _, m := range models {
		if m.ID == modelID {
			return (float64(inputTokens)*m.InputPricePerMTokUSD +
				float64(outputTokens)*m.OutputPricePerMTokUSD) / 1_000_000
		}
	}
	return 0
}

// compactTokenCount formats n as a short string: the raw number when it fits
// in three digits, and a one-decimal "Nk" abbreviation for thousands, matching
// how Claude Code and opencode keep token counts compact in their status areas.
func compactTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	f := float64(n) / 1000.0
	if f < 10 {
		return fmt.Sprintf("%.1fk", f)
	}
	return fmt.Sprintf("%.0fk", f)
}

// interruptHintAfter is how long a turn must run before runningStatus appends
// the "ctrl+c to interrupt" hint. A short turn finishes before the reader could
// act on it, so the bar stays uncluttered; only a turn long enough that the user
// might actually want to stop it surfaces the key, the way Claude Code reveals
// its interrupt hint once a run is clearly in progress. Ten seconds keeps the
// hint off the bulk of quick tool calls while still appearing well before a
// stuck-looking turn would tempt the user to kill the whole session.
const interruptHintAfter = 10 * time.Second

// runningStatus returns the status-bar segment shown while a turn is in flight:
// the bubbles spinner view (spinnerView, pre-rendered from m.streamSpinner.View()),
// a label for what the agent is doing, the elapsed time, and — when the turn has
// invoked at least one tool — a "[N]" count of total tool calls so far, e.g.
// "⣾ working 3s" or "⣾ Bash 3s [2]". spinnerView is passed in so the function
// stays side-effect-free; the caller (renderMain) reads m.streamSpinner.View()
// while streaming and passes "" when idle. It returns "" when no turn is running
// (a zero start time) so the bar reverts to its idle form the moment the agent
// finishes. A negative elapsed (clock skew) is treated as zero.
//
// Once the turn has run past interruptHintAfter the segment gains a
// "(ctrl+c to interrupt)" hint, so a user watching a long run learns how to stop
// it without quitting the session — Ctrl+C interrupts a turn in flight rather
// than quitting (see the key handler) but nothing else advertises that.
func runningStatus(started, now time.Time, activity string, toolCount int, spinnerView string) string {
	if started.IsZero() {
		return ""
	}
	elapsed := now.Sub(started)
	if elapsed < 0 {
		elapsed = 0
	}
	label := activity
	if label == "" {
		label = "working"
	}
	frame := spinnerView
	seg := frame + " " + label + " " + util.HumanDuration(elapsed)
	if toolCount > 0 {
		seg += fmt.Sprintf(" [%d]", toolCount)
	}
	if elapsed >= interruptHintAfter {
		seg += " (ctrl+c to interrupt)"
	}
	return seg
}

func (m *model) pushModelPicker() {
	if len(m.deps.Cfg.Models) == 0 {
		m.dialogs.Push(&dialog.Text{DialogID: "model_picker", Title: "Models", Body: "No configured models.", Theme: m.theme})
		return
	}
	m.modelCandidates = m.deps.Cfg.Models
	m.modelCursor = 0
	m.modelFilter = ""
	// Seed the cursor at the currently active model so the picker opens with
	// the selection already on the row the user is using, mirroring the session
	// picker which starts at the top but marks the current session.
	for i, mod := range m.modelCandidates {
		label := mod.Provider + "/" + mod.ID
		if m.status.Model == mod.ID || m.status.Model == label {
			m.modelCursor = i
			break
		}
	}
	// Rebuild the bubbles/list picker for the current models and terminal size.
	// The dialog stack entry is still pushed for the text-body fallback; the
	// overlay renderer in viewString uses the list when modelCandidates is set.
	lw, lh := pickerListSize(m.width, m.height)
	m.modelPickerList = newModelPicker(m.deps.Cfg.Models, m.status.Model)
	m.modelPickerList.SetSize(lw, lh)
	m.dialogs.Push(&dialog.Text{
		DialogID: "model_picker",
		Title:    "Models",
		Body:     m.modelPickerBody(),
		Theme:    m.theme,
	})
}

func (m *model) agentList() string {
	var lines []string
	for _, agent := range m.deps.Cfg.Agents {
		lines = append(lines, activeMarker(m.status.Agent == agent.Name)+agent.Name)
	}
	if len(lines) == 0 {
		return "No configured agents"
	}
	return strings.Join(lines, "\n")
}

// activeMarker prefixes a model- or agent-picker row: a filled dot for the entry
// matching the session's active selection so the open picker shows which model
// or agent is currently in use, and an aligning two-space blank for the rest so
// every row's name still starts at the same column. It mirrors how Claude Code
// and opencode flag the current choice in their pickers, turning the otherwise
// flat list into one the reader can orient in at a glance.
func activeMarker(active bool) string {
	if active {
		return "● "
	}
	return "  "
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
	agentName := "coder"
	if len(cfg.Agents) > 0 {
		agentName = emptyDefault(cfg.Agents[0].Name, "coder")
	}
	// When the user is signed in with ChatGPT, default a fresh session to their
	// ChatGPT model rather than the config's default agent model. Otherwise the
	// default (e.g. deepseek, which needs an API key the user hasn't set) makes
	// first-run onboarding fire on every start even though they are logged in.
	if chatgptModel := chatgptModelIfSignedIn(cfg); chatgptModel != "" {
		return chatgptModel, agentName
	}
	if len(cfg.Agents) > 0 {
		agent := cfg.Agents[0]
		model := agent.Model
		if model == "" && len(cfg.Models) > 0 {
			model = cfg.Models[0].ID
		}
		return emptyDefault(model, "unknown"), agentName
	}
	if len(cfg.Models) > 0 {
		return cfg.Models[0].ID, "coder"
	}
	return "unknown", "coder"
}

// chatgptModelIfSignedIn returns the first configured model on a chatgpt-type
// provider when a "Sign in with ChatGPT" session exists, or "" otherwise. It is
// used to default a fresh session to the ChatGPT model once the user has logged
// in, so they are not re-prompted to pick a model on every start.
func chatgptModelIfSignedIn(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if _, err := llm.ChatGPTStatus(); err != nil {
		return ""
	}
	chatgptProviders := make(map[string]bool)
	for _, p := range cfg.Providers {
		if p.Type == config.ProviderChatGPT {
			chatgptProviders[p.Name] = true
		}
	}
	for _, mod := range cfg.Models {
		if chatgptProviders[mod.Provider] {
			return mod.ID
		}
	}
	return ""
}

func emptyDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// dynamicSlashNames collects the runtime slash-command names contributed by the
// recipe and custom-prompt registries, each as a leading-slash name. Recipes
// come first, then prompts, matching the order slashHelpLines prints them, so
// Tab completion and the hint dropdown list them the same way /help does. It
// backs setDynamicCommands; nil registries contribute nothing.
func dynamicSlashNames(deps Dependencies) []string {
	var names []string
	if deps.Recipes != nil {
		for _, e := range deps.Recipes.List() {
			names = append(names, "/"+e.Name)
		}
	}
	if deps.Prompts != nil {
		for _, p := range deps.Prompts.List() {
			names = append(names, "/"+p.Name)
		}
	}
	return names
}

// dynamicSlashDescriptions collects a terse one-line gloss for each
// runtime-contributed slash command, keyed by its "/name". A recipe uses its
// title (falling back to its description); a custom prompt uses its frontmatter
// description (falling back to the first non-empty line of its template), the
// same sources slashHelpLines documents — but without the argument hint so the
// gloss stays short enough for the one-row completion menu. Commands with no
// usable text are omitted so the menu never appends a bare "— ". It backs
// setDynamicDescriptions; nil registries contribute nothing.
func dynamicSlashDescriptions(deps Dependencies) map[string]string {
	desc := make(map[string]string)
	if deps.Recipes != nil {
		for _, e := range deps.Recipes.List() {
			gloss := e.Title
			if gloss == "" {
				gloss = e.Description
			}
			if gloss != "" {
				desc["/"+e.Name] = gloss
			}
		}
	}
	if deps.Prompts != nil {
		for _, p := range deps.Prompts.List() {
			gloss := p.Description
			if gloss == "" {
				gloss = firstNonEmptyLine(p.Template)
			}
			if gloss != "" {
				desc["/"+p.Name] = gloss
			}
		}
	}
	return desc
}

func (m *model) slashHelpLines() []string {
	lines := []string{
		"/help - list commands",
		"/keys [filter] - show keyboard shortcuts, optionally narrowed to a filter",
		"/clear - clear visible chat",
		"/sessions - restore a recent session",
		"/tab [new|next|prev|close|N] - open or switch session tabs (Ctrl+T new, Ctrl+Right/Left switch)",
		"/tabs - list open tabs",
		"/compact - summarize older turns to shrink context",
		"/fork - branch the current session",
		"/rename <new title> - rename the current session",
		"/diff - show the latest edit diff",
		"/revert [apply|force] - undo this session's file changes (preview first, then apply)",
		"/export [md|html] - write the session transcript to a file",
		"/copy [last|all] - copy the last assistant reply or the whole chat to the clipboard",
		"/search <term> - find a term in the chat; Ctrl+/ next match, Ctrl+\\ previous",
		"/status - show model, session, and spend",
		"/mcp - list MCP servers with their connection state and capability counts",
		"/plan - restrict the agent to read-only tools and propose a plan",
		"/approve - exit plan mode and re-enable execution tools",
		"/model - open model picker (switch to a provider you have a key for)",
		"/agent - open agent picker",
		"/goal [text|run|stop|clear] - show, set, run, stop, or clear the goal",
		"/permissions [read-only|auto|full] - show or set approval mode",
		"/budget - show ledger and budget settings",
		"/theme [dark|light|high-contrast] - show or switch the color theme",
		"/yolo - toggle permission bypass",
		"/save - persist session",
		"/quit - exit (or just type 'exit')",
		"/exit - exit (alias for /quit)",
		"setup: set a provider API key env var (e.g. ANTHROPIC_API_KEY) or run 'bharatcode login <provider> --token ...', then /model to select it",
	}
	// Append registered recipes so the help listing stays self-documenting as
	// new recipes are dropped into the recipe directories.
	if m.deps.Recipes != nil {
		for _, e := range m.deps.Recipes.List() {
			title := e.Title
			if title == "" {
				title = e.Description
			}
			lines = append(lines, "/"+e.Name+" - "+title)
		}
	}
	// Append registered custom prompts (the pi-style /name slash commands) so
	// they are as discoverable as built-ins and recipes. The frontmatter
	// description and argument hint, when present, document each command; with
	// no description we fall back to the first line of the template.
	if m.deps.Prompts != nil {
		for _, p := range m.deps.Prompts.List() {
			label := "/" + p.Name
			if p.ArgumentHint != "" {
				label += " " + p.ArgumentHint
			}
			desc := p.Description
			if desc == "" {
				desc = firstNonEmptyLine(p.Template)
			}
			if desc != "" {
				label += " - " + desc
			}
			lines = append(lines, label)
		}
	}
	return lines
}

// slashHelpBodyFiltered returns the /help overlay body, optionally narrowed to
// lines whose text (command name and description taken together) contains filter.
// An empty or whitespace-only filter returns the full listing unchanged. A
// filter matching nothing returns a quiet "no commands match" note. A successful
// filter leads with an "M of N commands match …" count so the user can see how
// much of the listing the filter kept, mirroring keybindingHelpBodyFiltered.
//
// The filter is split on whitespace into terms that must ALL appear (case-
// insensitive) in a line, so a two-word query like "tab next" narrows to only
// the commands that mention both words. When no line matches by substring, the
// filter falls back to subsequence matching so run-together abbreviations
// ("diffrevert") still find their command rather than returning an empty note.
func (m *model) slashHelpBodyFiltered(filter string) string {
	all := m.slashHelpLines()
	if strings.TrimSpace(filter) == "" {
		return strings.Join(all, "\n")
	}
	matched := filterHelpLines(all, filter)
	if len(matched) == 0 {
		return fmt.Sprintf("No commands match %q.", strings.TrimSpace(filter))
	}
	noun := "commands"
	if len(matched) == 1 {
		noun = "command"
	}
	header := fmt.Sprintf("%d of %d %s match %q", len(matched), len(all), noun, strings.TrimSpace(filter))
	return header + "\n\n" + strings.Join(matched, "\n")
}

// filterHelpLines returns the lines from lines whose lower-cased text contains
// every term in filter (split on whitespace, AND logic, case-insensitive). When
// no lines match by substring, the filter falls back to isSubsequence on the
// collapsed (no-space) query, matching how filterKeybindingGroups degrades to a
// fuzzy pass rather than returning nothing.
func filterHelpLines(lines []string, filter string) []string {
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		return lines
	}
	terms := strings.Fields(q)
	var out []string
	for _, line := range lines {
		low := strings.ToLower(line)
		ok := true
		for _, t := range terms {
			if !strings.Contains(low, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, line)
		}
	}
	if len(out) > 0 {
		return out
	}
	// Subsequence fallback: join the query terms into one run so a compact
	// abbreviation ("diffrevert") matches the same lines as spaced words would.
	token := strings.Join(terms, "")
	for _, line := range lines {
		if isSubsequence(token, strings.ToLower(line)) {
			out = append(out, line)
		}
	}
	return out
}

// keyBinding is one row in the /keys overlay: a key (or key combo) and what it
// does. Rows are grouped into keyGroups so related shortcuts read together.
type keyBinding struct {
	key  string
	desc string
}

// keyGroup is a titled cluster of related shortcuts in the /keys overlay.
type keyGroup struct {
	title    string
	bindings []keyBinding
}

// keybindingGroups is the source of truth for the /keys overlay, grouped the way
// Claude Code and opencode cluster their shortcuts — navigation, tabs, panels —
// so a reader scans by category instead of one long flat list. The rows mirror
// the bindings handled in handleKey, so the two stay in step.
var keybindingGroups = []keyGroup{
	{title: "Navigation", bindings: []keyBinding{
		{"Tab", "switch focus, or complete a /command or @file"},
		{"Shift+Tab", "cycle the /command or @file menu backward"},
		{"Up/Down", "recall previous prompts"},
		{"Ctrl+R", "search prompt history backward (bck-i-search)"},
		{"Shift+Up/Down", "scroll the chat one line at a time"},
		{"PgUp/PgDn", "scroll the chat a page at a time"},
		{"Home/End", "jump to the oldest/newest message"},
	}},
	{title: "Prompt", bindings: []keyBinding{
		{"Enter", "send the prompt"},
		{"Alt+Enter", "add a new line (multi-line prompt)"},
		{"Backspace", "delete the character before the cursor"},
		{"Alt+Backspace", "delete the last word (also Ctrl+Backspace)"},
		{"Ctrl+U", "clear the whole prompt line"},
		{"Ctrl+Z", "undo the last input edit"},
		{"Ctrl+Y", "redo the last undone edit"},
	}},
	{title: "Tabs", bindings: []keyBinding{
		{"Ctrl+T", "new tab"},
		{"Ctrl+W", "close tab"},
		{"Ctrl+←/→", "switch to the previous/next tab (also Ctrl+Shift+Tab/Ctrl+Tab)"},
	}},
	{title: "Panels & pickers", bindings: []keyBinding{
		{"Ctrl+K", "open the command palette (filterable list of all commands)"},
		{"Ctrl+P", "open the model picker"},
		{"Ctrl+A", "open the agent picker"},
		{"Ctrl+S", "open settings"},
		{"Ctrl+D", "show the latest edit diff"},
		{"Ctrl+F", "toggle the file-tree panel"},
		{"/ (in panel)", "filter the file-tree listing"},
		{"Enter (in panel)", "insert the selected file as an @mention"},
		{"j/k (in dialog)", "move one line in a dialog or overlay (vim-style)"},
		{"g/G (in dialog)", "jump to top / bottom of a dialog or overlay"},
	}},
	{title: "Search", bindings: []keyBinding{
		{"Ctrl+/", "jump to the next search match"},
		{"Ctrl+\\", "jump to the previous search match"},
	}},
	{title: "Session", bindings: []keyBinding{
		{"Ctrl+C", "interrupt the running turn, or quit on an empty idle prompt"},
		{"Esc", "close a panel or dialog, clear the search, or hide help"},
	}},
}

// keybindingHelpBody renders the global key shortcuts shown by /keys. The
// slash-command listing in /help documents only commands typed at the prompt,
// leaving the Ctrl-key bindings — which have no slash equivalent — otherwise
// undiscoverable without reading the source. /keys collects them in one
// overlay, grouped under section headers the way Claude Code and opencode print
// their shortcuts, so a user can learn the full keymap from a single place. It
// lives in a dialog rather than the chat help dump because the dialog is not
// height-clamped to the chat viewport, leaving room for the whole keymap.
//
// The key column is padded to a single width shared across every group so each
// description starts at the same column regardless of which section it sits in,
// keeping the overlay aligned. Width is measured in runes so a multi-byte key
// glyph (the "←/→" arrows) lines up with the rest.
func keybindingHelpBody() string {
	return renderKeyGroups(keybindingGroups)
}

// renderKeyGroups formats the given shortcut groups as the /keys overlay body:
// each group's title on its own line above its indented binding rows, with the
// key column padded to a width shared across every group so descriptions align
// regardless of section (measured in runes so the multi-byte "←/→" arrows line
// up). Groups are separated by a blank line and no trailing blank line is left.
// Pulling this out of keybindingHelpBody lets the full and filtered overlays
// render identically from whichever subset of groups they show.
func renderKeyGroups(groups []keyGroup) string {
	keyWidth := 0
	for _, g := range groups {
		for _, b := range g.bindings {
			if n := len([]rune(b.key)); n > keyWidth {
				keyWidth = n
			}
		}
	}

	var b strings.Builder
	for gi, g := range groups {
		if gi > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(g.title)
		b.WriteByte('\n')
		for _, kb := range g.bindings {
			pad := strings.Repeat(" ", keyWidth-len([]rune(kb.key)))
			b.WriteString("  " + kb.key + pad + "  " + kb.desc + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// keybindingHelpBodyFiltered renders the /keys overlay narrowed to the shortcuts
// matching filter (see filterKeybindingGroups). An empty or whitespace-only
// filter renders the full overlay unchanged. When the filter matches nothing it
// returns a quiet "no shortcuts match" note rather than an empty body, so the
// overlay always explains itself.
//
// A successful filter leads with a one-line "M of N shortcuts match …" count so
// the user can see how much of the keymap the filter kept — the search-result
// count Claude Code and opencode show above a narrowed list — turning an
// otherwise-silent narrowing into a measured one. The count sits on its own line
// above a blank separator, so it reads as a header rather than a binding row and
// the groups below stay aligned.
func keybindingHelpBodyFiltered(filter string) string {
	if strings.TrimSpace(filter) == "" {
		return keybindingHelpBody()
	}
	groups := filterKeybindingGroups(filter)
	if len(groups) == 0 {
		return fmt.Sprintf("No shortcuts match %q.", strings.TrimSpace(filter))
	}
	matched, total := countBindings(groups), countBindings(keybindingGroups)
	noun := "shortcuts"
	if matched == 1 {
		noun = "shortcut"
	}
	header := fmt.Sprintf("%d of %d %s match %q", matched, total, noun, strings.TrimSpace(filter))
	return header + "\n\n" + renderKeyGroups(groups)
}

// countBindings totals the number of binding rows across the given shortcut
// groups, so the filtered overlay can report how many shortcuts survived a
// filter against the full keymap. It counts rows, not groups, since a group is a
// heading rather than a shortcut the user can press.
func countBindings(groups []keyGroup) int {
	n := 0
	for _, g := range groups {
		n += len(g.bindings)
	}
	return n
}

// filterKeybindingGroups returns the keyGroups whose bindings match filter, a
// case-insensitive query split on whitespace into terms that must ALL match — a
// binding is kept only when every term is a substring of its title, key, or
// description. The AND-of-terms rule lets a query name a binding from two angles
// at once ("tab switch" finds the tab-switching shortcut even though no single
// run of text contains both words), the way Claude Code and opencode narrow a
// shortcut search as you add words, while a single-term query behaves exactly as
// a plain substring filter did. A group whose title matches a term satisfies
// that term for all of its bindings, so "/keys tabs" still surfaces the whole
// Tabs section; a group with no surviving binding is dropped. An empty or
// whitespace-only filter returns every group unchanged.
//
// When the substring pass finds nothing, the filter falls back to a fuzzy
// subsequence match (see fuzzyFilterKeybindingGroups), so a run-together or
// abbreviated query — "swtab" for "switch … tab", "prevmatch" for the
// previous-match key — still surfaces its binding rather than the bare "no
// shortcuts match" note. This mirrors the subsequence fallback the slash-command
// menu uses (matchSlash), keeping fuzzy discovery consistent across the TUI's
// filters. Substring matches always win when present, so a precise query is
// never broadened.
func filterKeybindingGroups(filter string) []keyGroup {
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		return keybindingGroups
	}
	terms := strings.Fields(q)
	var out []keyGroup
	for _, g := range keybindingGroups {
		title := strings.ToLower(g.title)
		var kept []keyBinding
		for _, b := range g.bindings {
			key, desc := strings.ToLower(b.key), strings.ToLower(b.desc)
			matchesAll := true
			for _, t := range terms {
				if !strings.Contains(title, t) &&
					!strings.Contains(key, t) &&
					!strings.Contains(desc, t) {
					matchesAll = false
					break
				}
			}
			if matchesAll {
				kept = append(kept, b)
			}
		}
		if len(kept) > 0 {
			out = append(out, keyGroup{title: g.title, bindings: kept})
		}
	}
	if len(out) == 0 {
		return fuzzyFilterKeybindingGroups(q)
	}
	return out
}

// fuzzyFilterKeybindingGroups is the subsequence fallback filterKeybindingGroups
// reaches for when no binding matches the filter by substring. The query's
// whitespace is dropped — so a multi-word query is matched as one run, letting
// "switch tab" and "switchtab" behave alike — and a binding is kept when that run
// is an ordered subsequence of its "title key description" haystack. A binding's
// own group title is folded into its haystack so a section name still helps a
// fuzzy query land, matching the substring pass. Groups are returned in their
// source order with only their surviving bindings, and a query matching nothing
// even fuzzily yields no groups, leaving the overlay to show its "no shortcuts
// match" note.
func fuzzyFilterKeybindingGroups(query string) []keyGroup {
	token := strings.Join(strings.Fields(query), "")
	if token == "" {
		return nil
	}
	var out []keyGroup
	for _, g := range keybindingGroups {
		title := strings.ToLower(g.title)
		var kept []keyBinding
		for _, b := range g.bindings {
			hay := title + " " + strings.ToLower(b.key) + " " + strings.ToLower(b.desc)
			if isSubsequence(token, hay) {
				kept = append(kept, b)
			}
		}
		if len(kept) > 0 {
			out = append(out, keyGroup{title: g.title, bindings: kept})
		}
	}
	return out
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed and
// truncated to a single help-listing line. It backs the /help description for
// custom prompts that declare no frontmatter description.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const maxLen = 60
		// Measure and cut by rune, not byte, so a multi-byte character (an
		// accented letter, CJK glyph, or emoji in a prompt template) is never
		// sliced mid-rune into invalid UTF-8.
		if runes := []rune(line); len(runes) > maxLen {
			return string(runes[:maxLen-1]) + "…"
		}
		return line
	}
	return ""
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

// workingDir returns the process working directory, or "" when it cannot be
// determined. It is the default workspace root for the file-tree panel.
func workingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
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
