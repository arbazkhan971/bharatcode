package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/chat"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// userStreamID is the chat-list key for echoed user input.
const userStreamID = "local-user"

// queuedStreamPrefix is the chat-list key prefix for queued steering messages.
// A per-message counter suffix keeps each queued bubble distinct.
const queuedStreamPrefix = "local-queued"

// queuedPrefix labels a steering message in the chat so the user can see it is
// queued for the in-flight turn rather than already sent.
const queuedPrefix = "[queued] "

// agentEventMsg carries a single agent.Event into the Bubble Tea update loop.
type agentEventMsg agent.Event

// runDoneMsg signals that loop.Run has fully returned for a turn. It is emitted
// after the agent loop releases its run mutex, so it is safe to start the next
// turn (the autonomous goal loop relies on this to avoid concurrent Run calls).
type runDoneMsg struct {
	last *message.Message
	err  error
}

// assistantStreamID returns the chat-list key for the assistant bubble of the
// current turn. A per-turn suffix ensures each turn opens a fresh bubble
// instead of appending to the previous one.
func (m *model) assistantStreamID() string {
	return fmt.Sprintf("assistant-%d", m.turn)
}

// nextToolTurnID returns a fresh, unique chat-list id for an appended tool turn
// (a tool invocation or its result). The monotonic counter guarantees each turn
// is a distinct item even when concurrent read-only calls interleave their
// events, so no two tool turns ever share an id and collapse into one bubble.
func (m *model) nextToolTurnID() string {
	m.toolTurnSeq++
	return fmt.Sprintf("tool-%d", m.toolTurnSeq)
}

// startRun ensures a session exists, renders the user's prompt, launches the
// agent loop in a background goroutine, and kicks off the event listen loop. It
// also starts the streaming spinner here (not in Init) so the 12fps tick loop
// runs only while a turn is in flight rather than from program startup.
func (m *model) startRun(prompt string) (tea.Model, tea.Cmd) {
	runCmd, err := m.launchTurn(prompt)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Run failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	return m, tea.Batch(runCmd, m.ensureListening(), m.streamSpinner.Tick)
}

// continueRun launches another turn that feeds prompt to the agent and reuses
// the existing listen loop. It also starts the streaming spinner so the braille
// animation is visible for goal-loop continuations — mirrors startRun's batch.
func (m *model) continueRun(prompt string) tea.Cmd {
	runCmd, err := m.launchTurn(prompt)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Run failed", Body: err.Error(), Theme: m.theme})
		return nil
	}
	return tea.Batch(runCmd, m.streamSpinner.Tick)
}

// launchTurn ensures a session exists, opens a fresh turn, renders the prompt
// as the user bubble, and returns the command that drives the agent run.
func (m *model) launchTurn(prompt string) (tea.Cmd, error) {
	if err := m.ensureSession(); err != nil {
		return nil, err
	}
	m.turn++
	if m.tabFirstPrompt == "" {
		m.tabFirstPrompt = prompt
	}
	m.chat.Stream(userStreamID, prompt)
	m.chat.SetRole(userStreamID, message.RoleUser)
	m.chat.FinishStream(userStreamID)
	m.chat.Reindex(userStreamID)
	m.running = true
	m.turnStartedAt = m.now
	m.currentActivity = ""
	m.turnToolCount = 0    // reset per-turn tool-call counter
	m.turnErrShown = false // reset per-turn error-surfaced flag
	m.lastTurnTokens = ""  // clear previous turn's counts while the new turn runs
	m.lastContextPct = 0   // clear previous context-window fill while the new turn runs
	// Inline any @-file references so the model sees their contents, while the
	// chat bubble above keeps the user's original text. Resolution is scoped to
	// the workspace root; unresolved mentions are left untouched. Image files
	// (PNG/JPEG/GIF/WebP) are returned as separate ImageBlocks for vision models.
	expanded, _, imgBlocks := expandFileMentions(prompt, m.workspaceRoot)
	// Re-inject the active goal as a persistent frame on every turn so the
	// model stays anchored to it; the bubble above stays free of the frame.
	return m.runAgent(m.frameForAgent(expanded), imgBlocks), nil
}

