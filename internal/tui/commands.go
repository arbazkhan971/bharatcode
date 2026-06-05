package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	"github.com/arbazkhan971/bharatcode/internal/tui/diff"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// recentSessionLimit bounds the number of sessions shown in the /sessions
// picker so the dialog stays readable.
const recentSessionLimit = 20

// openSessionPicker loads recent sessions and pushes a selectable picker. When
// no sessions exist it surfaces an informational dialog instead.
func (m *model) openSessionPicker() (tea.Model, tea.Cmd) {
	sessions, err := m.deps.Sessions.List(m.ctx, session.ListFilter{Limit: recentSessionLimit})
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "sessions", Title: "Sessions", Body: "Could not list sessions: " + err.Error(), Theme: m.theme})
		return m, nil
	}
	if len(sessions) == 0 {
		m.dialogs.Push(&dialog.Text{DialogID: "sessions", Title: "Sessions", Body: "No saved sessions yet.", Theme: m.theme})
		return m, nil
	}
	m.sessionCandidates = sessions
	m.sessionCursor = 0
	m.sessionFilter = ""
	m.dialogs.Push(&dialog.Text{
		DialogID: "sessions",
		Title:    "Sessions",
		Body:     m.sessionPickerBody(),
		Theme:    m.theme,
	})
	return m, nil
}

// visibleSessions returns the picker rows that match the live filter query,
// in candidate order. An empty query returns every candidate. Matching is a
// case-insensitive substring test against the session title and short id, so
// typing part of either narrows the list (mirroring the session switchers in
// Claude Code and opencode).
func (m *model) visibleSessions() []session.Session {
	if m.sessionFilter == "" {
		return m.sessionCandidates
	}
	q := strings.ToLower(m.sessionFilter)
	out := make([]session.Session, 0, len(m.sessionCandidates))
	for _, s := range m.sessionCandidates {
		hay := strings.ToLower(s.Title + " " + shortSessionID(s.ID))
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

// sessionPickerBody renders the session list with a cursor marker and a hint.
// When a filter query is active it is echoed above the list, and an empty
// result set is reported so the user knows the query matched nothing.
func (m *model) sessionPickerBody() string {
	visible := m.visibleSessions()
	lines := make([]string, 0, len(visible)+4)
	if m.sessionFilter != "" {
		lines = append(lines, "Filter: "+m.sessionFilter, "")
	}
	if len(visible) == 0 {
		lines = append(lines, "(no sessions match)")
	}
	for i, s := range visible {
		marker := "  "
		if i == m.sessionCursor {
			marker = "> "
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		lines = append(lines, fmt.Sprintf("%s%s · %d msgs · %s", marker, title, s.MessageCount, shortSessionID(s.ID)))
	}
	lines = append(lines, "", "type to filter · ↑/↓ to move · enter to restore · esc to cancel")
	return strings.Join(lines, "\n")
}

// handleSessionPickerKey processes navigation and selection while the session
// picker is open. It returns whether the key was consumed; an unconsumed key
// (other than enter/esc) falls through to the dialog's own handler.
func (m *model) handleSessionPickerKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.sessionCursor > 0 {
			m.sessionCursor--
			m.refreshSessionPicker()
		}
		return true, nil
	case "down":
		if m.sessionCursor < len(m.visibleSessions())-1 {
			m.sessionCursor++
			m.refreshSessionPicker()
		}
		return true, nil
	case "backspace":
		// Backspace edits the live filter rather than dismissing the picker.
		if m.sessionFilter != "" {
			r := []rune(m.sessionFilter)
			m.sessionFilter = string(r[:len(r)-1])
			m.sessionCursor = 0
			m.refreshSessionPicker()
		}
		return true, nil
	case "enter":
		visible := m.visibleSessions()
		if len(visible) == 0 {
			// Nothing matches the filter; keep the picker open rather than
			// restoring an arbitrary session.
			return true, nil
		}
		chosen := visible[m.sessionCursor]
		m.dialogs.Pop()
		m.sessionCandidates = nil
		m.sessionFilter = ""
		return true, m.restoreSession(chosen.ID)
	default:
		// A printable keypress extends the filter query. Anything else (esc,
		// etc.) falls through to the dialog's own handler.
		if text := msg.Key().Text; text != "" {
			m.sessionFilter += text
			m.sessionCursor = 0
			m.refreshSessionPicker()
			return true, nil
		}
		return false, nil
	}
}

// refreshSessionPicker re-renders the open picker dialog so the moved cursor is
// reflected. It replaces the top dialog in place.
func (m *model) refreshSessionPicker() {
	m.dialogs.Pop()
	m.dialogs.Push(&dialog.Text{
		DialogID: "sessions",
		Title:    "Sessions",
		Body:     m.sessionPickerBody(),
		Theme:    m.theme,
	})
}

// restoreSession switches the active session to id and loads its persisted
// transcript into the chat view. It updates the session identity shown in the
// status bar and footer and refreshes the ledger summary for the new session.
func (m *model) restoreSession(id string) tea.Cmd {
	sess, err := m.deps.Sessions.Get(m.ctx, id)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Restore failed", Body: err.Error(), Theme: m.theme})
		return nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, id)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Restore failed", Body: err.Error(), Theme: m.theme})
		return nil
	}

	m.sessionID = sess.ID
	m.sessionPersisted = true
	m.status.SessionID = sess.ID
	m.status.Model = sess.Model
	m.status.Agent = sess.Agent
	m.footer.SessionID = sess.ID
	// Reset the session-scoped spend; the ledger bus repopulates it for the
	// restored session as fresh summaries arrive.
	m.footer.CostINR = 0

	m.chat.Clear()
	for _, msg := range msgs {
		m.chat.Append(msg)
	}
	// Refresh the ledger footer for the newly active session. The summary
	// command is best-effort and returns nil on error, so a quiet or
	// unavailable ledger never blocks the switch; live ledger-bus events keep
	// the footer current thereafter.
	return m.waitLedger()
}

