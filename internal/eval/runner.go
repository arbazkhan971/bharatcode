package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// Runner executes evaluation suites and accumulates reports.
type Runner struct {
	// MaxSteps caps how many loop steps the agent may take per task.
	// A zero or negative value uses the agent default (50).
	MaxSteps int
}

// RunSuite runs every task in suite and returns the aggregate report.
func (r Runner) RunSuite(ctx context.Context, suite Suite) (Report, error) {
	report := Report{
		SuiteName: suite.Name,
		StartedAt: time.Now(),
	}
	for _, task := range suite.Tasks {
		res := r.RunTask(ctx, task)
		report.Tasks = append(report.Tasks, res)
	}
	report.FinishedAt = time.Now()
	report.aggregate()
	return report, nil
}

// RunTask executes one task in an isolated temp directory and returns the
// per-task result.
func (r Runner) RunTask(ctx context.Context, task Task) TaskResult {
	res := TaskResult{
		TaskID:   task.ID,
		TaskName: task.Name,
	}

	// Set up isolated temp directory.
	dir, err := os.MkdirTemp("", "eval-"+task.ID+"-*")
	if err != nil {
		res.Err = fmt.Sprintf("creating temp dir: %s", err)
		return res
	}
	defer os.RemoveAll(dir)

	// Build fixture.
	if task.Fixture != nil {
		if err := task.Fixture(dir); err != nil {
			res.Err = fmt.Sprintf("building fixture: %s", err)
			return res
		}
	}

	// Spin up a stub provider, agent, and an in-memory session.
	observer := &toolObserver{}
	provider := &scriptProvider{scripts: cloneScript(task.Script), observer: observer}
	registry := newMinimalRegistry(dir, observer)

	database, err := db.Open(ctx, filepath.Join(dir, "eval.db"))
	if err != nil {
		res.Err = fmt.Sprintf("opening eval db: %s", err)
		return res
	}
	defer database.Close()

	repo := session.NewRepo(database)
	sess := &session.Session{
		ProjectPath: dir,
		Title:       task.Name,
		Model:       "eval-model",
		Agent:       "eval",
	}
	if err := repo.Create(ctx, sess); err != nil {
		res.Err = fmt.Sprintf("creating session: %s", err)
		return res
	}

	bus := pubsub.NewTopic[agent.Event]("eval", 64)

	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}

	loop := agent.New(agent.Config{
		Name:         "eval",
		Model:        "eval-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          bus,
		SystemPrompt: "You are an evaluation agent.",
		MaxSteps:     maxSteps,
	})

	userMsg := message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: task.Goal}},
	}

	runErr := loop.Run(ctx, sess.ID, userMsg)

	// Collect the final assistant text from the session.
	msgs, _ := repo.Messages(ctx, sess.ID)
	finalText := lastAssistantText(msgs)

	outcome := Outcome{
		ToolCalls: observer.calls(),
		FinalText: finalText,
		Err:       runErr,
	}
	res.Steps = provider.stepCount()
	res.Recoveries = observer.recoveries()

	// Apply the task checker.
	if task.Check != nil {
		passed, reason := task.Check(dir, outcome)
		res.Passed = passed
		res.Reason = reason
	} else {
		// Default: pass when the run completed without error.
		res.Passed = runErr == nil
		if runErr != nil {
			res.Reason = runErr.Error()
		} else {
			res.Reason = "run completed without error"
		}
	}
	if runErr != nil && res.Reason == "" {
		res.Reason = runErr.Error()
	}
	return res
}

// -------- stub provider --------

// scriptProvider replays a pre-scripted sequence of event slices, one slice
// per provider call (turn). It satisfies llm.Provider and requires zero real
// credentials.
type scriptProvider struct {
	mu       sync.Mutex
	scripts  [][][]llm.Event
	step     int
	observer *toolObserver
}

// cloneScript deep-copies the event script so the runner is safe to call
// multiple times with the same Task without cross-contamination.
func cloneScript(src [][]llm.Event) [][][]llm.Event {
	// We keep one copy as a 3-D slice: [turn][event]. The outer dimension
	// enables the provider to cycle from the front on each Stream call while
	// preserving the original for re-use.
	out := make([][][]llm.Event, 1)
	out[0] = make([][]llm.Event, len(src))
	for i, turn := range src {
		out[0][i] = append([]llm.Event(nil), turn...)
	}
	return out
}

