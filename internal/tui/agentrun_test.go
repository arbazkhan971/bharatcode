package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/arbazkhan971/bharatcode/internal/tui/notification"
	"github.com/charmbracelet/bubbles/v2/spinner"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainText strips ANSI styling and collapses runs of whitespace so content
// assertions hold regardless of glamour's markdown styling and reflow.
func plainText(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// TestSubmitInput_DrivesAgentAndStreamsToChat is the CHANGE 1 contract test: a
// plain prompt must reach the agent loop, and the scripted assistant text plus
// the scripted tool call must appear in the chat's rendered content.
func TestSubmitInput_DrivesAgentAndStreamsToChat(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Reading the file now."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"text":"hi"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "All done with the task."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	h.submit(t, "please run echo")
	h.drain(t, func() bool { return !m.running })

	// Strip ANSI styling and collapse whitespace: assistant prose is now
	// markdown-rendered (glamour), so assert the content reached the chat
	// regardless of styling/reflow.
	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "please run echo", "user prompt must be echoed into the chat")
	require.Contains(t, rendered, "Reading the file now.", "scripted assistant text must reach the chat")
	require.Contains(t, rendered, "All done with the task.", "final scripted assistant text must reach the chat")
	// The tool call now renders as a discrete activity-stream turn led by the
	// tool's action verb (the bare name for an unmapped tool), not the old
	// bracketed "[tool: echo]" marker dumped into the assistant bubble.
	require.NotContains(t, rendered, "[tool: echo]", "tool calls must no longer render as bracket markers")
	require.NotContains(t, rendered, "[done:", "tool results must no longer render as bracket markers")
	require.Regexp(t, regexp.MustCompile(`(?i)\becho\b`), rendered, "scripted tool call must reach the chat as a styled turn")

	require.GreaterOrEqual(t, provider.calls(), 2, "provider must be called once per agent turn")
	require.False(t, m.running, "run must have finished")

	// The prompt must have actually reached the agent loop and been persisted.
	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	require.Equal(t, "please run echo", firstUserText(msgs), "user prompt must be persisted by the agent loop")
}

func TestSubmitInput_IdentityQuestionAnswersLocally(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "wrong provider answer"},
			llm.EndEvent{},
		},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	h.submit(t, "who are you?")

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "who are you?", "user prompt must be echoed into the chat")
	require.Contains(t, rendered, "BharatCode", "local identity answer must name BharatCode")
	require.Contains(t, rendered, "terminal-based AI coding agent")
	require.NotContains(t, rendered, "wrong provider answer")
	require.Equal(t, 0, provider.calls(), "simple identity questions must not call the provider")
	require.False(t, m.running)

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, message.RoleUser, msgs[0].Role)
	require.Equal(t, message.RoleAssistant, msgs[1].Role)
	require.Contains(t, textBlocks(msgs[1]), "BharatCode")
}

// TestEditToolCall_RendersInlineUnifiedDiff is the change-D contract test: a
// scripted edit tool call must surface in the transcript as a tinted unified
// diff (the removed line in the old fragment, the added line in the new), led by
// the "Editing" verb — not as the raw tool arguments or a plain confirmation.
// The inline diff is built from the EventToolCalled arguments, which fire before
// the tool executes, so the edit's own filesystem outcome does not matter here.
func TestEditToolCall_RendersInlineUnifiedDiff(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Applying the edit."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: json.RawMessage(`{"path":"f.go","old_string":"alpha","new_string":"omega"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	h.submit(t, "please edit the file")
	h.drain(t, func() bool { return !m.running })

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "Editing", "an edit call must lead with the Editing verb")
	require.Contains(t, rendered, "alpha", "the removed fragment must appear in the inline diff")
	require.Contains(t, rendered, "omega", "the added fragment must appear in the inline diff")
	require.Contains(t, rendered, "f.go", "the edited file's diff header must appear")
	// The raw argument JSON must never leak into the transcript.
	require.NotContains(t, rendered, "old_string", "raw tool arguments must not render")
	require.NotContains(t, rendered, "new_string", "raw tool arguments must not render")

	// The styled render must carry diff tinting (ANSI), proving the diff routed
	// through the viewer rather than dumping plain text.
	require.Contains(t, m.chat.Render(200), "\x1b[", "the inline diff must be tinted")
}

