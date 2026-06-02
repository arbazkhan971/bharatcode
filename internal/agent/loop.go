package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

const defaultMaxSteps = 50

// planModePrompt is appended to the system prompt while the Loop runs in plan
// mode. It instructs the agent to investigate read-only and produce a
// step-by-step plan for approval instead of executing changes, mirroring the
// read-only tool restriction enforced by toolAllowed.
const planModePrompt = `

# Plan Mode (read-only)

You are operating in PLAN MODE. You may ONLY use read-only tools to investigate;
file-mutating and command-running tools (write, edit, bash, and similar) are
disabled and will be refused. Do NOT attempt to make changes. Instead, produce a
clear, step-by-step PLAN describing exactly what you would do, then stop and wait
for the user to approve it. Execution only begins after the plan is approved.`

// writeClassTools names the file-mutating tools whose successful runs fire a
// FileEdit lifecycle hook. The agent loop detects mutations by tool name so it
// stays decoupled from the tools package's concrete implementations.
var writeClassTools = map[string]struct{}{
	"write":     {},
	"edit":      {},
	"multiedit": {},
}

// hookFirer is the subset of *hooks.Engine the agent loop consumes to fire
// lifecycle hooks. It is an interface so tests can inject a capturing fake via
// the Config; *hooks.Engine satisfies it. A nil hookFirer means no hooks are
// configured and every Fire call is skipped.
type hookFirer interface {
	Fire(ctx context.Context, event hooks.Event, payload any) (hooks.Decision, error)
}

// Config bundles the dependencies a Loop needs.
type Config struct {
	Name          string
	Model         string
	Provider      llm.Provider
	Tools         toolSource
	Permission    *permission.Checker
	Sessions      *session.Repo
	FileTracker   *filetracker.Tracker
	Ledger        *ledger.Ledger
	Bus           *pubsub.Topic[Event]
	Hooks         hookFirer
	SystemPrompt  string
	ToolAllowList []string
	MaxSteps      int
	// PlanMode starts the Loop in plan mode: the agent is restricted to
	// read-only tools and is prompted to output a step-by-step plan rather than
	// execute changes. Approve transitions out of plan mode so execution tools
	// become available again. It defaults to false (normal execution), which is
	// the non-breaking default.
	PlanMode bool
	// ToolResultMaxBytes caps the byte length of a single tool result before it
	// is appended to the conversation history, so one oversized output (a giant
	// file read, verbose bash output) cannot blow the context window. A
	// non-positive value selects defaultToolResultMaxBytes. Error results are
	// never truncated.
	ToolResultMaxBytes int
	// Compactor condenses the conversation before it is sent to the provider
	// when Compact is invoked. When nil, a default drop-and-mark Compactor is
	// used.
	Compactor Compactor
	// Router selects which configured model to use for each turn, enabling
	// cost-aware routing of cheap models to simple turns and stronger models to
	// complex ones. When nil, no routing occurs and the Loop always uses Model,
	// which is the default, non-breaking behavior.
	Router Router
	// RouteHint is an explicit complexity override forwarded to Router on every
	// turn this Loop runs. It is set once at construction (a Loop serves many
	// Run calls), so it pins the same hint for the Loop's lifetime rather than
	// varying per turn. It defaults to ComplexityUnset, leaving the Router to
	// derive complexity from each turn. It is ignored when Router is nil.
	RouteHint TurnComplexity
}