// ensureSession creates a persisted session row the first time the user runs a
// prompt so the agent loop can append messages against a real session.
func (m *model) ensureSession() error {
	if m.sessionPersisted {
		return nil
	}
	modelName, agentName := initialIdentity(m.deps.Cfg)
	s := &session.Session{
		Title: "New session",
		Model: modelName,
		Agent: agentName,
	}
	if err := m.deps.Sessions.Create(m.ctx, s); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	m.sessionID = s.ID
	m.sessionPersisted = true
	m.status.SessionID = s.ID
	m.footer.SessionID = s.ID
	return nil
}

// runAgent returns a command that drives one agent turn to completion. loop.Run
// blocks, and Bubble Tea executes each command in its own goroutine, so the
// command blocks here until the turn finishes. Streaming output surfaces
// through the agent bus (drained by the listen loop) while this command runs;
// the returned runDoneMsg fires only after Run releases its run mutex, which
// makes it safe to start the next turn.
//
// imgBlocks carries any inline images collected from @-mention resolution; they
// are appended to the user message's content so vision-capable models can
// inspect them. A nil or empty slice produces a plain text-only message.
func (m *model) runAgent(prompt string, imgBlocks []message.ImageBlock) tea.Cmd {
	loop := m.deps.Agent
	sessionID := m.sessionID
	ctx := m.ctx
	repo := m.deps.Sessions
	return func() tea.Msg {
		// Expand @URL mentions inside the goroutine — network I/O must not run
		// in the Bubble Tea Update handler (which is synchronous).
		expanded, _ := expandURLMentions(ctx, prompt)
		content := make([]message.ContentBlock, 0, 1+len(imgBlocks))
		content = append(content, message.TextBlock{Text: expanded})
		for _, img := range imgBlocks {
			content = append(content, img)
		}
		userMsg := message.Message{
			Role:    message.RoleUser,
			Content: content,
		}
		err := loop.Run(ctx, sessionID, userMsg)
		return runDoneMsg{last: lastAssistantMessage(ctx, repo, sessionID), err: err}
	}
}

// lastAssistantMessage returns the most recent assistant message in the
// session, or nil if none can be read. It lets the goal loop inspect the final
// reply for a completion signal.
func lastAssistantMessage(ctx context.Context, repo *session.Repo, sessionID string) *message.Message {
	if repo == nil {
		return nil
	}
	msgs, err := repo.Messages(ctx, sessionID)
	if err != nil {
		return nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.RoleAssistant {
			m := msgs[i]
			return &m
		}
	}
	return nil
}

// ensureListening subscribes to the agent bus exactly once and returns a
// command that reads the next agent event. Subsequent calls reuse the existing
// subscription so no buffered events are lost.
func (m *model) ensureListening() tea.Cmd {
	if m.eventCh == nil {
		ch, cancel := m.deps.Bus.Subscribe()
		m.eventCh = ch
		m.eventCancel = cancel
	}
	return m.listenAgent()
}

// listenAgent returns a command that blocks until one agent event arrives on
// the established subscription channel, then delivers it as a message. The
// Update handler re-issues this command after each event so the channel is
// drained continuously without re-subscribing (which would drop buffered
// events and leave the TUI deaf after the first event).
func (m *model) listenAgent() tea.Cmd {
	ch := m.eventCh
	ctx := m.ctx
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			return agentEventMsg(ev)
		}
	}
}