// TestGoalRun_IteratesUntilCap is the CHANGE 2 contract test: when no
// completion signal is emitted, the outer goal loop iterates and stops exactly
// at the iteration cap.
func TestGoalRun_IteratesUntilCap(t *testing.T) {
	scripts := make([][]llm.Event, 0, maxGoalIterations+2)
	for i := 0; i < maxGoalIterations+2; i++ {
		scripts = append(scripts, []llm.Event{
			llm.DeltaTextEvent{Text: "still working"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		})
	}
	// A large context window keeps the whole transcript in budget so automatic
	// compaction never fires; otherwise its summarization Stream calls would be
	// counted alongside the goal iterations and make this assertion depend on
	// the system-prompt/tool-schema size rather than the iteration cap itself.
	provider := &scriptedProvider{scripts: scripts, contextWindow: 1 << 20}

	h := newAgentHarness(t, provider)
	m := h.model

	m.goal = "make the build pass"
	h.submitSlash(t, "/goal run")
	h.drain(t, func() bool { return !m.goalActive && !m.running })

	require.False(t, m.goalActive, "goal loop must stop at the cap")
	require.Equal(t, maxGoalIterations, provider.calls(), "outer goal loop must iterate exactly the cap number of times")
	require.True(t, m.dialogs.Contains("goal"))
	require.Contains(t, m.dialogs.Render(120), "iteration cap", "user must be told the cap was reached")
}

// TestGoalRun_StopsOnCompletionSignal is the CHANGE 2 goal-met path: the outer
// loop halts before the cap once the agent emits the completion marker.
func TestGoalRun_StopsOnCompletionSignal(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "made progress"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			llm.DeltaTextEvent{Text: goalDoneMarker},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		// Extra script that must NOT be consumed if the loop stops correctly.
		{
			llm.DeltaTextEvent{Text: "should never run"},
			llm.EndEvent{},
		},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	m.goal = "tidy the imports"
	h.submitSlash(t, "/goal run")
	h.drain(t, func() bool { return !m.goalActive && !m.running })

	require.False(t, m.goalActive, "goal loop must stop on completion signal")
	require.Equal(t, 2, provider.calls(), "loop must iterate twice then stop on the completion marker")
	require.Contains(t, m.dialogs.Render(120), "Goal complete")
}

// TestGoalFrame_ReinjectedAcrossTurnsButNotInChat is the backlog #6 contract:
// once a goal is set, every agent turn carries it as an active frame, yet the
// frame never leaks into the user's chat bubble.
func TestGoalFrame_ReinjectedAcrossTurnsButNotInChat(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "turn one"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
		{llm.DeltaTextEvent{Text: "turn two"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
	}}

	h := newAgentHarness(t, provider)
	m := h.model
	m.goal = "keep the build green"

	h.submit(t, "first message")
	h.drain(t, func() bool { return !m.running })

	got := allUserText(provider.lastRequest().Messages)
	require.Contains(t, got, "<active-goal>", "first turn must carry the goal frame to the agent")
	require.Contains(t, got, "keep the build green", "frame must include the goal text")
	require.Contains(t, got, "first message", "user prompt must still reach the agent")

	h.submit(t, "second message")
	h.drain(t, func() bool { return !m.running })

	got2 := allUserText(provider.lastRequest().Messages)
	require.Contains(t, got2, "<active-goal>", "the goal frame must be re-injected on the next turn too")
	require.Contains(t, got2, "second message")

	// The frame is agent-facing only; the chat must show the plain prompts.
	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "first message")
	require.Contains(t, rendered, "second message")
	require.NotContains(t, rendered, "active-goal", "the goal frame must not leak into the chat bubble")
}

// TestGoalFrame_AbsentWhenNoGoal confirms turns are untouched when no goal is
// set, so the frame is strictly opt-in.
func TestGoalFrame_AbsentWhenNoGoal(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "ok"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	h.submit(t, "just a message")
	h.drain(t, func() bool { return !m.running })

	got := allUserText(provider.lastRequest().Messages)
	require.NotContains(t, got, "active-goal", "no goal set means no frame")
	require.Contains(t, got, "just a message")
}