// Loop runs a single named agent for one session at a time.
type Loop struct {
	cfg       Config
	name      string
	runMu     sync.Mutex
	cancelMu  sync.Mutex
	cancelRun context.CancelFunc
	allowed   map[string]struct{}

	// compactMu guards the in-memory compaction snapshot below. Run reads it
	// and Compact writes it, potentially from different goroutines.
	compactMu sync.Mutex
	// compacted holds the condensed history produced by the most recent
	// Compact call. It is nil when no compaction has occurred. It lives only in
	// memory and is never written to the on-disk session.
	compacted []message.Message
	// compactedLen records the on-disk message count at compaction time, so Run
	// can graft messages that arrived after compaction onto the snapshot.
	compactedLen int

	// steerMu guards the steering queue below. Steer writes it (possibly from a
	// different goroutine than Run), and Run drains it at safe boundaries.
	steerMu sync.Mutex
	// steerQueue holds user steering messages queued mid-run via Steer. Run
	// drains them at the top of each step so they reach the provider as the next
	// user messages without restarting the turn.
	steerQueue []string
	// running reports whether a Run is currently in flight. Steer reads it to
	// tell the caller whether the steering text was queued onto a live turn or
	// must be started as a fresh prompt.
	running bool

	// backoff drives the retry schedule for transient provider failures. It is
	// defaulted in New and overridden in-package by tests for determinism.
	backoff llm.Backoff
	// sleep waits between provider retries, honouring context cancellation.
	// Production uses contextSleep; tests inject a no-op recorder so retries do
	// not sleep for real.
	sleep sleepFunc

	// activeModel is the model resolved for the current turn. It is set once at
	// the top of Run (from the configured Router, or cfg.Model when no Router is
	// set) and read by every per-step provider call so the whole turn uses one
	// model. Run holds runMu for its entire duration and rejects concurrent
	// runs, so no further synchronization is needed.
	activeModel string

	// planMode reports whether the Loop is currently restricted to read-only
	// tools and prompted to produce a plan. It is initialised from cfg.PlanMode
	// and cleared by Approve. It is atomic because Approve may be called from a
	// different goroutine than the in-flight Run that reads it.
	planMode atomic.Bool
}

// New constructs a Loop from cfg.
func New(cfg Config) *Loop {
	if cfg.Name == "" {
		cfg.Name = "coder"
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.Provider == nil {
		panic("agent: provider is nil")
	}
	if cfg.Tools == nil {
		panic("agent: tools registry is nil")
	}
	if cfg.Sessions == nil {
		panic("agent: sessions repo is nil")
	}

	allowed := make(map[string]struct{}, len(cfg.ToolAllowList))
	for _, name := range cfg.ToolAllowList {
		if _, ok := cfg.Tools.Get(name); !ok {
			panic("agent: allowed tool is not registered: " + name)
		}
		allowed[name] = struct{}{}
	}
	l := &Loop{
		cfg:         cfg,
		name:        cfg.Name,
		allowed:     allowed,
		backoff:     llm.Backoff{},
		sleep:       contextSleep,
		activeModel: cfg.Model,
	}
	l.planMode.Store(cfg.PlanMode)
	return l
}

// Name returns the configured agent name.
func (l *Loop) Name() string {
	return l.name
}

// PlanMode reports whether the Loop is currently restricted to read-only tools
// and prompted to produce a plan.
func (l *Loop) PlanMode() bool {
	return l.planMode.Load()
}

// SetPlanMode turns plan mode on or off at runtime. Turning it on restricts the
// next provider call to read-only tools and appends the plan-mode prompt;
// turning it off (equivalent to Approve) restores execution tools. It takes
// effect on the next provider call and is safe to call from any goroutine, so a
// UI can toggle plan mode on a live Loop without recreating it (which would lose
// session state). Approve remains the dedicated "exit plan mode" affordance.
func (l *Loop) SetPlanMode(on bool) {
	l.planMode.Store(on)
}

// Approve transitions the Loop out of plan mode so execution tools (write, edit,
// bash, and similar) become available again and the plan-mode prompt is no
// longer appended. It takes effect on the next provider call. Approve is safe to
// call from any goroutine; calling it when not in plan mode is a no-op.
func (l *Loop) Approve() {
	l.planMode.Store(false)
}

// Interrupt cancels an in-flight Run.
func (l *Loop) Interrupt() {
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	if l.cancelRun != nil {
		l.cancelRun()
	}
}

// Steer queues text as a steering message for the in-flight Run. When a Run is
// active, the text is appended to the conversation as the next user message at
// the next safe boundary (after the current tool batch, before the next
// provider call), so the agent course-corrects without a full restart. It
// returns true when a Run was in flight and the message was queued onto it, and
// false when no Run was active; in the latter case the caller should start the
// text as a fresh turn. Steer is safe to call from any goroutine.
func (l *Loop) Steer(text string) (queued bool) {
	if text == "" {
		return false
	}
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if !l.running {
		return false
	}
	l.steerQueue = append(l.steerQueue, text)
	return true
}

// drainSteering removes and returns all queued steering messages, converting
// each into a user message bound to sessionID. It returns nil when the queue is
// empty.
func (l *Loop) drainSteering(sessionID string) []message.Message {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if len(l.steerQueue) == 0 {
		return nil
	}
	out := make([]message.Message, 0, len(l.steerQueue))
	for _, text := range l.steerQueue {
		out = append(out, textMessage(sessionID, message.RoleUser, text))
	}
	l.steerQueue = l.steerQueue[:0]
	return out
}

// hasSteering reports whether any steering messages are queued.
func (l *Loop) hasSteering() bool {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	return len(l.steerQueue) > 0
}

// PendingSteering drains and returns any steering messages that were queued but
// not consumed by a Run (for example, text that arrived between the loop's
// final steering check and the run mutex releasing). The TUI uses this after a
// turn finishes to start the leftover text as a fresh prompt so no steering
// message is lost. It returns the raw queued text in order.
func (l *Loop) PendingSteering() []string {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if len(l.steerQueue) == 0 {
		return nil
	}
	out := make([]string, len(l.steerQueue))
	copy(out, l.steerQueue)
	l.steerQueue = l.steerQueue[:0]
	return out
}

// Compact condenses the session's conversation in memory so the next provider
// request sends a smaller history. It loads the current on-disk history, runs
// it through the configured Compactor (or the default drop-and-mark Compactor),
// and stores the condensed result as an in-memory snapshot. Compaction never
// mutates the on-disk session: it only changes what Run sends to the provider
// on subsequent turns, exactly like truncateForContext.
//
// The system prompt is preserved automatically because it is carried in the
// Config and passed to the provider separately, never within the history. The
// most recent genuine user message is preserved explicitly: if the Compactor
// drops it, Compact re-appends it so the model always retains the live prompt.
func (l *Loop) Compact(ctx context.Context, sessionID string) error {
	history, err := l.cfg.Sessions.Messages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("loading session messages: %w", err)
	}

	condensed, err := l.compactHistory(ctx, history)
	if err != nil {
		return err
	}

	l.compactMu.Lock()
	l.compacted = condensed
	l.compactedLen = len(history)
	l.compactMu.Unlock()

	return nil
}