// handleFork branches the current session and switches to the new fork,
// surfacing a confirmation dialog. It is a no-op with an explanatory dialog
// when there is no persisted session to fork.
func (m *model) handleFork() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "fork", Title: "Fork", Body: "No active session to fork yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}
	forked, err := forkSession(m.ctx, m.deps.Sessions, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Fork failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	cmd := m.restoreSession(forked.ID)
	m.dialogs.Push(&dialog.Text{
		DialogID: "fork",
		Title:    "Forked session",
		Body:     fmt.Sprintf("Branched into %s\nNow editing %s", forked.Title, shortSessionID(forked.ID)),
		Theme:    m.theme,
	})
	return m, cmd
}

// compactStreamID is the chat-list key for the /compact confirmation. A fixed
// id keeps it distinct from per-turn assistant bubbles; only one confirmation
// is shown at a time, so it does not need a counter suffix.
const compactStreamID = "local-compact"

// compactConfirmation is the message surfaced in the chat after a successful
// manual context compaction.
const compactConfirmation = "Context compacted — older turns summarized."

// handleCompact condenses the active session's conversation in memory via the
// agent loop's Compactor seam, so the next provider request sends a smaller
// history. It is a no-op with an explanatory dialog when there is no persisted
// session yet. On success it surfaces a confirmation in the chat. Compaction
// never mutates the on-disk transcript; it only changes what the agent sends to
// the provider on subsequent turns.
func (m *model) handleCompact() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "compact", Title: "Compact", Body: "No active session to compact yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}
	if err := m.deps.Agent.Compact(m.ctx, m.sessionID); err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Compact failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	m.chat.Stream(compactStreamID, compactConfirmation)
	m.chat.FinishStream(compactStreamID)
	m.chat.Reindex(compactStreamID)
	return m, nil
}

// handleDiff renders the most recent edit, multiedit, or write tool call for
// the active session as a before/after unified diff. It surfaces an
// informational dialog when no such change exists.
func (m *model) handleDiff() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "No edit diff is available yet.", Theme: m.theme})
		return m, nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "Could not load messages: " + err.Error(), Theme: m.theme})
		return m, nil
	}
	diffs := latestEditDiffs(msgs)
	if len(diffs) == 0 {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "No edit diff is available yet.", Theme: m.theme})
		return m, nil
	}
	patch := unifiedPatch(diffs)
	viewer := diff.New(m.theme)
	rendered := viewer.RenderUnifiedNumbered(patch, max(1, m.width-6))
	// Lead with a diffstat summary so the scope of the change is visible before
	// scrolling, matching the header Claude Code and opencode show above a diff.
	// A multi-file change gets a per-file breakdown beneath the aggregate line.
	if stat := viewer.StatLines(patch); stat != "" {
		rendered = stat + "\n\n" + rendered
	}
	m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: rendered, Theme: m.theme})
	return m, nil
}

