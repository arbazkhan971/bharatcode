package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
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

// startRun ensures a session exists, renders the user's prompt, launches the
// agent loop in a background goroutine, and kicks off the event listen loop. It
// is used for user-initiated turns; goal-loop continuations use continueRun so
// they do not spawn a second concurrent listener.
func (m *model) startRun(prompt string) (tea.Model, tea.Cmd) {
	runCmd, err := m.launchTurn(prompt)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Run failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	return m, tea.Batch(runCmd, m.ensureListening())
}

// continueRun launches another turn that feeds prompt to the agent and reuses
// the existing listen loop. It returns only the run command; the caller is
// responsible for keeping a single listener alive.
func (m *model) continueRun(prompt string) tea.Cmd {
	runCmd, err := m.launchTurn(prompt)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Run failed", Body: err.Error(), Theme: m.theme})
		return nil
	}
	return runCmd
}

// launchTurn ensures a session exists, opens a fresh turn, renders the prompt
// as the user bubble, and returns the command that drives the agent run.
func (m *model) launchTurn(prompt string) (tea.Cmd, error) {
	if err := m.ensureSession(); err != nil {
		return nil, err
	}
	m.turn++
	m.chat.Stream(userStreamID, prompt)
	m.chat.FinishStream(userStreamID)
	m.chat.Reindex(userStreamID)
	m.running = true
	// Inline any @-file references so the model sees their contents, while the
	// chat bubble above keeps the user's original text. Resolution is scoped to
	// the workspace root; unresolved mentions are left untouched.
	expanded, _ := expandFileMentions(prompt, m.workspaceRoot)
	return m.runAgent(expanded), nil
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
func (m *model) runAgent(prompt string) tea.Cmd {
	loop := m.deps.Agent
	sessionID := m.sessionID
	ctx := m.ctx
	repo := m.deps.Sessions
	return func() tea.Msg {
		userMsg := message.Message{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: prompt}},
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
		if text := assistantText(ev.Message); text != "" {
			m.chat.Stream(streamID, text)
		}
	case agent.EventToolCalled:
		m.chat.Stream(streamID, "\n[tool: "+ev.ToolName+"]\n")
	case agent.EventToolResult:
		m.chat.Stream(streamID, "[done: "+ev.ToolName+"]\n")
	case agent.EventLoopDetected:
		if text := assistantText(ev.Message); text != "" {
			m.chat.Stream(streamID, "\n"+text)
		}
		m.chat.FinishStream(streamID)
	case agent.EventRunError:
		msg := "agent error"
		if ev.Err != nil {
			msg = ev.Err.Error()
		}
		m.chat.Stream(streamID, "\n[error: "+msg+"]\n")
		m.chat.FinishStream(streamID)
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
	m.chat.FinishStream(m.assistantStreamID())
	m.chat.Reindex(m.assistantStreamID())

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
		return m, nil
	}
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