// compactHistory runs history through the configured Compactor (or the default
// drop-and-mark Compactor) and enforces the preserve-latest-user invariant: if
// the Compactor dropped the most recent genuine user message, it is re-appended
// so the live prompt is never lost. The system prompt is preserved
// automatically because it is carried in the Config and sent to the provider
// separately, never within the history. The returned slice is a fresh copy.
func (l *Loop) compactHistory(ctx context.Context, history []message.Message) ([]message.Message, error) {
	compactor := l.cfg.Compactor
	if compactor == nil {
		compactor = newDropAndMarkCompactor(2)
	}

	input := append([]message.Message(nil), history...)
	condensed, err := compactor.Compact(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("compacting history: %w", err)
	}
	condensed = append([]message.Message(nil), condensed...)

	// Preserve the most recent genuine user message: if the Compactor dropped
	// it, re-append it so the live prompt is never lost.
	if idx := latestUserIndex(history); idx >= 0 {
		latest := history[idx]
		if !containsMessage(condensed, latest) {
			condensed = append(condensed, latest)
		}
	}
	return condensed, nil
}

// applyCompaction grafts messages that arrived after the last Compact call onto
// the condensed snapshot. When no compaction has occurred it returns history
// unchanged, so the normal turn path is unaffected.
func (l *Loop) applyCompaction(history []message.Message) []message.Message {
	l.compactMu.Lock()
	defer l.compactMu.Unlock()
	if l.compacted == nil {
		return history
	}
	out := append([]message.Message(nil), l.compacted...)
	if l.compactedLen <= len(history) {
		out = append(out, history[l.compactedLen:]...)
	}
	return out
}

