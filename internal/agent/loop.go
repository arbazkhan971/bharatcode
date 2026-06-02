package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
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
	Hooks         *hooks.Engine
	SystemPrompt  string
	ToolAllowList []string
	MaxSteps      int
	// Compactor condenses the conversation before it is sent to the provider
	// when Compact is invoked. When nil, a default drop-and-mark Compactor is
	// used.
	Compactor Compactor
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
	return &Loop{cfg: cfg, name: cfg.Name, allowed: allowed}
}

// Name returns the configured agent name.
func (l *Loop) Name() string {
	return l.name
}

// Interrupt cancels an in-flight Run.
func (l *Loop) Interrupt() {
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	if l.cancelRun != nil {
		l.cancelRun()
	}
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

	compactor := l.cfg.Compactor
	if compactor == nil {
		compactor = newDropAndMarkCompactor(2)
	}

	input := append([]message.Message(nil), history...)
	condensed, err := compactor.Compact(ctx, input)
	if err != nil {
		return fmt.Errorf("compacting history: %w", err)
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

	l.compactMu.Lock()
	l.compacted = condensed
	l.compactedLen = len(history)
	l.compactMu.Unlock()

	return nil
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

// Run drives a single user turn.
func (l *Loop) Run(ctx context.Context, sessionID string, userMsg message.Message) error {
	if !l.runMu.TryLock() {
		panic("agent: Run called concurrently on one Loop")
	}
	defer l.runMu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	l.cancelMu.Lock()
	l.cancelRun = cancel
	l.cancelMu.Unlock()
	defer func() {
		cancel()
		l.cancelMu.Lock()
		l.cancelRun = nil
		l.cancelMu.Unlock()
	}()

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
	history = l.applyCompaction(history)
	history = truncateForContext(history, l.contextWindow())
	detector := &loopDetector{}

	for step := 0; step < l.cfg.MaxSteps; step++ {
		assistant, pendingToolCalls, usage, err := l.callProvider(runCtx, history)
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
			toolMsg := message.Message{
				SessionID: sessionID,
				Role:      message.RoleUser,
				Content: []message.ContentBlock{message.ToolResultBlock{
					ToolUseID: call.ID,
					Content:   result.Content,
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
	events, err := l.cfg.Provider.Stream(ctx, llm.Request{
		Model:        l.cfg.Model,
		Messages:     history,
		Tools:        l.llmTools(),
		SystemPrompt: l.cfg.SystemPrompt,
	})
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

func (l *Loop) runTool(ctx context.Context, sessionID string, call pendingToolCall) (result tools.Result) {
	tool, ok := l.cfg.Tools.Get(call.Name)
	if !ok {
		return tools.Result{Content: "unknown tool: " + call.Name, IsError: true}
	}
	_, allowedLimited := l.allowed[call.Name]
	allowed := len(l.allowed) == 0 || allowedLimited
	wrapped := hookedTool{inner: tool, hooks: l.cfg.Hooks, sessionID: sessionID, agentName: l.name, allowed: allowed}
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
		if len(l.allowed) > 0 {
			if _, ok := l.allowed[tool.Name()]; !ok {
				continue
			}
		}
		out = append(out, llm.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Schema(),
		})
	}
	return out
}

func (l *Loop) contextWindow() int {
	for _, model := range l.cfg.Provider.Models() {
		if model.ID == l.cfg.Model {
			return model.ContextWindow
		}
	}
	return 0
}

func (l *Loop) recordUsage(ctx context.Context, sessionID string, usage llm.Usage) error {
	if l.cfg.Ledger == nil {
		return nil
	}
	return l.cfg.Ledger.Record(ctx, ledger.Entry{
		ID:           newID(),
		SessionID:    sessionID,
		Provider:     l.cfg.Provider.Name(),
		Model:        l.cfg.Model,
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