// allUserText concatenates the text of every user message in order, so a test
// can assert what the agent loop actually received this turn.
func allUserText(msgs []message.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		for _, blk := range m.Content {
			if tb, ok := blk.(message.TextBlock); ok {
				b.WriteString(tb.Text)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// --- harness -------------------------------------------------------------

type agentHarness struct {
	model *model
	repo  *session.Repo
	bus   *pubsub.Topic[agent.Event]
	// msgCh receives every tea.Msg produced by background run and listen
	// goroutines launched via startBatch.  drain reads from it to ensure
	// runDoneMsg is never dropped even when loop.Run exceeds listenPollTimeout.
	msgCh chan tea.Msg
}

func newAgentHarness(t *testing.T, provider llm.Provider) *agentHarness {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "tui.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	repo := session.NewRepo(database)

	registry := tools.NewRegistry(tools.Dependencies{WorkDir: t.TempDir()})
	registry.Register(&echoTool{})

	bus := pubsub.NewTopic[agent.Event]("tui_agent_test", 256)
	cfg := &config.Config{
		Models: []config.Model{{ID: "fake-model", Provider: "fake"}},
		Agents: []config.Agent{{Name: "coder", Model: "fake-model"}},
		Ledger: config.LedgerConfig{MaxInrPerMonth: 100},
	}
	perm := permission.New(cfg, pubsub.NewTopic[pubsub.PermissionRequest]("tui_perm_test", 16))

	coord, err := agent.NewCoordinator(cfg, agent.Dependencies{
		Tools:      registry,
		Permission: perm,
		Sessions:   repo,
		Bus:        bus,
		Providers:  map[string]llm.Provider{"fake": provider},
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	loop, err := coord.Agent("coder")
	require.NoError(t, err)

	// A real ledger backed by the test database so the footer-refresh path
	// (used by session restore) reads a genuine summary instead of panicking
	// on a nil backing store.
	ledgerBus := pubsub.NewTopic[rootledger.Summary]("tui_ledger_test", 16)
	t.Cleanup(ledgerBus.Close)
	deps := Dependencies{
		Agent:       loop,
		Coordinator: coord,
		Sessions:    repo,
		Cfg:         cfg,
		Bus:         bus,
		Permission:  perm,
		Ledger:      rootledger.New(database, &cfg.Ledger, cfg.Models, ledgerBus),
		// A real tracker backed by the test database so the completion-summary
		// path (handleRunDone -> ChangedFiles) reads a genuine, empty change set
		// instead of panicking on a Tracker with no backing store.
		FileTracker: filetracker.NewTracker(database, nil),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	h := &agentHarness{
		model: m,
		repo:  repo,
		bus:   bus,
		// Buffer is large enough to hold all events from a goal-loop run plus
		// the runDoneMsg for each iteration.  The scripted provider emits a
		// small fixed number of events per turn, so (maxGoalIterations+2)*16
		// is more than ample.
		msgCh: make(chan tea.Msg, (maxGoalIterations+2)*16),
	}
	// Subscribe to the agent bus exactly as Init would, but for its side effect
	// only: ensureListening sets m.eventCh, which drain reads directly as the
	// SOLE reader.  We discard the returned listen command so no background
	// goroutine ever competes with drain for events on eventCh — concurrent
	// readers would make len(m.eventCh) a lie and reintroduce the dropped-event
	// race this harness exists to avoid.
	_ = m.ensureListening()
	return h
}

// submit feeds a plain prompt through the real input + enter path and launches
// the resulting commands into background goroutines that forward their results
// to h.msgCh.  drain then reads from h.msgCh so runDoneMsg is never lost even
// when loop.Run takes longer than listenPollTimeout.
func (h *agentHarness) submit(t *testing.T, text string) {
	t.Helper()
	h.model.input.WriteString(text)
	_, cmd := h.model.Update(keySpecial("enter", tea.KeyEnter))
	h.startBatch(t, cmd)
}

// submitSlash feeds a slash command through the real input + enter path.  Like
// submit, it routes the run batch through h.msgCh.
func (h *agentHarness) submitSlash(t *testing.T, text string) {
	t.Helper()
	h.model.input.WriteString(text)
	_, cmd := h.model.Update(keySpecial("enter", tea.KeyEnter))
	h.startBatch(t, cmd)
}

// startBatch evaluates cmd and launches the blocking RUN command into a
// background goroutine that forwards its terminal runDoneMsg to h.msgCh.  A
// startRun batch is [runCmd, listenCmd]; continueRun returns a bare runCmd.  The
// listen command is deliberately NOT launched: drain is the sole reader of
// m.eventCh, so spawning a listener here would steal events and make the
// len(m.eventCh) termination check unreliable.  Any agentEventMsg that does
// surface (it never should, since the listener is dropped) is discarded rather
// than forwarded, keeping h.msgCh a pure runDoneMsg/continuation channel.
func (h *agentHarness) startBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	// Batch commands return instantaneously (they just wrap their sub-commands);
	// run commands block for the whole turn.  Evaluate cmd() in a goroutine with
	// a short peek so a blocking run command does not stall the caller, then
	// route only run/continuation results onward.
	peeked := make(chan tea.Msg, 1)
	go func() { peeked <- cmd() }()

	select {
	case msg := <-peeked:
		h.dispatchBatchResult(msg)
	case <-time.After(listenPollTimeout):
		// cmd() is still blocking (loop.Run).  Hand it to a goroutine that
		// forwards the eventual runDoneMsg via msgCh.
		go func() { h.dispatchBatchResult(<-peeked) }()
	}
}

// dispatchBatchResult routes one command result.  A startRun batch is
// constructed as tea.Batch(runCmd, ensureListening()); tea.Batch preserves the
// argument order in the resulting BatchMsg slice (compactCmds only filters
// nils), so the run command is always the first element and the listen command
// the second.  We launch ONLY the run command — the listen command is dropped
// because drain reads m.eventCh directly as the sole reader.  Launching the
// listener here would race drain for events and make the len(m.eventCh)
// termination check unreliable.  A bare runDoneMsg (from continueRun) is
// forwarded directly.
func (h *agentHarness) dispatchBatchResult(msg tea.Msg) {
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		if len(batch) == 0 {
			return
		}
		runCmd := batch[0]
		go func() {
			if result := runCmd(); result != nil {
				h.msgCh <- result
			}
		}()
		return
	}
	// A non-batch result is the run command's terminal runDoneMsg.
	h.msgCh <- msg
}

// listenPollTimeout bounds a single listen command in tests. Real agent events
// arrive in microseconds; a timeout means the bus is momentarily quiet and the
// caller should loop back to re-check its stop condition.
const listenPollTimeout = 100 * time.Millisecond

// run executes one command (and any batched sub-commands), feeding each
// produced message back into Update and recursively pumping the results until
// the command chain naturally drains (a listen command finds the bus quiet).
func (h *agentHarness) run(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	h.pump(t, cmd)
}

func (h *agentHarness) pump(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := execWithTimeout(cmd, listenPollTimeout)
	if msg == nil {
		return
	}
	// The streaming spinner's Tick is a self-perpetuating cosmetic timer: each
	// TickMsg yields another Tick while a turn runs. In production bubbletea
	// schedules those off the synchronous path, but this test pump runs commands
	// inline and would recurse on ticks forever, starving real run progress.
	// Apply the tick (so the spinner state advances) but do NOT pump its
	// follow-up Tick — it carries no run-progress and never terminates.
	if _, ok := msg.(spinner.TickMsg); ok {
		h.model.Update(msg)
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			h.pump(t, sub)
		}
		return
	}
	_, next := h.model.Update(msg)
	h.pump(t, next)
}

// drain drives the model forward until done() reports true AND every agent
// event published for the turn(s) has been rendered into the chat.
//
// drain is the SOLE reader of m.eventCh.  Because loop.Run publishes all of a
// turn's events into the bus (a buffered channel) BEFORE it returns, the
// runDoneMsg that flips m.running can arrive while trailing events are still
// buffered in m.eventCh, undrained.  Stopping on done() alone therefore races:
// it can quit before "All done with the task." (the final turn's text) reaches
// the chat.  The real terminator is "the run is done AND m.eventCh is empty" —
// authoritative precisely because nothing else reads m.eventCh concurrently.
//
// Events are read straight from m.eventCh and fed through Update so the genuine
// integration surface (handleAgentEvent -> chat) runs; the listen command it
// returns is discarded (drain owns the channel).  Run lifecycle messages
// (runDoneMsg) and goal-loop continuations arrive on h.msgCh from startBatch.
func (h *agentHarness) drain(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if done() && len(h.model.eventCh) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("drain timed out; running=%v goalActive=%v buffered=%d",
				h.model.running, h.model.goalActive, len(h.model.eventCh))
		}
		select {
		case ev := <-h.model.eventCh:
			// Feed the event through the real Update path; drop the listen
			// command it returns since drain reads m.eventCh itself.
			_, _ = h.model.Update(agentEventMsg(ev))
		case msg := <-h.msgCh:
			_, next := h.model.Update(msg)
			if next != nil {
				// continueRun (goal loop): keep the run pipeline alive. Its
				// follow-up runDoneMsg returns on h.msgCh via startBatch.
				h.startBatch(t, next)
			}
		case <-time.After(listenPollTimeout):
			// Both channels momentarily quiet (e.g. loop.Run still running and
			// has not published yet); re-check the terminator and loop.
		}
	}
}

// execWithTimeout runs cmd in a goroutine and returns its message, or nil if it
// does not complete within d (e.g. a listen command waiting on a quiet bus).
func execWithTimeout(cmd tea.Cmd, d time.Duration) tea.Msg {
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case msg := <-out:
		return msg
	case <-time.After(d):
		return nil
	}
}

