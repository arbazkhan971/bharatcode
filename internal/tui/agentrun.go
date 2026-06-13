package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/identity"
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

// noticeMsg carries an out-of-band UI notice (an app.Notice) that arrived on the
// consolidated stream. Surfacing it as its own message keeps the demux in
// listenAgent total over every UIEvent kind, so a future producer reaching the
// UI through the single stream needs no new subscription.
type noticeMsg app.Notice

// runDoneMsg signals that loop.Run has fully returned for a turn. It is emitted
// after the agent loop releases its run mutex, so it is safe to start the next
// turn (the autonomous goal loop relies on this to avoid concurrent Run calls).
type runDoneMsg struct {
	// sessionID is the session whose turn finished. With per-session concurrent
	// runs, runDoneMsg may arrive for a background tab; the handler uses this to
	// decide whether the finished run is the active tab's.
	sessionID string
	last      *message.Message
	err       error
}

// assistantStreamID returns the chat-list key for the active tab's assistant
// bubble of the current turn. A per-turn suffix ensures each turn opens a fresh
// bubble instead of appending to the previous one.
func (m *model) assistantStreamID() string {
	return assistantStreamIDFor(m.turn)
}

// assistantStreamIDFor returns the assistant-bubble chat-list key for the given
// turn number. The background-stream path keys on the owning tab's own turn
// (t.turn) so its bubble lands in that tab's distinct *chat.List, never the
// active tab's; the per-turn suffix opens a fresh bubble each turn.
func assistantStreamIDFor(turn int) string {
	return fmt.Sprintf("assistant-%d", turn)
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
	// A turn is starting, so the shell is now a conversation: leave the landing
	// page for chat. This is the single landing→chat transition point, covering
	// the first prompt, goal-loop continuations, and identity answers alike.
	m.setState(uiChat, m.focus)
	m.turn++
	if m.tabFirstPrompt == "" {
		m.tabFirstPrompt = prompt
	}
	m.chat.Stream(userStreamID, prompt)
	m.chat.SetRole(userStreamID, message.RoleUser)
	m.chat.FinishStream(userStreamID)
	m.chat.Reindex(userStreamID)
	if answer, ok := identity.Answer(prompt); ok {
		if err := m.appendLocalIdentityTurn(prompt, answer); err != nil {
			return nil, err
		}
		return nil, nil
	}
	m.running = true
	m.turnStartedAt = m.now
	m.currentActivity = ""
	m.turnToolCount = 0    // reset per-turn tool-call counter
	m.turnErrShown = false // reset per-turn error-surfaced flag
	m.deltaPending = 0     // no provisional delta text yet this turn
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

func (m *model) appendLocalIdentityTurn(prompt, answer string) error {
	if m.deps.Sessions != nil && m.sessionID != "" {
		now := time.Now().UTC()
		if err := m.deps.Sessions.AppendMessage(m.ctx, m.sessionID, message.Message{
			Role:      message.RoleUser,
			Content:   []message.ContentBlock{message.TextBlock{Text: prompt}},
			CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("appending identity user message: %w", err)
		}
		if err := m.deps.Sessions.AppendMessage(m.ctx, m.sessionID, message.Message{
			Role:      message.RoleAssistant,
			Content:   []message.ContentBlock{message.TextBlock{Text: answer}},
			CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("appending identity assistant message: %w", err)
		}
	}
	id := m.assistantStreamID()
	m.chat.Stream(id, answer)
	m.chat.SetRole(id, message.RoleAssistant)
	m.chat.FinishStream(id)
	m.chat.Reindex(id)
	m.currentActivity = ""
	m.running = false
	return nil
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
	// Carry the yolo intent (from --yolo or a /yolo toggle made while the session
	// was still the "new" placeholder) onto the freshly created session, so
	// auto-approval is scoped to this real session id.
	if m.status.Yolo && m.deps.Workspace != nil {
		m.deps.Workspace.SetSessionYolo(s.ID, true)
	}
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
//
// The turn is driven through Workspace.Prompt, which routes via the per-session
// SessionRunner: each session resolves its OWN Loop, so distinct tabs run
// concurrently, a second prompt for the same session queues behind the first
// rather than panicking the Loop, and Interrupt/InterruptSession cancel through
// the runner. Prompt has the same blocking semantics as a direct Run (it blocks
// until the runner's Wait returns).
func (m *model) runAgent(prompt string, imgBlocks []message.ImageBlock) tea.Cmd {
	ws := m.deps.Workspace
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
			SessionID: sessionID,
			Role:      message.RoleUser,
			Content:   content,
		}
		err := ws.Prompt(ctx, sessionID, userMsg)
		return runDoneMsg{sessionID: sessionID, last: lastAssistantMessage(ctx, repo, sessionID), err: err}
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

// ensureListening subscribes to the consolidated UI event stream exactly once,
// through the workspace seam, and returns a command that reads the next event.
// The single stream carries agent transitions, permission requests, and notices,
// so this one subscription replaces the former separate agent-bus and
// permission-topic subscriptions. Subsequent calls reuse the existing
// subscription so no buffered events are lost.
func (m *model) ensureListening() tea.Cmd {
	if m.eventCh == nil {
		ch, cancel := m.deps.Workspace.Subscribe()
		m.eventCh = ch
		m.eventCancel = cancel
	}
	return m.listenAgent()
}

// listenAgent returns a command that blocks until one event arrives on the
// established consolidated subscription, then demultiplexes it into the matching
// Bubble Tea message: an agent transition becomes agentEventMsg, a permission
// request becomes permissionRequestMsg, and a notice becomes noticeMsg. The
// Update handler re-issues this command after each event so the channel is
// drained continuously without re-subscribing (which would drop buffered events
// and leave the TUI deaf after the first event).
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
			return uiEventMsg(ev)
		}
	}
}

// uiEventMsg maps one consolidated app.UIEvent onto the Bubble Tea message the
// Update loop already handles, by switching on the event kind. Keeping the demux
// here (rather than three separate subscriptions) is the whole point of the
// consolidated stream: a single Subscribe feeds agent, permission, and notice
// handling. An unknown kind yields a nil message, which Bubble Tea ignores.
func uiEventMsg(ev app.UIEvent) tea.Msg {
	switch ev.Kind {
	case app.UIEventAgent:
		return agentEventMsg(ev.Agent)
	case app.UIEventPermission:
		return permissionRequestMsg(ev.Permission)
	case app.UIEventNotice:
		return noticeMsg(ev.Notice)
	default:
		return nil
	}
}

// eventTarget carries the per-tab render state one agent event mutates: the
// chat list it streams into, the assistant-bubble key for the owning tab's
// current turn, and pointers to the four run-state fields the event advances.
// Resolving the target by sessionID first, then running one shared body against
// it, lets the active tab (target fields point at m.*) and a background tab
// (they point at the tab struct's fields) share identical rendering logic — so a
// background stream advances its own tab's transcript and counters without ever
// touching the active tab's chat or state.
type eventTarget struct {
	chat            *chat.List
	streamID        string
	deltaPending    *int
	currentActivity *string
	turnToolCount   *int
	turnErrShown    *bool
}

// activeEventTarget returns the target backed by the active tab's live model
// fields, so the existing single-tab render path runs byte-identically.
func (m *model) activeEventTarget() eventTarget {
	return eventTarget{
		chat:            m.chat,
		streamID:        m.assistantStreamID(),
		deltaPending:    &m.deltaPending,
		currentActivity: &m.currentActivity,
		turnToolCount:   &m.turnToolCount,
		turnErrShown:    &m.turnErrShown,
	}
}

// backgroundEventTarget returns the target backed by a non-active tab's own
// fields and chat list, keyed on that tab's turn so its bubble lands in its
// distinct list.
func backgroundEventTarget(t *tab) eventTarget {
	return eventTarget{
		chat:            t.chat,
		streamID:        assistantStreamIDFor(t.turn),
		deltaPending:    &t.deltaPending,
		currentActivity: &t.currentActivity,
		turnToolCount:   &t.turnToolCount,
		turnErrShown:    &t.turnErrShown,
	}
}

// handleAgentEvent renders one agent event into its owning tab's chat view and
// re-issues the listen command, keeping the stream alive. The owning tab is
// resolved by ev.SessionID FIRST: an event for the active session runs against
// m.* (byte-identical to the single-tab path), an event for a background tab
// runs the SAME logic against that tab's own chat and counters, and an event for
// a closed/unknown session is dropped. This is what makes a background run
// visible — its output keeps streaming into its tab while another tab is active.
// Run lifecycle (and goal-loop advancement) is handled separately on runDoneMsg.
func (m *model) handleAgentEvent(ev agentEventMsg) (tea.Model, tea.Cmd) {
	owner, ok := m.tabForSession(ev.SessionID)
	if !ok {
		// No open tab owns this session (it was closed): drop the straggler — there
		// is no chat to render it into — and keep the stream alive.
		return m, m.listenAgent()
	}
	var tgt eventTarget
	if ev.SessionID == "" || ev.SessionID == m.sessionID {
		tgt = m.activeEventTarget()
	} else {
		tgt = backgroundEventTarget(owner)
	}
	m.applyAgentEvent(tgt, ev)
	return m, m.listenAgent()
}

// applyAgentEvent renders one agent event into tgt's chat list and advances
// tgt's run-state counters. It is the single shared body the active and
// background paths both run, parameterized by the owning tab's chat/streamID and
// counter pointers. Tool-turn ids come from m.nextToolTurnID() (a global
// monotonic counter) in both paths so two concurrently streaming tabs never
// collide ids and collapse tool bubbles. The PlanMode/StorePlan capture stays
// keyed on ev.SessionID so a background tab's plan turn stores against the right
// session.
func (m *model) applyAgentEvent(tgt eventTarget, ev agentEventMsg) {
	cl := tgt.chat
	streamID := tgt.streamID
	switch ev.Kind {
	case agent.EventLLMStreamStart:
		// A provider stream attempt is starting. Any un-reconciled delta text
		// belongs to a previous attempt of this same call that failed mid-stream
		// (the retry re-streams the response from the beginning), so rewind it
		// before the fresh attempt's deltas arrive. Completed calls always reset
		// deltaPending via their boundary event, so this can never eat text from
		// an earlier, finished response.
		if *tgt.deltaPending > 0 {
			cl.TruncateStreamTail(streamID, *tgt.deltaPending)
			*tgt.deltaPending = 0
		}
	case agent.EventLLMDelta:
		// Incremental assistant text: render it as it arrives. The bytes are
		// provisional — EventLLMResponse below replaces them with the canonical
		// full text — so count what was appended for exact rollback.
		*tgt.currentActivity = ""
		if ev.Delta != "" {
			cl.Stream(streamID, ev.Delta)
			*tgt.deltaPending += len(ev.Delta)
		}
	case agent.EventLLMResponse:
		// Fresh model text means the agent is thinking again, not inside a tool;
		// clear the activity so the status bar reverts to "working".
		*tgt.currentActivity = ""
		// Replace the provisional delta text with the canonical response so the
		// bubble is byte-identical to the recorded message even when the lossy
		// bus dropped some deltas mid-stream.
		if *tgt.deltaPending > 0 {
			cl.TruncateStreamTail(streamID, *tgt.deltaPending)
			*tgt.deltaPending = 0
		}
		if text := assistantText(ev.Message); text != "" {
			cl.Stream(streamID, text)
		}
	case agent.EventToolCalled:
		// Surface the running tool's name in the status bar so a long turn reads
		// as "Bash"/"Edit" rather than a bare "working". Count each call so the
		// status can show total tool invocations for progress clarity.
		*tgt.currentActivity = ev.ToolName
		*tgt.turnToolCount++
		// Close the assistant's prose bubble (if any) so the tool block becomes its
		// own turn rather than merging into the surrounding text. Reindex detaches
		// the id so the next model text after the tool opens a fresh bubble instead
		// of re-appending to the closed one. Closing the bubble commits any delta
		// text it still holds (the canonical EventLLMResponse normally reconciled
		// it already; if the lossy bus dropped that event the deltas are the best
		// rendition we have), so the pending counter must not survive the close.
		*tgt.deltaPending = 0
		cl.FinishStream(streamID)
		cl.Reindex(streamID)
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
			cl.Append(message.Message{
				ID:   useID,
				Role: message.RoleAssistant,
				Content: []message.ContentBlock{message.TextBlock{
					Text: "tool: " + ev.ToolName + "\n" + chat.DiffMarker + "\n" + patch,
				}},
			})
		} else {
			cl.Append(message.Message{
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
		*tgt.currentActivity = ""
		// Append the tool's output as its own turn. A tool-role message flattens to
		// its raw content, and the renderer leads it with a "Result" verb and draws
		// the output indented under the muted connector, with long output elided and
		// added/removed lines tinted. Empty output renders the header alone, so a
		// silent tool does not leave a dangling bubble.
		cl.Append(message.Message{
			ID:   m.nextToolTurnID(),
			Role: message.RoleTool,
			Content: []message.ContentBlock{message.ToolResultBlock{
				Content: ev.ToolResult,
			}},
		})
	case agent.EventLoopDetected:
		*tgt.deltaPending = 0
		if text := assistantText(ev.Message); text != "" {
			cl.Stream(streamID, "\n"+text)
		}
		cl.FinishStream(streamID)
	case agent.EventRunError:
		msg := "agent error"
		if ev.Err != nil {
			msg = friendlyRunError(ev.Err)
		}
		// Close any open prose bubble, then surface the failure as its own discrete
		// notice turn rather than a bracketed marker dumped into the text. The
		// partial delta text above the error block (if any) stays — what arrived
		// before the failure is information — but its pending counter must not
		// leak into the next turn's reconciliation.
		*tgt.deltaPending = 0
		cl.FinishStream(streamID)
		cl.Reindex(streamID)
		cl.Append(message.Message{
			ID:      m.nextToolTurnID(),
			Role:    message.RoleTool,
			Content: []message.ContentBlock{message.ToolResultBlock{Content: "Error: " + msg, IsError: true}},
		})
		*tgt.turnErrShown = true
	case agent.EventAutoCompacted:
		// Surface a brief inline notice so users understand why the visible
		// history shrank. The notice is injected as a synthetic stream so it
		// appears between the current assistant bubble and the next one.
		cl.Stream(streamID, "\nContext auto-compacted — older turns summarised to free space.\n")
	case agent.EventTurnFinished:
		*tgt.deltaPending = 0
		if text := assistantText(ev.Message); text != "" {
			cl.Stream(streamID, text)
		}
		cl.FinishStream(streamID)
		// Capture the plan when the plan turn ends. Key the plan-mode check and the
		// store on the event's own session so a background tab's plan turn stores
		// against the right session rather than the focused tab.
		if m.deps.Workspace.PlanMode(ev.SessionID) && ev.Message != nil {
			planText := agent.ExtractPlanText(*ev.Message)
			m.deps.Coordinator.StorePlan(ev.SessionID, planText)
		}
	}
}

// handleRunDone is invoked once a turn's loop.Run has fully returned. It closes
// the assistant bubble, clears running state, and drives the autonomous goal
// loop (CHANGE 2) when one is active. A run error aborts any goal loop.
//
// With per-session concurrent runs a runDoneMsg can arrive for a BACKGROUND tab
// while a different tab is active. The owning tab is resolved by done.sessionID
// FIRST: a background finish clears only that tab's own run-state and finishes
// its own assistant bubble (handleBackgroundRunDone), so it never wedges the
// active tab's m.running or corrupts the active chat. A finish for the active
// session runs the existing path verbatim below. A finish for a closed/unknown
// session is dropped.
//
// Sequencing note: runDoneMsg can be delivered to the Bubble Tea loop a
// hair before the turn's final agent events (EventLLMResponse, EventTurnFinished)
// have been dispatched from m.eventCh. Reindexing the assistant stream ID here
// would detach it from the chat index, causing any straggler event to open a
// fresh (duplicate) assistant bubble. We intentionally do NOT Reindex the
// assistant stream after closing it: the per-turn suffix in assistantStreamID
// already ensures the next turn opens a new bubble, so Reindex is redundant and
// its only effect is to break the straggler invariant. A late delta folds
// correctly into the existing finished item via the normal Stream path.
func (m *model) handleRunDone(done runDoneMsg) (tea.Model, tea.Cmd) {
	owner, ok := m.tabForSession(done.sessionID)
	if !ok {
		// The owning tab was closed before its run returned: there is nothing to
		// update, and ReleaseSession already dropped the run's bookkeeping.
		return m, nil
	}
	if done.sessionID != "" && done.sessionID != m.sessionID {
		// A background tab finished: update only its own state, never m.*.
		return m.handleBackgroundRunDone(owner, done)
	}

	m.running = false
	m.turnStartedAt = time.Time{}
	m.currentActivity = ""
	m.chat.FinishStream(m.assistantStreamID())
	// Do NOT Reindex here — see sequencing note in the function comment.
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
	pending := m.deps.Workspace.PendingSteering(done.sessionID)

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
			// Reindex is intentionally omitted here (see handleRunDone comment).
			id := m.assistantStreamID()
			m.chat.FinishStream(id)
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
	body := turnNotifyBody(done.last)
	if body == "Turn complete" {
		if m.deps.Sessions != nil && m.sessionPersisted && m.sessionID != "" {
			if msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID); err == nil {
				if fallback := turnNotifyBodyFromMessages(msgs); fallback != "" {
					body = fallback
				}
			}
		}
	}
	_ = m.notifications.Notify("BharatCode", body)

	// Append a compact local completion summary so the user sees exactly which
	// files the turn changed and how they were verified — without scrolling the
	// transcript or reading any log. The summary is suppressed when the assistant's
	// own prose already named every changed path (no duplication) and the prose was
	// non-empty; an empty final answer always yields the summary so a silent
	// file-creation turn still ends with useful completion text.
	m.appendCompletionSummary(done.last)

	// Refresh the cached changed-file count surfaced in the header strip. Turn end
	// is the only point the count can move, so it is queried here rather than on
	// every render frame.
	m.refreshChangedCount()

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

// handleBackgroundRunDone finishes a turn that completed in a non-active tab. It
// mutates ONLY that tab's struct fields and its own chat list — never m.* — so a
// background finish can never clear the active tab's running indicator or corrupt
// its transcript. It mirrors the active path's bubble-close, token/context
// capture, and error surfacing, but deliberately does NOT advance an autonomous
// goal loop or auto-continue leftover steering: those are single-tab intents that
// park until the tab is focused (no regression, since today no run can be
// backgrounded at all). The drained steering is discarded so it does not leak
// into an unrelated future turn on the shared Loop.
func (m *model) handleBackgroundRunDone(t *tab, done runDoneMsg) (tea.Model, tea.Cmd) {
	t.running = false
	t.turnStartedAt = time.Time{}
	t.currentActivity = ""
	streamID := assistantStreamIDFor(t.turn)
	t.chat.FinishStream(streamID)
	// Do NOT Reindex — same straggler invariant as the active path.
	if done.last != nil && done.last.Usage != nil {
		u := done.last.Usage
		tokens := formatTurnTokens(u.InputTokens, u.OutputTokens)
		var cfg []config.Model
		if m.deps.Cfg != nil {
			cfg = m.deps.Cfg.Models
		}
		// A background tab can run a different model than the active one; key the
		// cost/window on the tab's own status model rather than m.status.Model.
		cost := turnCostUSD(cfg, t.statusModel, u.InputTokens, u.OutputTokens)
		if cost > 0 {
			t.lastTurnTokens = tokens + " · " + formatTurnCostUSD(cost)
		} else {
			t.lastTurnTokens = tokens
		}
		if window := contextWindowForModel(cfg, t.statusModel); window > 0 {
			t.lastContextPct = u.InputTokens * 100 / window
			if t.lastContextPct < 1 {
				t.lastContextPct = 1
			}
			if t.lastContextPct > 100 {
				t.lastContextPct = 100
			}
		}
	}
	// Clear any leftover steering on the shared Loop for this session so it does
	// not leak into a future turn; a background tab does not auto-continue it.
	_ = m.deps.Workspace.PendingSteering(done.sessionID)
	if done.err != nil {
		// Surface the failure into the background tab's own transcript when it was
		// not already reported inline, mirroring the active error path. A user
		// interrupt is intentional, so it stays quiet.
		if !t.turnErrShown && !errors.Is(done.err, context.Canceled) {
			t.chat.FinishStream(streamID)
			t.chat.Append(message.Message{
				ID:      m.nextToolTurnID(),
				Role:    message.RoleTool,
				Content: []message.ContentBlock{message.ToolResultBlock{Content: "Error: " + friendlyRunError(done.err), IsError: true}},
			})
		}
		return m, nil
	}
	// Notify on a background completion too, so a turn finishing in a non-focused
	// tab still reaches the user when the terminal is out of focus.
	_ = m.notifications.Notify("BharatCode", turnNotifyBody(done.last))
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

// turnNotifyBodyFromMessages falls back to the most recent tool result when a
// turn ends without final assistant prose. That keeps the desktop notification
// useful for simple file-creation tasks where the tool output already contains
// the absolute path or verification detail the user needs.
func turnNotifyBodyFromMessages(messages []message.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case message.RoleAssistant:
			if body := turnNotifyBody(&msg); body != "Turn complete" {
				return body
			}
		case message.RoleTool:
			if body := toolResultSummary(&msg); body != "" {
				return body
			}
		}
	}
	return ""
}

// toolResultSummary returns the first line of the most recent tool result, or
// "" when the message does not carry one. The notification body stays one line
// long, while the CLI fallback can use the full tool result content.
func toolResultSummary(msg *message.Message) string {
	if msg == nil || msg.Role != message.RoleTool {
		return ""
	}
	for _, block := range msg.Content {
		if result, ok := block.(message.ToolResultBlock); ok && result.Content != "" {
			return firstLine(result.Content)
		}
		if result, ok := block.(*message.ToolResultBlock); ok && result.Content != "" {
			return firstLine(result.Content)
		}
	}
	return ""
}

func firstLine(text string) string {
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		text = text[:nl]
	}
	return strings.TrimSpace(text)
}

// appendCompletionSummary appends a compact local summary turn at the end of a
// successful turn when the session changed files the assistant did not already
// name. The summary lists each unmentioned path verbatim plus a one-line
// verification status, so a TUI user reads exactly what changed and how it was
// checked without opening any log or scrolling the transcript.
//
// It is a no-op when no file tracker or session is wired, when no files changed,
// or when the assistant's prose already named every changed path (avoiding a
// duplicate echo of paths the model itself reported). An empty final assistant
// message, however, always produces the summary — that is the silent
// file-creation turn where the model returned no closing prose and the user would
// otherwise see no completion text at all.
func (m *model) appendCompletionSummary(last *message.Message) {
	if m.deps.FileTracker == nil || !m.sessionPersisted || m.sessionID == "" {
		return
	}
	changed, err := m.deps.FileTracker.ChangedFiles(m.ctx, m.sessionID)
	if err != nil || len(changed) == 0 {
		return
	}

	// A path counts as "already mentioned" when its absolute path or basename
	// appears anywhere the user can already read it: the assistant's prose this
	// turn, or any earlier summary already in the transcript. Matching against the
	// whole visible transcript also dedupes across turns, so a file changed in turn
	// one is not re-listed in turn two's summary.
	prose := strings.TrimSpace(assistantText(last))
	seen := prose + "\n" + m.chat.TranscriptText()
	unmentioned := unmentionedPaths(changed, seen)

	// When the model's prose already named every changed file there is nothing the
	// summary would add, so stay quiet — unless the prose was empty, in which case
	// the summary is the only completion text the user gets.
	if len(unmentioned) == 0 && prose != "" {
		return
	}
	if len(unmentioned) == 0 {
		// Empty prose but every path already surfaced earlier (e.g. via a prior
		// summary): fall back to listing all changed files so the silent turn still
		// closes with the paths it touched.
		unmentioned = changed
	}

	summary := completionSummaryText(unmentioned, m.verificationStatus())
	if summary == "" {
		return
	}
	id := m.assistantStreamID()
	m.chat.FinishStream(id)
	// Reindex is intentionally omitted here (see handleRunDone comment): the
	// next turn's assistantStreamID has a different suffix, so no Reindex is
	// needed to give it a fresh bubble, and omitting it keeps the straggler
	// invariant intact.
	m.chat.Append(message.Message{
		ID:      m.nextToolTurnID(),
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: summary}},
	})
}