// handleAgentEvent renders one agent event into the chat view and re-issues the
// listen command, keeping the stream alive. Run lifecycle (and goal-loop
// advancement) is handled separately on runDoneMsg, after loop.Run returns.
func (m *model) handleAgentEvent(ev agentEventMsg) (tea.Model, tea.Cmd) {
	streamID := m.assistantStreamID()
	switch ev.Kind {
	case agent.EventLLMResponse:
		// Fresh model text means the agent is thinking again, not inside a tool;
		// clear the activity so the status bar reverts to "working".
		m.currentActivity = ""
		if text := assistantText(ev.Message); text != "" {
			m.chat.Stream(streamID, text)
		}
	case agent.EventToolCalled:
		// Surface the running tool's name in the status bar so a long turn reads
		// as "Bash"/"Edit" rather than a bare "working". Count each call so the
		// status can show total tool invocations for progress clarity.
		m.currentActivity = ev.ToolName
		m.turnToolCount++
		// Close the assistant's prose bubble (if any) so the tool block becomes its
		// own turn rather than merging into the surrounding text. Reindex detaches
		// the id so the next model text after the tool opens a fresh bubble instead
		// of re-appending to the closed one.
		m.chat.FinishStream(streamID)
		m.chat.Reindex(streamID)
		// Append the invocation as a discrete turn. A message carrying only a
		// ToolUseBlock flattens to "tool: <name>", which the activity-stream
		// renderer leads with the action verb (e.g. "Running", "Editing"). The raw
		// JSON arguments (ev.ToolInput) are intentionally not rendered — only the
		// name drives the verb, so no argument JSON leaks into the transcript.
		useID := m.nextToolTurnID()
		if patch := editPatchForToolCall(ev.ToolName, ev.ToolInput); patch != "" {
			// A file-modifying tool (edit, write, multiedit) carries enough in its
			// arguments to show the change as a unified diff, the way /diff does.
			// Lead the turn with the "tool: <name>" marker so the verb still reads
			// "Editing", then carry the patch tagged with the diff marker so the
			// renderer routes it through the diff viewer (line numbers, red/green)
			// rather than dumping the raw arguments. A new-file write shows all-green;
			// an edit shows red/green hunks.
			m.chat.Append(message.Message{
				ID:   useID,
				Role: message.RoleAssistant,
				Content: []message.ContentBlock{message.TextBlock{
					Text: "tool: " + ev.ToolName + "\n" + chat.DiffMarker + "\n" + patch,
				}},
			})
		} else {
			m.chat.Append(message.Message{
				ID:   useID,
				Role: message.RoleAssistant,
				Content: []message.ContentBlock{message.ToolUseBlock{
					ID:    useID,
					Name:  ev.ToolName,
					Input: ev.ToolInput,
				}},
			})
		}
	case agent.EventToolResult:
		m.currentActivity = ""
		// Append the tool's output as its own turn. A tool-role message flattens to
		// its raw content, and the renderer leads it with a "Result" verb and draws
		// the output indented under the muted connector, with long output elided and
		// added/removed lines tinted. Empty output renders the header alone, so a
		// silent tool does not leave a dangling bubble.
		m.chat.Append(message.Message{
			ID:   m.nextToolTurnID(),
			Role: message.RoleTool,
			Content: []message.ContentBlock{message.ToolResultBlock{
				Content: ev.ToolResult,
			}},
		})
	case agent.EventLoopDetected:
		if text := assistantText(ev.Message); text != "" {
			m.chat.Stream(streamID, "\n"+text)
		}
		m.chat.FinishStream(streamID)
	case agent.EventRunError:
		msg := "agent error"
		if ev.Err != nil {
			msg = friendlyRunError(ev.Err)
		}
		// Close any open prose bubble, then surface the failure as its own discrete
		// notice turn rather than a bracketed marker dumped into the text.
		m.chat.FinishStream(streamID)
		m.chat.Reindex(streamID)
		m.chat.Append(message.Message{
			ID:      m.nextToolTurnID(),
			Role:    message.RoleTool,
			Content: []message.ContentBlock{message.ToolResultBlock{Content: "Error: " + msg, IsError: true}},
		})
		m.turnErrShown = true
	case agent.EventAutoCompacted:
		// Surface a brief inline notice so users understand why the visible
		// history shrank. The notice is injected as a synthetic stream so it
		// appears between the current assistant bubble and the next one.
		m.chat.Stream(streamID, "\nContext auto-compacted — older turns summarised to free space.\n")
	case agent.EventTurnFinished:
		if text := assistantText(ev.Message); text != "" {
			m.chat.Stream(streamID, text)
		}
		m.chat.FinishStream(streamID)
		// Capture the plan when the plan turn ends.
		if m.deps.Agent.PlanMode() && ev.Message != nil {
			planText := agent.ExtractPlanText(*ev.Message)
			m.deps.Coordinator.StorePlan(m.sessionID, planText)
		}
	}
	return m, m.listenAgent()
}