func firstUserText(msgs []message.Message) string {
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tb, ok := b.(message.TextBlock); ok {
				return tb.Text
			}
		}
	}
	return ""
}

func textBlocks(msg message.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if tb, ok := block.(message.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}

// --- scripted provider ---------------------------------------------------

type scriptedProvider struct {
	mu      sync.Mutex
	scripts [][]llm.Event
	callN   int
	lastReq llm.Request
	// contextWindow overrides the reported model context window when > 0.
	// Tests that must not trip automatic compaction (which would issue extra
	// summarization Stream calls and consume scripts) set this large so the
	// history always fits regardless of system-prompt or tool-schema size.
	contextWindow int
}

func (p *scriptedProvider) Name() string { return "fake" }

func (p *scriptedProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	p.callN++
	p.lastReq = req
	var events []llm.Event
	if len(p.scripts) > 0 {
		events = p.scripts[0]
		p.scripts = p.scripts[1:]
	}
	p.mu.Unlock()

	ch := make(chan llm.Event, len(events)+1)
	go func() {
		defer close(ch)
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (p *scriptedProvider) Models() []llm.Model {
	// Default to a large window so tests that do not deliberately exercise
	// compaction are not coupled to the byte size of the built-in tool
	// descriptions (which feed the system prompt). Tests that need a tight
	// window set contextWindow explicitly.
	window := 1 << 20
	if p.contextWindow > 0 {
		window = p.contextWindow
	}
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: window, SupportsTools: true}}
}

func (p *scriptedProvider) SupportsTools() bool  { return true }
func (p *scriptedProvider) SupportsImages() bool { return false }

func (p *scriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callN
}

// lastRequest returns a copy of the most recent request the loop sent to the
// provider, letting a test inspect the history the agent actually transmitted
// (e.g. to confirm compaction shrank it).
func (p *scriptedProvider) lastRequest() llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastReq
}