// fitHistory ensures history fits the model's usable context window before it
// is sent to the provider. When the window is unknown (zero), it preserves the
// historical behavior of leaving history untouched. When history already fits,
// it is returned unchanged.
//
// On overflow it first invokes the Compactor to SUMMARIZE the conversation
// (preserving the system prompt automatically and the latest genuine user
// message explicitly) rather than hard-dropping. If the compacted history fits,
// it is used. If compaction does not free enough room, it falls back to
// drop-oldest truncation so the turn still proceeds.
//
// If the latest genuine user message alone exceeds the usable window, no
// strategy can make the turn fit; fitHistory returns ErrContextOverflow rather
// than looping or silently sending an over-window request.
func (l *Loop) fitHistory(ctx context.Context, history []message.Message) ([]message.Message, error) {
	window := l.contextWindow()
	if window <= 0 {
		// Unknown window: keep the prior behavior of sending history as-is.
		return history, nil
	}

	budget := fitBudget(window, l.cfg.SystemPrompt)
	if fitsBudget(history, budget) {
		return history, nil
	}

	// The latest genuine user message must always survive a fit. If it alone
	// exceeds the budget, no compaction or drop-oldest can rescue the turn.
	if idx := latestUserIndex(history); idx >= 0 {
		if estimateMessageTokens(history[idx]) > budget {
			return nil, ErrContextOverflow
		}
	}

	// Try compaction first: summarize rather than hard-drop.
	condensed, err := l.compactHistory(ctx, history)
	if err != nil {
		return nil, err
	}
	if fitsBudget(condensed, budget) {
		return condensed, nil
	}

	// Compaction did not free enough room: fall back to drop-oldest, which
	// always retains the latest user message.
	return truncateForContext(history, window), nil
}

