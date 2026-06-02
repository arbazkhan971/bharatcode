package tui

import (
	"context"
	"encoding/json"
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
	require.Contains(t, rendered, "[tool: echo]", "scripted tool call must reach the chat")

	require.GreaterOrEqual(t, provider.calls(), 2, "provider must be called once per agent turn")
	require.False(t, m.running, "run must have finished")

	// The prompt must have actually reached the agent loop and been persisted.
	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	require.Equal(t, "please run echo", firstUserText(msgs), "user prompt must be persisted by the agent loop")
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
	provider := &scriptedProvider{scripts: scripts}

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
		Sessions:    repo,
		Cfg:         cfg,
		Bus:         bus,
		Permission:  perm,
		Ledger:      rootledger.New(database, &cfg.Ledger, cfg.Models, ledgerBus),
		FileTracker: &filetracker.Tracker{},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	// Start the listen loop exactly as Init would.
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
	h.run(t, m.ensureListening())
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

// startBatch evaluates cmd and launches every resulting sub-command in its own
// goroutine, forwarding each non-nil result to h.msgCh.  For a tea.BatchMsg
// every sub-command runs concurrently; for a plain cmd a single goroutine is
// used.  cmd() is never called in the caller's goroutine: this prevents a
// blocking run or listen command from stalling the drain loop.
func (h *agentHarness) startBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	// Call cmd() in a goroutine and immediately redirect the result.  Batch
	// commands return instantaneously (they just wrap their args); run and
	// listen commands block for variable durations.
	peeked := make(chan tea.Msg, 1)
	go func() { peeked <- cmd() }()

	select {
	case msg := <-peeked:
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			// Expand the batch: launch each sub-command concurrently.
			for _, sub := range batch {
				c := sub
				go func() {
					if result := c(); result != nil {
						h.msgCh <- result
					}
				}()
			}
			return
		}
		// Plain (non-batch) result arrived immediately: forward it.
		h.msgCh <- msg
	case <-time.After(listenPollTimeout):
		// cmd() is still blocking (loop.Run or listenAgent).  Let the existing
		// goroutine complete on its own and forward via msgCh when it does.
		go func() {
			if result := <-peeked; result != nil {
				h.msgCh <- result
			}
		}()
	}
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
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			h.pump(t, sub)
		}
		return
	}
	_, next := h.model.Update(msg)
	h.pump(t, next)
}

// drain drives the model forward until done() returns true or the overall
// deadline passes.  It reads exclusively from h.msgCh, which is the single
// delivery point for both runDoneMsg (from the run goroutine) and agentEventMsg
// (from the listen goroutine chain started by startBatch).  Reading only from
// msgCh — never creating additional competing goroutines via pump — ensures no
// event is stolen and every streaming fragment reaches the chat model.
//
// When Update returns a command (e.g. continueRun for the goal loop), drain
// routes it through startBatch so the next runDoneMsg also appears on msgCh.
func (h *agentHarness) drain(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("drain timed out; running=%v goalActive=%v", h.model.running, h.model.goalActive)
		}
		select {
		case msg := <-h.msgCh:
			_, next := h.model.Update(msg)
			if next != nil {
				// continueRun (goal loop) or listenAgent (event chain):
				// keep the goroutine pipeline alive.
				h.startBatch(t, next)
			}
		case <-time.After(listenPollTimeout):
			// msgCh quiet; re-check done() and loop.
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

// --- scripted provider ---------------------------------------------------

type scriptedProvider struct {
	mu      sync.Mutex
	scripts [][]llm.Event
	callN   int
	lastReq llm.Request
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
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: 8192, SupportsTools: true}}
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