// echoTool is a minimal real tool so EventToolCalled fires for the scripted
// tool call (the loop skips the event when the tool is unregistered).
type echoTool struct{}

func (e *echoTool) Name() string            { return "echo" }
func (e *echoTool) Description() string     { return "Echo the provided text." }
func (e *echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (e *echoTool) Run(_ context.Context, args json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: string(args)}, nil
}

// --- notification tests --------------------------------------------------

// countingNotifier records how many notifications were dispatched.
type countingNotifier struct {
	mu    sync.Mutex
	calls []struct{ title, body string }
}

func (c *countingNotifier) Notify(title, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, struct{ title, body string }{title, body})
	return nil
}

func (c *countingNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// TestRunDone_NotifiesWhenUnfocused verifies that completing a turn while the
// terminal is out of focus dispatches a desktop notification — matching the
// Claude Code / opencode parity behaviour.
func TestRunDone_NotifiesWhenUnfocused(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Here is your answer."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	h := newAgentHarness(t, provider)

	// Swap in a counting notifier and simulate the terminal losing focus.
	rec := &countingNotifier{}
	notif := notification.NewFocusAware(rec)
	notif.SetFocused(false)
	h.model.notifications = notif

	h.submit(t, "give me an answer")
	h.drain(t, func() bool { return !h.model.running })

	require.Equal(t, 1, rec.count(), "expected one notification after the turn completed")
}

// TestRunDone_NoNotifyWhenFocused verifies that when the terminal still has focus
// no notification is dispatched — the FocusAware wrapper must suppress it.
func TestRunDone_NoNotifyWhenFocused(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Here is your answer."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	h := newAgentHarness(t, provider)

	rec := &countingNotifier{}
	// focused is true by default in NewFocusAware — no SetFocused(false) call.
	h.model.notifications = notification.NewFocusAware(rec)

	h.submit(t, "give me an answer")
	h.drain(t, func() bool { return !h.model.running })

	require.Equal(t, 0, rec.count(), "expected no notification when terminal is focused")
}

// TestTurnNotifyBody covers the body-extraction helper across edge cases.
func TestTurnNotifyBody(t *testing.T) {
	t.Parallel()

	msg := func(text string) *message.Message {
		return &message.Message{
			Role:    message.RoleAssistant,
			Content: []message.ContentBlock{message.TextBlock{Text: text}},
		}
	}

	require.Equal(t, "Turn complete", turnNotifyBody(nil))
	require.Equal(t, "Turn complete", turnNotifyBody(msg("")))
	require.Equal(t, "Turn complete", turnNotifyBody(msg("   ")))
	require.Equal(t, "First line only", turnNotifyBody(msg("First line only\nmore here")))
	require.Equal(t, "single line", turnNotifyBody(msg("single line")))

	longLine := strings.Repeat("x", 120)
	got := turnNotifyBody(msg(longLine))
	require.LessOrEqual(t, len(got), 100)
	require.True(t, strings.HasSuffix(got, "..."))
}

func TestTurnNotifyBodyFromMessagesFallsBackToToolResult(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role:    message.RoleAssistant,
			Content: []message.ContentBlock{message.TextBlock{Text: ""}},
		},
		{
			Role: message.RoleTool,
			Content: []message.ContentBlock{message.ToolResultBlock{
				Content: "created /tmp/work/notes/new.txt (6 bytes)\n\ndiagnostics passed",
			}},
		},
	}

	require.Equal(t, "created /tmp/work/notes/new.txt (6 bytes)", turnNotifyBodyFromMessages(msgs))
}