// handleRunDone is invoked once a turn's loop.Run has fully returned. It closes
// the assistant bubble, clears running state, and drives the autonomous goal
// loop (CHANGE 2) when one is active. A run error aborts any goal loop.
func (m *model) handleRunDone(done runDoneMsg) (tea.Model, tea.Cmd) {
	m.running = false
	m.turnStartedAt = time.Time{}
	m.currentActivity = ""
	m.chat.FinishStream(m.assistantStreamID())
	m.chat.Reindex(m.assistantStreamID())
	// Surface the turn's token counts (and per-turn USD cost when pricing is
	// configured) in the status bar once the turn is done. The counts live on
	// the last assistant message's Usage field, populated by the provider's
	// EndEvent; the cost is derived from the model's per-MTok rates in config.
	if done.last != nil && done.last.Usage != nil {
		u := done.last.Usage
		tokens := formatTurnTokens(u.InputTokens, u.OutputTokens)
		var cfg []config.Model
		if m.deps.Cfg != nil {
			cfg = m.deps.Cfg.Models
		}
		cost := turnCostUSD(cfg, m.status.Model, u.InputTokens, u.OutputTokens)
		if cost > 0 {
			m.lastTurnTokens = tokens + " · " + formatTurnCostUSD(cost)
		} else {
			m.lastTurnTokens = tokens
		}
		if window := contextWindowForModel(cfg, m.status.Model); window > 0 {
			m.lastContextPct = u.InputTokens * 100 / window
			if m.lastContextPct < 1 {
				m.lastContextPct = 1 // at least 1% when there is measurable usage
			}
			if m.lastContextPct > 100 {
				m.lastContextPct = 100
			}
		}
	}

	// Drain any steering text the agent could not consume (it arrived after the
	// loop's final steering check but before its run mutex released). The queue
	// lives on the shared Loop and the run loop drains it unconditionally at the
	// next turn, so it must be cleared here on EVERY run-end to avoid leaking
	// into an unrelated future turn.
	pending := m.deps.Agent.PendingSteering()

	if done.err != nil {
		// The turn errored or was interrupted: discard the leftover steering
		// rather than auto-starting it, since the user likely just cancelled.
		m.stopGoal()
		// Surface the failure when it was not already reported inline via an
		// EventRunError. Several Run error paths return without publishing an
		// event (e.g. session-append failures), which would otherwise vanish.
		// A user interrupt (context cancellation) is intentional, not a fault,
		// so it stays quiet.
		if !m.turnErrShown && !errors.Is(done.err, context.Canceled) {
			// Close any open prose bubble, then surface the failure as its own
			// discrete notice turn — mirroring the EventRunError path above —
			// rather than dumping a marker into the assistant text.
			id := m.assistantStreamID()
			m.chat.FinishStream(id)
			m.chat.Reindex(id)
			m.chat.Append(message.Message{
				ID:      m.nextToolTurnID(),
				Role:    message.RoleTool,
				Content: []message.ContentBlock{message.ToolResultBlock{Content: "Error: " + friendlyRunError(done.err), IsError: true}},
			})
		}
		return m, nil
	}

	// Fire a desktop notification when the terminal is out of focus so the user
	// learns the turn finished while they were away — matching the behaviour of
	// Claude Code and opencode. FocusAware suppresses the call when the window
	// still has focus, so this is a no-op for interactive sessions.
	_ = m.notifications.Notify("BharatCode", turnNotifyBody(done.last))

	if cmd := m.advanceGoal(done.last); cmd != nil {
		return m, cmd
	}
	// Deliver the leftover steering as a fresh turn. continueRun reuses the
	// existing listener rather than spawning a second.
	if len(pending) > 0 {
		return m, m.continueRun(strings.Join(pending, "\n"))
	}
	return m, nil
}

// turnNotifyBody returns a short one-line summary of the last assistant message
// for the desktop notification body. When the message is empty or nil a generic
// "Turn complete" string is used so the notification is never blank.
func turnNotifyBody(last *message.Message) string {
	text := strings.TrimSpace(assistantText(last))
	if text == "" {
		return "Turn complete"
	}
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		text = strings.TrimSpace(text[:nl])
	}
	const maxLen = 100
	if len(text) > maxLen {
		return text[:maxLen-3] + "..."
	}
	return text
}

// friendlyRunError converts a turn error into a message the user can act on. A
// missing-credentials failure (anything wrapping llm.ErrAuth) is the common
// first-run case — the default model's provider has no key — so instead of the
// raw "calling provider: ... : authentication failed" it returns a hint that
// names the in-app fixes: switch to a configured model with /model, or set a
// key / sign in. The provider's own message (which names the exact env var and
// 'bharatcode login' command) is kept as the lead so the specific remedy is not
// lost. Non-auth errors are returned verbatim.
func friendlyRunError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, llm.ErrAuth) {
		return err.Error() + "\nTip: run /model to pick a model you have a key for, or set the key / sign in, then resend."
	}
	return err.Error()
}

// assistantText extracts the plain-text content of an assistant message.
func assistantText(msg *message.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, block := range msg.Content {
		if b, ok := block.(message.TextBlock); ok && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}