// planEnabledBody and planDisabledBody are the dialog bodies shown when plan
// mode is toggled, kept as constants so the wording stays consistent.
const (
	planEnabledBody = "Plan mode on. The agent is restricted to read-only tools and will propose a plan instead of editing. Use /approve to execute."
	approveBody     = "Plan approved. Execution tools are enabled again; send your next prompt to proceed."
	approveNoopBody = "Not in plan mode. Nothing to approve."
)

// handlePlan turns on plan mode on the live agent loop so the next turn is
// restricted to read-only tools and the agent is prompted to produce a plan
// rather than execute. It takes effect on the next provider call; the existing
// session is preserved. /approve clears it.
func (m *model) handlePlan() (tea.Model, tea.Cmd) {
	if m.deps.Agent.PlanMode() {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Plan mode", Body: planEnabledBody, Theme: m.theme})
		return m, nil
	}
	m.deps.Agent.SetPlanMode(true)
	m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Plan mode", Body: planEnabledBody, Theme: m.theme})
	return m, nil
}

// handleApprove exits plan mode on the live agent loop, re-enabling execution
// tools. It is a no-op (with an explanatory dialog) when the loop is not in plan
// mode. When a plan is stored, it shows the plan for final review before
// auto-continuing execution with the approved plan.
func (m *model) handleApprove() (tea.Model, tea.Cmd) {
	if !m.deps.Agent.PlanMode() {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve", Body: approveNoopBody, Theme: m.theme})
		return m, nil
	}

	// Retrieve the stored plan for display in the approval dialog.
	planText := m.deps.Coordinator.PlanFor(m.sessionID)

	// Show the plan in the approval dialog so the user can review it before execution.
	if planText != "" {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve Plan", Body: planText, Theme: m.theme})
	} else {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve", Body: approveBody, Theme: m.theme})
	}

	// Approve the plan: transitions the loop out of plan mode and retrieves the plan.
	planText = m.deps.Coordinator.ApprovePlan(m.sessionID, m.deps.Agent)

	// Auto-continue execution: seed the next turn with the approved plan.
	// Extract the prompt text from the seed message so continueRun can render it.
	seed := agent.SeedMessageFromPlan(m.sessionID, planText)
	var seedText string
	for _, block := range seed.Content {
		if tb, ok := block.(message.TextBlock); ok {
			seedText = tb.Text
			break
		}
	}
	return m, m.continueRun(seedText)
}

// handleStatus pushes a panel summarizing the active model, agent, session,
// message count, approval mode, and INR spend for this session.
func (m *model) handleStatus() (tea.Model, tea.Cmd) {
	m.dialogs.Push(&dialog.Text{DialogID: "status", Title: "Status", Body: m.statusPanel(), Theme: m.theme})
	return m, nil
}

// statusPanel renders the status panel body.
func (m *model) statusPanel() string {
	lines := []string{
		"Model: " + m.status.Model,
		"Agent: " + m.status.Agent,
		"Session: " + m.sessionID,
		fmt.Sprintf("Messages: %d", m.sessionMessageCount()),
		"Approval: " + approvalModeLabel(m.deps.Permission.GetApprovalMode()),
		fmt.Sprintf("Session spend: ₹%.2f", m.footer.CostINR),
	}
	if m.footer.MonthlyBudgetINR > 0 {
		lines = append(lines, fmt.Sprintf("Monthly budget: ₹%.2f", m.footer.MonthlyBudgetINR))
	}
	return strings.Join(lines, "\n")
}

// sessionMessageCount returns the persisted message count for the active
// session, or 0 when the session has not been persisted or cannot be read.
func (m *model) sessionMessageCount() int {
	if !m.sessionPersisted {
		return 0
	}
	sess, err := m.deps.Sessions.Get(m.ctx, m.sessionID)
	if err != nil {
		return 0
	}
	return sess.MessageCount
}