// TestFriendlyRunError_AuthGetsHint asserts a missing-credentials error (one
// wrapping llm.ErrAuth) is rewritten with an actionable hint pointing at /model
// and key setup, while the provider's specific message is preserved and a
// non-auth error is returned unchanged.
func TestFriendlyRunError_AuthGetsHint(t *testing.T) {
	t.Parallel()

	authErr := fmt.Errorf("calling provider: no API key for deepseek: set DEEPSEEK_API_KEY or run 'bharatcode login deepseek': %w", llm.ErrAuth)
	got := friendlyRunError(authErr)
	require.Contains(t, got, "no API key for deepseek", "the provider's specific remedy must be kept")
	require.Contains(t, got, "/model", "an auth error must point the user at the model picker")
	require.Contains(t, strings.ToLower(got), "key", "an auth error must mention setting a key")

	plain := errors.New("boom: network unreachable")
	require.Equal(t, "boom: network unreachable", friendlyRunError(plain),
		"a non-auth error must be returned verbatim")

	require.Empty(t, friendlyRunError(nil), "a nil error yields an empty string")
}

// TestEventRunError_AuthRendersFriendlyHint asserts that when the agent emits a
// run error wrapping ErrAuth, the chat shows the actionable hint rather than a
// bare "authentication failed", and marks the turn as having surfaced an error.
func TestEventRunError_AuthRendersFriendlyHint(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	authErr := fmt.Errorf("calling provider: no API key for deepseek: set DEEPSEEK_API_KEY: %w", llm.ErrAuth)
	m.handleAgentEvent(agentEventMsg(agent.Event{Kind: agent.EventRunError, Err: authErr}))

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "no API key for deepseek", "the auth error detail must reach the chat")
	require.Contains(t, rendered, "/model", "the auth error must surface the /model hint inline")
	require.True(t, m.turnErrShown, "rendering a run error must mark the turn as already surfaced")
}