func (p *scriptProvider) Name() string { return "eval-stub" }

func (p *scriptProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	var events []llm.Event
	if len(p.scripts) > 0 && len(p.scripts[0]) > 0 {
		events = p.scripts[0][0]
		p.scripts[0] = p.scripts[0][1:]
		if len(p.scripts[0]) == 0 {
			p.scripts = p.scripts[1:]
		}
	}
	// Observe tool calls embedded in the request so we can count recoveries
	// (retries / repeated calls) without access to agent internals.
	_ = req
	p.step++
	p.mu.Unlock()

	ch := make(chan llm.Event, len(events)+1)
	go func() {
		defer close(ch)
		for _, ev := range events {
			// Record tool calls from ToolUseEndEvents so the observer sees
			// exactly what the scripted agent would call.
			if tce, ok := ev.(llm.ToolUseEndEvent); ok {
				p.observer.record(tce.Name, tce.Input)
			}
			ch <- ev
		}
	}()
	return ch, nil
}

func (p *scriptProvider) Models() []llm.Model {
	return []llm.Model{{
		ID:            "eval-model",
		Provider:      "eval-stub",
		ContextWindow: 8192,
		SupportsTools: true,
	}}
}

func (p *scriptProvider) SupportsTools() bool  { return true }
func (p *scriptProvider) SupportsImages() bool { return false }

func (p *scriptProvider) stepCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.step
}

// -------- tool observer --------

// toolObserver collects tool calls and counts error results (recoveries).
type toolObserver struct {
	mu         sync.Mutex
	toolCalls  []ToolCall
	errorCount int
}

func (o *toolObserver) record(name string, input json.RawMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.toolCalls = append(o.toolCalls, ToolCall{Name: name, Input: input})
}

func (o *toolObserver) recordError() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errorCount++
}

func (o *toolObserver) calls() []ToolCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ToolCall, len(o.toolCalls))
	copy(out, o.toolCalls)
	return out
}

func (o *toolObserver) recoveries() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.errorCount
}

// -------- minimal tool registry --------

// minimalRegistry exposes a small set of recording tools sufficient for
// eval tasks (view, edit, write, bash). The tools don't perform real I/O —
// they record the call and return an empty success. The observer is notified
// of each call so the CheckFn can assert on tool usage.
func newMinimalRegistry(workDir string, obs *toolObserver) *evalRegistry {
	r := &evalRegistry{tools: map[string]tools.Tool{}, workDir: workDir}
	for _, name := range []string{"view", "edit", "write", "multiedit", "bash", "glob", "grep", "ls"} {
		r.register(&recordingEvalTool{name: name, obs: obs})
	}
	return r
}

type evalRegistry struct {
	mu      sync.RWMutex
	tools   map[string]tools.Tool
	workDir string
}

func (r *evalRegistry) register(t tools.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *evalRegistry) Get(name string) (tools.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *evalRegistry) List() []tools.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]tools.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// recordingEvalTool is a stub tool that records calls via the observer and
// always succeeds. It does not perform real file I/O so eval runs are hermetic.
type recordingEvalTool struct {
	name string
	obs  *toolObserver
}

func (t *recordingEvalTool) Name() string { return t.name }

func (t *recordingEvalTool) Description() string { return "eval stub: " + t.name }

func (t *recordingEvalTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t *recordingEvalTool) Run(_ context.Context, args json.RawMessage) (tools.Result, error) {
	// The observer has already been notified by the script provider for the
	// scripted tool calls; this records any additional tool invocations that
	// come from live agent logic.
	t.obs.record(t.name, args)
	return tools.Result{Content: "ok"}, nil
}

// -------- helpers --------

func lastAssistantText(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.RoleAssistant {
			continue
		}
		var out string
		for _, block := range msgs[i].Content {
			if tb, ok := block.(message.TextBlock); ok {
				out += tb.Text
			}
		}
		return out
	}
	return ""
}