// Run drives a single user turn.
func (l *Loop) Run(ctx context.Context, sessionID string, userMsg message.Message) error {
	if !l.runMu.TryLock() {
		panic("agent: Run called concurrently on one Loop")
	}

	runCtx, cancel := context.WithCancel(ctx)
	l.cancelMu.Lock()
	l.cancelRun = cancel
	l.cancelMu.Unlock()
	l.steerMu.Lock()
	l.running = true
	l.steerMu.Unlock()
	defer func() {
		cancel()
		l.cancelMu.Lock()
		l.cancelRun = nil
		l.cancelMu.Unlock()
		// Release the run mutex BEFORE clearing the running flag so a caller that
		// observes running==false (and starts a fresh Run) never finds the mutex
		// still held. Clearing running last also closes the window where Steer
		// could queue onto a turn that has already stopped draining.
		l.runMu.Unlock()
		l.steerMu.Lock()
		l.running = false
		l.steerMu.Unlock()
	}()

	// SessionEnd fires once when the run completes for any reason (normal
	// finish, step limit, loop detection, provider/persistence error, or
	// cancellation). It is deferred so every exit path is covered. The hook
	// fires with context.WithoutCancel so a cancelled run still runs its
	// SessionEnd command, since "cancels" is an explicit end-of-run case.
	defer l.fireHook(context.WithoutCancel(ctx), hooks.SessionEnd, hooks.SessionPayload{SessionID: sessionID})

	l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnStarted})
	userMsg.SessionID = sessionID
	if userMsg.Role == "" {
		userMsg.Role = message.RoleUser
	}
	if userMsg.CreatedAt.IsZero() {
		userMsg.CreatedAt = time.Now().UTC()
	}
	if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, userMsg); err != nil {
		return fmt.Errorf("appending user message: %w", err)
	}

	history, err := l.cfg.Sessions.Messages(runCtx, sessionID)
	if err != nil {
		return fmt.Errorf("loading session messages: %w", err)
	}

	// SessionStart fires when a session's first turn begins, not on every Run.
	// The just-appended user message is the only message in history exactly when
	// this is the session's first turn, so a later Run on the same session never
	// refires it.
	if len(history) == 1 {
		l.fireHook(runCtx, hooks.SessionStart, hooks.SessionPayload{SessionID: sessionID})
	}

	history = l.applyCompaction(history)

	// Resolve which model serves this turn BEFORE fitting history, so the whole
	// turn (every step, retry, usage record, and the context-window budget used
	// by fitHistory) uses one model. Routing only inspects the latest genuine
	// user message, which is present pre-fit and is guaranteed to survive a fit,
	// so deciding here yields the same choice as deciding post-fit. With no
	// Router this is exactly cfg.Model, preserving prior behavior.
	l.activeModel = l.resolveTurnModel(history)

	history, err = l.fitHistory(runCtx, history)
	if err != nil {
		l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, Err: err})
		return err
	}

	detector := &loopDetector{}

	for step := 0; step < l.cfg.MaxSteps; step++ {
		// Drain any steering messages queued via Steer at this safe boundary
		// (always after a complete tool batch, never between an assistant
		// tool_use and its tool_result). Each is appended as the next user
		// message so the agent course-corrects without restarting the turn.
		for _, steerMsg := range l.drainSteering(sessionID) {
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, steerMsg); err != nil {
				return fmt.Errorf("appending steering message: %w", err)
			}
			history = append(history, steerMsg)
		}

		assistant, pendingToolCalls, usage, err := l.callProviderWithRetry(runCtx, history)
		if err != nil {
			failure := textMessage(sessionID, message.RoleAssistant, "provider failed: "+err.Error())
			_ = l.cfg.Sessions.AppendMessage(runCtx, sessionID, failure)
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, Err: err})
			return fmt.Errorf("calling provider: %w", err)
		}
		if usage != nil {
			assistant.Usage = &message.TokenUsage{
				InputTokens:      usage.InputTokens,
				OutputTokens:     usage.OutputTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				CacheWriteTokens: usage.CacheWriteTokens,
			}
			if err := l.recordUsage(runCtx, sessionID, *usage); err != nil {
				return fmt.Errorf("recording ledger usage: %w", err)
			}
		}
		if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, assistant); err != nil {
			return fmt.Errorf("appending assistant message: %w", err)
		}
		l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventLLMResponse, Message: &assistant})
		history = append(history, assistant)

		if len(pendingToolCalls) == 0 {
			// The model would end the turn, but if steering arrived while it was
			// composing this reply, keep going so the queued message is consumed
			// as the next user message instead of being deferred to a restart.
			if l.hasSteering() {
				continue
			}
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished})
			return nil
		}

		for _, call := range pendingToolCalls {
			looped, err := detector.observe(call.Name, call.Input)
			if err != nil {
				return fmt.Errorf("checking tool loop: %w", err)
			}
			if looped {
				msg := textMessage(sessionID, message.RoleAssistant, ErrLoopDetected.Error())
				if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, msg); err != nil {
					return fmt.Errorf("appending loop-detection message: %w", err)
				}
				l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventLoopDetected, Message: &msg})
				return nil
			}

			result := l.runTool(runCtx, sessionID, call)
			l.fireFileEditHook(runCtx, sessionID, call, result)
			// Bound oversized tool output before it enters the conversation
			// history so a single giant result cannot blow the context window.
			// Error results pass through so their essential message stays intact.
			content := truncateToolResult(result.Content, l.toolResultMaxBytes(), result.IsError)
			toolMsg := message.Message{
				SessionID: sessionID,
				Role:      message.RoleUser,
				Content: []message.ContentBlock{message.ToolResultBlock{
					ToolUseID: call.ID,
					Content:   content,
					IsError:   result.IsError,
				}},
				CreatedAt: time.Now().UTC(),
			}
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, toolMsg); err != nil {
				return fmt.Errorf("appending tool result: %w", err)
			}
			history = append(history, toolMsg)
		}
	}

	msg := textMessage(sessionID, message.RoleAssistant, "step limit reached")
	if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, msg); err != nil {
		return fmt.Errorf("appending step-limit message: %w", err)
	}
	l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished, Message: &msg})
	return nil
}

type pendingToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (l *Loop) callProvider(ctx context.Context, history []message.Message) (message.Message, []pendingToolCall, *llm.Usage, error) {
	req := llm.Request{
		Model:        l.activeModel,
		Messages:     history,
		Tools:        l.llmTools(),
		SystemPrompt: l.systemPrompt(),
	}
	l.applyReasoning(&req)
	events, err := l.cfg.Provider.Stream(ctx, req)
	if err != nil {
		return message.Message{}, nil, nil, err
	}

	var text string
	var calls []pendingToolCall
	var usage *llm.Usage
	openCalls := make(map[string]*pendingToolCall)
	for {
		select {
		case <-ctx.Done():
			return message.Message{}, nil, nil, fmt.Errorf("reading provider stream: %w", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				blocks := []message.ContentBlock{}
				if text != "" || len(calls) == 0 {
					blocks = append(blocks, message.TextBlock{Text: text})
				}
				for _, call := range calls {
					if len(call.Input) == 0 {
						call.Input = json.RawMessage(`{}`)
					}
					blocks = append(blocks, message.ToolUseBlock{ID: call.ID, Name: call.Name, Input: call.Input})
				}
				return message.Message{
					Role:      message.RoleAssistant,
					Content:   blocks,
					CreatedAt: time.Now().UTC(),
				}, calls, usage, nil
			}
			switch e := ev.(type) {
			case llm.DeltaTextEvent:
				text += e.Text
			case llm.ToolUseStartEvent:
				call := &pendingToolCall{ID: e.ID, Name: e.Name}
				openCalls[e.ID] = call
				calls = append(calls, *call)
			case llm.ToolUseDeltaEvent:
				call, ok := openCalls[e.ID]
				if !ok {
					call = &pendingToolCall{ID: e.ID}
					openCalls[e.ID] = call
					calls = append(calls, *call)
				}
				call.Input = append(call.Input, []byte(e.Delta)...)
				for i := range calls {
					if calls[i].ID == e.ID {
						calls[i] = *call
					}
				}
			case llm.ToolUseEndEvent:
				call, ok := openCalls[e.ID]
				if !ok {
					calls = append(calls, pendingToolCall{ID: e.ID, Name: e.Name, Input: e.Input})
					continue
				}
				call.Name = e.Name
				call.Input = e.Input
				for i := range calls {
					if calls[i].ID == e.ID {
						calls[i] = *call
					}
				}
			case llm.EndEvent:
				u := e.Usage
				usage = &u
			case llm.ErrorEvent:
				return message.Message{}, nil, nil, e.Err
			}
		}
	}
}

// systemPrompt returns the system prompt for the current provider call. In plan
// mode it appends the plan-mode instruction so the agent is prompted to produce
// a plan rather than execute; once Approve clears plan mode, the base prompt is
// used unchanged.
func (l *Loop) systemPrompt() string {
	if l.planMode.Load() {
		return l.cfg.SystemPrompt + planModePrompt
	}
	return l.cfg.SystemPrompt
}

func (l *Loop) runTool(ctx context.Context, sessionID string, call pendingToolCall) (result tools.Result) {
	tool, ok := l.cfg.Tools.Get(call.Name)
	if !ok {
		return tools.Result{Content: "unknown tool: " + call.Name, IsError: true}
	}
	wrapped := hookedTool{inner: tool, hooks: l.cfg.Hooks, sessionID: sessionID, agentName: l.name, allowed: l.toolAllowed(call.Name)}
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventToolCalled, ToolName: call.Name})
	result, err := l.runToolSafely(ctx, &wrapped, call)
	if err != nil {
		l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, ToolName: call.Name, Err: err})
		return tools.Result{Content: err.Error(), IsError: true}
	}
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventToolResult, ToolName: call.Name})
	return result
}

// runToolSafely runs the tool and converts any panic into an error so a single
// misbehaving tool cannot take down the agent loop. The recovered panic is
// logged with its stack and surfaced to the caller as an error.
func (l *Loop) runToolSafely(ctx context.Context, wrapped *hookedTool, call pendingToolCall) (result tools.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			slog.Error(
				"tool panicked",
				slog.String("tool", call.Name),
				slog.Any("panic", r),
				slog.String("stack", string(stack)),
			)
			err = fmt.Errorf("tool %q panicked: %v", call.Name, r)
			result = tools.Result{}
		}
	}()
	return wrapped.Run(ctx, call.Input)
}

func (l *Loop) llmTools() []llm.Tool {
	out := []llm.Tool{}
	for _, tool := range l.cfg.Tools.List() {
		if !l.toolAllowed(tool.Name()) {
			continue
		}
		out = append(out, llm.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Schema(),
		})
	}
	return out
}

// toolAllowed reports whether the named tool may run on this turn. It applies
// the configured allow-list first (when non-empty, only listed tools pass), then
// layers plan mode on top, which can only further restrict the set to read-only
// tools. When plan mode is off and no allow-list is configured this reduces to
// "always allowed", preserving the unrestricted default. The two effects
// intersect — plan mode never expands a configured allow-list — so a custom
// agent restricted to a few tools does not gain new ones when plan mode turns
// on.
func (l *Loop) toolAllowed(name string) bool {
	if len(l.allowed) > 0 {
		if _, ok := l.allowed[name]; !ok {
			return false
		}
	}
	if l.planMode.Load() {
		if _, ok := readOnlySet[name]; !ok {
			return false
		}
	}
	return true
}