// TestHandleRunDone_SurfacesUnpublishedError asserts that a turn error which was
// NOT already shown inline (e.g. a Run path that returns without publishing an
// EventRunError) is surfaced in the chat, so the failure is never silently
// swallowed.
func TestHandleRunDone_SurfacesUnpublishedError(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.turnErrShown = false
	m.handleRunDone(runDoneMsg{err: errors.New("appending user message: disk full")})

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "disk full", "an unpublished run error must still reach the chat")
}

// TestHandleRunDone_QuietOnCancel asserts that a user interrupt (context
// cancellation) is not reported as a fault: the chat stays free of an error
// line, since the cancellation was intentional.
func TestHandleRunDone_QuietOnCancel(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.turnErrShown = false
	m.handleRunDone(runDoneMsg{err: fmt.Errorf("calling provider: %w", context.Canceled)})

	rendered := plainText(m.chat.Render(200))
	require.NotContains(t, rendered, "[error:", "a user cancellation must not render an error line")
}

// TestHandleRunDone_NoDoubleReport asserts that an error already surfaced inline
// (turnErrShown set by EventRunError) is not re-reported by handleRunDone, so the
// user does not see the same failure twice.
func TestHandleRunDone_NoDoubleReport(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.turnErrShown = true
	m.handleRunDone(runDoneMsg{err: fmt.Errorf("calling provider: %w", llm.ErrAuth)})

	rendered := plainText(m.chat.Render(200))
	require.NotContains(t, rendered, "[error:", "handleRunDone must not duplicate an inline-reported error")
}

// --- completion summary (T7) ---------------------------------------------

// trackedModel returns a model wired to a real session repo and a real file
// tracker that share one test database, plus the persisted session id. The
// completion-summary path (appendCompletionSummary -> ChangedFiles, and the
// verification scan -> Sessions.Messages) reads genuine rows this way rather than
// a stub, so a recorded write and a recorded tool result both surface.
func trackedModel(t *testing.T) (*model, string) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "summary.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	repo := session.NewRepo(database)
	s := &session.Session{Title: "summary", Model: "fake-model", Agent: "coder"}
	require.NoError(t, repo.Create(context.Background(), s))

	m := newSizedModel(t)
	m.deps.Sessions = repo
	m.deps.FileTracker = filetracker.NewTracker(database, nil)
	m.sessionID = s.ID
	m.sessionPersisted = true
	return m, s.ID
}

// TestAppendCompletionSummary_ListsUnmentionedPaths asserts that when a turn
// changed a file the assistant never named, the closing summary lists the exact
// path and a verification line, so a TUI user reads the output path without any
// log.
func TestAppendCompletionSummary_ListsUnmentionedPaths(t *testing.T) {
	m, sid := trackedModel(t)
	path := filepath.Join(t.TempDir(), "notes", "new.txt")
	_, err := m.deps.FileTracker.RecordWrite(context.Background(), sid, path, nil, []byte("hi"))
	require.NoError(t, err)

	m.appendCompletionSummary(&message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "Done."}},
	})

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, path, "the exact changed path must reach the transcript")
	require.Contains(t, rendered, "Updated 1 file", "the summary must lead with the change count")
	require.Contains(t, rendered, "not run", "no build/test ran, so the summary must flag the change as unverified")
}