// handleRegistryPrompt looks up name in the prompt registry and, when found,
// renders it with the remaining args spliced into {{input}} and submits the
// expansion to the agent. It reports whether the command was handled as a
// registry prompt; an unregistered name returns false so the caller can fall
// back to the unknown-command dialog.
func (m *model) handleRegistryPrompt(name string, args string) (handled bool, model tea.Model, cmd tea.Cmd) {
	if m.deps.Prompts == nil {
		return false, m, nil
	}
	if _, ok := m.deps.Prompts.Get(name); !ok {
		return false, m, nil
	}
	rendered, err := m.deps.Prompts.RenderSlash(name, args)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Prompt error", Body: err.Error(), Theme: m.theme})
		return true, m, nil
	}
	// Splice in any !`cmd` inline shell substitutions so the template can embed
	// live repository state (git status, branch, test output) at invocation
	// time, matching Claude Code / pi custom-command behaviour.
	rendered = expandBangCommands(rendered, m.runBangCommand)
	model, cmd = m.startRun(rendered)
	return true, model, cmd
}

// handleRegistryRecipe looks up name in the recipe registry and, when found,
// collects any RequirementUserPrompt parameters interactively (pushing one
// dialog per parameter), then renders the recipe and submits the result to the
// agent via m.startRun. When args is non-empty it is pre-populated as an
// "input" parameter value (mirroring handleRegistryPrompt's {{input}} binding)
// and also used as the fallback value for any single required user_prompt
// parameter. It reports handled=true when the name matches a recipe; an
// unregistered name returns handled=false so the caller can fall back to the
// unknown-command dialog.
func (m *model) handleRegistryRecipe(name string, args string) (handled bool, mod tea.Model, cmd tea.Cmd) {
	if m.deps.Recipes == nil {
		return false, m, nil
	}
	entry, ok := m.deps.Recipes.Get(name)
	if !ok {
		return false, m, nil
	}

	r, err := entry.Load()
	if err != nil {
		m.dialogs.Push(&dialog.Text{
			DialogID: "error",
			Title:    "Recipe load error",
			Body:     err.Error(),
			Theme:    m.theme,
		})
		return true, m, nil
	}

	// Pre-populate the "input" key from args (mirroring handleRegistryPrompt)
	// and also seed any single user_prompt param that has no default.
	prePopulated := make(map[string]string)
	if args != "" {
		prePopulated["input"] = args
		// If there is exactly one user_prompt parameter and the user passed
		// args, bind args to that parameter so simple one-param recipes just work
		// without a dialog: /myrecipe some text -> param filled from args.
		var userPromptParams []recipe.Parameter
		for _, p := range r.Parameters {
			if p.Requirement == recipe.RequirementUserPrompt {
				userPromptParams = append(userPromptParams, p)
			}
		}
		if len(userPromptParams) == 1 && prePopulated[userPromptParams[0].Name] == "" {
			prePopulated[userPromptParams[0].Name] = args
		}
	}

	// onComplete is called once all user_prompt parameters have been
	// collected. It renders the recipe and submits the result to the agent.
	onComplete := func(params map[string]string) (tea.Model, tea.Cmd) {
		rendered, rerr := recipe.Render(r, params)
		if rerr != nil {
			m.dialogs.Push(&dialog.Text{
				DialogID: "error",
				Title:    "Recipe render error",
				Body:     rerr.Error(),
				Theme:    m.theme,
			})
			return m, nil
		}
		return m.startRun(rendered)
	}

	collector := newRecipeParamCollector(m, r, prePopulated, onComplete)
	// Store the active collector on the model so handleKey can advance it
	// after each parameter dialog pops.
	m.recipeCollector = collector
	mod, cmd = collector.pushNextOrComplete(m)
	return true, mod, cmd
}

// shortSessionID truncates a session id to a stable short form for display.
func shortSessionID(id string) string {
	if id == "" {
		return "new"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// loadPromptRegistry builds the prompt registry from the standard global and
// project prompt directories. It never returns an error; a load failure yields
// an empty registry so a missing or malformed prompts directory cannot block
// TUI startup.
func loadPromptRegistry(cfg *config.Config) *config.PromptRegistry {
	reg, err := config.LoadPromptRegistry(promptDirs(cfg)...)
	if err != nil || reg == nil {
		empty, _ := config.LoadPromptRegistry()
		return empty
	}
	return reg
}

// promptDirs returns the directories scanned for custom prompts. The set is
// derived from the configured data directory when available; an empty slice is
// acceptable and yields an empty registry.
func promptDirs(cfg *config.Config) []string {
	if cfg == nil || cfg.Options.DataDir == "" {
		return nil
	}
	return []string{filepath.Join(cfg.Options.DataDir, "prompts")}
}