// unmentionedPaths returns the subset of paths whose absolute form and basename
// are both absent from seen, preserving the input order. A path the assistant
// already named — by full path or by file name — is dropped so the summary never
// echoes what the prose (or an earlier summary) already showed the user.
func unmentionedPaths(paths []string, seen string) []string {
	var out []string
	for _, p := range paths {
		if strings.Contains(seen, p) || strings.Contains(seen, filepath.Base(p)) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// completionSummaryText formats the closing summary: a short lead line, the exact
// changed paths one per line, and a verification status line. It returns "" only
// when there are no paths to report (the caller already guarantees at least one).
func completionSummaryText(paths []string, verify string) string {
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	noun := "file"
	if len(paths) > 1 {
		noun = "files"
	}
	fmt.Fprintf(&b, "Updated %d %s:\n", len(paths), noun)
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	if verify != "" {
		b.WriteString(verify)
	}
	return strings.TrimRight(b.String(), "\n")
}

// verificationStatus derives a one-line note on whether the turn verified its
// own work, read from the session's recorded tool activity. It pairs each
// build/test command with its result and reports the strongest signal: a failure
// if any verification command failed, a pass if at least one succeeded and none
// failed, or a "not verified" hint when no build/test ran at all. The hint keeps
// the agent honest — it never lets a file-changing turn read as done when nothing
// confirmed the change compiles or passes.
func (m *model) verificationStatus() string {
	if m.deps.Sessions == nil {
		return ""
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID)
	if err != nil {
		return ""
	}
	return verificationFromMessages(msgs)
}

// verificationFromMessages scans messages for verification commands (build/test/
// lint/vet runs) and their results, returning a one-line status. It walks in
// order so a tool-use block's command can be paired with the tool-result block
// that follows it. An empty string means no file-changing turn detail to add.
func verificationFromMessages(msgs []message.Message) string {
	ran, passed, failed := false, false, false
	for i := range msgs {
		for _, block := range msgs[i].Content {
			use, ok := block.(message.ToolUseBlock)
			if !ok || !isVerificationCommand(use.Name, use.Input) {
				continue
			}
			ran = true
			if ok, isErr := toolResultOutcome(msgs[i+1:], use.ID); ok {
				if isErr {
					failed = true
				} else {
					passed = true
				}
			}
		}
	}
	switch {
	case failed:
		return "Verification: a build/test command failed — review before relying on this."
	case passed:
		return "Verification: build/test passed."
	case ran:
		return "Verification: build/test ran (outcome not captured)."
	default:
		return "Verification: not run — changes are unverified."
	}
}

// isVerificationCommand reports whether a tool call is a build/test/lint check
// worth surfacing in the completion summary's verification line. It matches the
// common Go verification commands inside a shell tool's "command" argument, plus
// any dedicated build/test tool by name, so the status reflects whether the turn
// actually confirmed its own work.
func isVerificationCommand(name string, input json.RawMessage) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec", "run":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return false
		}
		cmd := strings.ToLower(args.Command)
		for _, needle := range []string{"go build", "go test", "go vet", "golangci-lint", "make test", "make build"} {
			if strings.Contains(cmd, needle) {
				return true
			}
		}
		return false
	case "test", "build", "lint", "check":
		return true
	default:
		return false
	}
}

// toolResultOutcome finds the tool-result block answering useID among the blocks
// that follow it and reports whether it was found and whether it errored. The
// IsError flag is the loop's own success signal; when it is unset the result text
// is scanned for a failing-build/test marker so a non-zero exit reported only in
// the body (some shells return output rather than a flagged error) still counts
// as a failure.
func toolResultOutcome(rest []message.Message, useID string) (found, isErr bool) {
	for i := range rest {
		for _, block := range rest[i].Content {
			res, ok := block.(message.ToolResultBlock)
			if !ok || (useID != "" && res.ToolUseID != useID) {
				continue
			}
			if res.IsError || looksLikeFailure(res.Content) {
				return true, true
			}
			return true, false
		}
	}
	return false, false
}

// looksLikeFailure reports whether tool-result text reads as a failed build or
// test even when the result was not flagged IsError — a guard for shells that
// surface a non-zero exit only in the captured output.
func looksLikeFailure(out string) bool {
	low := strings.ToLower(out)
	for _, marker := range []string{"build failed", "test failed", "--- fail", "\nfail\t", "exit status 1", "exit status 2"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
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