// TestAppendCompletionSummary_SuppressedWhenProseNamesPath asserts the summary
// stays quiet when the assistant's own prose already named the changed file —
// the path is not echoed a second time.
func TestAppendCompletionSummary_SuppressedWhenProseNamesPath(t *testing.T) {
	m, sid := trackedModel(t)
	path := filepath.Join(t.TempDir(), "main.go")
	_, err := m.deps.FileTracker.RecordWrite(context.Background(), sid, path, nil, []byte("package main"))
	require.NoError(t, err)

	// Seed the visible transcript with the assistant prose that named the file,
	// then pass that same message as the turn's final answer.
	last := &message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "I created " + path + " for you."}},
	}
	m.chat.Stream("a-final", "I created "+path+" for you.")
	m.chat.FinishStream("a-final")

	before := plainText(m.chat.Render(200))
	m.appendCompletionSummary(last)
	after := plainText(m.chat.Render(200))

	require.Equal(t, before, after, "the summary must not echo a path the prose already named")
	require.NotContains(t, after, "Updated 1 file", "no summary block should be appended")
}

// TestAppendCompletionSummary_EmptyProseStillSummarizes asserts that a silent
// file-creation turn — the assistant returned no closing prose — still ends with
// useful completion text naming the changed path.
func TestAppendCompletionSummary_EmptyProseStillSummarizes(t *testing.T) {
	m, sid := trackedModel(t)
	path := filepath.Join(t.TempDir(), "out.txt")
	_, err := m.deps.FileTracker.RecordWrite(context.Background(), sid, path, nil, []byte("data"))
	require.NoError(t, err)

	m.appendCompletionSummary(&message.Message{Role: message.RoleAssistant})

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, path, "an empty final answer must still surface the changed path")
	require.Contains(t, rendered, "Updated 1 file")
}

// TestVerificationFromMessages covers the build/test status derivation across the
// pass, fail, and not-run cases the summary's verification line reports.
func TestVerificationFromMessages(t *testing.T) {
	t.Parallel()

	bashCall := func(id, cmd string) message.Message {
		return message.Message{Role: message.RoleAssistant, Content: []message.ContentBlock{
			message.ToolUseBlock{ID: id, Name: "bash", Input: json.RawMessage(`{"command":"` + cmd + `"}`)},
		}}
	}
	result := func(id, out string, isErr bool) message.Message {
		return message.Message{Role: message.RoleTool, Content: []message.ContentBlock{
			message.ToolResultBlock{ToolUseID: id, Content: out, IsError: isErr},
		}}
	}

	// No verification command at all.
	require.Contains(t, verificationFromMessages([]message.Message{bashCall("c1", "ls")}), "not run")

	// A passing build.
	pass := []message.Message{bashCall("c1", "go build ./..."), result("c1", "ok", false)}
	require.Contains(t, verificationFromMessages(pass), "passed")

	// A failing test, flagged by IsError.
	fail := []message.Message{bashCall("c1", "go test ./..."), result("c1", "FAIL", true)}
	require.Contains(t, verificationFromMessages(fail), "failed")

	// A failure surfaced only in the body (non-zero exit echoed, not flagged).
	bodyFail := []message.Message{bashCall("c1", "go test ./..."), result("c1", "--- FAIL: TestX\nexit status 1", false)}
	require.Contains(t, verificationFromMessages(bodyFail), "failed")
}

// TestUnmentionedPaths asserts a path is dropped when the seen text names it by
// full path or by basename, and kept otherwise, preserving order.
func TestUnmentionedPaths(t *testing.T) {
	t.Parallel()

	paths := []string{"/repo/a.go", "/repo/b.go", "/repo/c.go"}
	got := unmentionedPaths(paths, "I edited /repo/a.go and touched b.go")
	require.Equal(t, []string{"/repo/c.go"}, got, "named paths (by full path or basename) must be dropped, the rest kept in order")
	require.Nil(t, unmentionedPaths(paths, "/repo/a.go /repo/b.go /repo/c.go"), "all-named yields nil")
}