func (l *Loop) contextWindow() int {
	for _, model := range l.cfg.Provider.Models() {
		if model.ID == l.activeModel {
			return model.ContextWindow
		}
	}
	return 0
}

// applyReasoning populates the per-model reasoning controls on req from the
// active model's configuration. ReasoningEffort is forwarded only when
// configured non-empty, and Thinking only when a positive budget is configured.
// Both are gated by the provider against the model id, so populating them for a
// model that does not support the feature is harmless: the provider omits the
// unsupported field rather than failing the request. Models that configure
// neither leave req unchanged, preserving prior behavior.
func (l *Loop) applyReasoning(req *llm.Request) {
	model, ok := findModelByID(l.cfg.Provider.Models(), l.activeModel)
	if !ok {
		return
	}
	if model.ReasoningEffort != "" {
		req.ReasoningEffort = model.ReasoningEffort
	}
	if model.ThinkingBudget > 0 {
		req.Thinking = &llm.ThinkingConfig{BudgetTokens: model.ThinkingBudget}
	}
}

// findModelByID returns the model with the given id from models and whether it
// was found.
func findModelByID(models []llm.Model, id string) (llm.Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return llm.Model{}, false
}

// resolveTurnModel picks the model for this turn. With no Router it returns the
// configured default unchanged, so routing is strictly opt-in. With a Router it
// asks the policy to choose from the provider's models; an empty or unknown
// choice falls back to the configured default so a declining Router never
// breaks the turn.
func (l *Loop) resolveTurnModel(history []message.Message) string {
	if l.cfg.Router == nil {
		return l.cfg.Model
	}
	models := l.cfg.Provider.Models()
	turn := Turn{
		History:        history,
		ToolsAvailable: len(l.llmTools()) > 0,
		Hint:           l.cfg.RouteHint,
	}
	choice := l.cfg.Router.Route(turn, models)
	if choice == "" {
		return l.cfg.Model
	}
	for _, m := range models {
		if m.ID == choice {
			return choice
		}
	}
	return l.cfg.Model
}

func (l *Loop) recordUsage(ctx context.Context, sessionID string, usage llm.Usage) error {
	if l.cfg.Ledger == nil {
		return nil
	}
	return l.cfg.Ledger.Record(ctx, ledger.Entry{
		ID:           newID(),
		SessionID:    sessionID,
		Provider:     l.cfg.Provider.Name(),
		Model:        l.activeModel,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		At:           time.Now(),
	})
}

func (l *Loop) publish(ctx context.Context, ev Event) {
	if l.cfg.Bus != nil {
		l.cfg.Bus.Publish(ctx, ev)
	}
}

// fireHook fires a lifecycle hook, guarding on a nil hooks engine and logging
// (rather than propagating) any error so a misconfigured hook never aborts the
// agent run. Lifecycle hooks are best-effort observers: their failure must not
// fail the turn the way a blocking PreToolUse decision does.
func (l *Loop) fireHook(ctx context.Context, event hooks.Event, payload any) {
	if l.cfg.Hooks == nil {
		return
	}
	if _, err := l.cfg.Hooks.Fire(ctx, event, payload); err != nil {
		slog.Warn("Lifecycle hook failed", "event", event, "error", err)
	}
}

// fireFileEditHook fires a FileEdit hook after a successful write-class tool
// run. It returns immediately for non-mutating tools, error results (which
// covers hook-blocked and panicking tools), and inputs without a path.
func (l *Loop) fireFileEditHook(ctx context.Context, sessionID string, call pendingToolCall, result tools.Result) {
	if _, ok := writeClassTools[call.Name]; !ok {
		return
	}
	if result.IsError {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil || args.Path == "" {
		return
	}
	l.fireHook(ctx, hooks.FileEdit, hooks.FileEditPayload{Path: args.Path, SessionID: sessionID})
}

func textMessage(sessionID string, role message.Role, text string) message.Message {
	return message.Message{
		SessionID: sessionID,
		Role:      role,
		Content:   []message.ContentBlock{message.TextBlock{Text: text}},
		CreatedAt: time.Now().UTC(),
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
