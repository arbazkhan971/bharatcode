package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
)

// SubTask describes one unit of work for a subagent dispatched by
// DispatchParallel. Agent names the configured agent to run (for example
// "task"); an empty Agent defaults to "task", the read-only investigation
// agent. Prompt is the user message that seeds the subagent's session. Title,
// when non-empty, is used as the spawned session's title; otherwise the title
// is derived from the prompt.
type SubTask struct {
	Agent  string
	Prompt string
	Title  string
}

// Result is the outcome of a single SubTask. Index records the task's position
// in the slice passed to DispatchParallel so results can be correlated back to
// their inputs regardless of completion order. SessionID is the fresh session
// the subagent ran in. Output is the subagent's final assistant text (the last
// assistant message in the session). Err is non-nil when the subagent's run, or
// its setup, failed; on error Output is the empty string.
type Result struct {
	Index     int
	SessionID string
	Output    string
	Err       error
}

// DispatchParallel runs each SubTask in its own subagent loop concurrently and
// returns one Result per task. Results are returned in input order: Result[i]
// always corresponds to tasks[i], independent of the order in which the
// subagents finished. This lets a parent agent fan out independent
// investigations and collect their conclusions deterministically.
//
// Each subagent gets a fresh Loop (via Agent) and a fresh session, so the
// concurrent runs never share mutable state: a Loop's Run panics if invoked
// concurrently, and DispatchParallel never reuses one across tasks. Concurrency
// is bounded by limit; a limit <= 0 runs every task at once (bounded only by
// len(tasks)).
//
// DispatchParallel is context-cancellable: cancelling ctx stops in-flight
// subagent runs and prevents queued ones from starting, and every affected
// Result carries the cancellation error. The aggregate error returned joins all
// per-task errors (nil when every task succeeded); per-task errors are also
// available on each Result so callers can tell which subtasks failed.
func (c *Coordinator) DispatchParallel(ctx context.Context, tasks []SubTask, limit int) ([]Result, error) {
	results := make([]Result, len(tasks))
	if len(tasks) == 0 {
		return results, nil
	}
	if limit <= 0 || limit > len(tasks) {
		limit = len(tasks)
	}

	// Buffered-channel semaphore bounds how many subagents run at once. Writing
	// results by distinct index means no goroutine touches another's slot, so no
	// mutex guards results; wg.Wait establishes the happens-before for the read.
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range tasks {
		// Acquire a slot before spawning so a cancelled context short-circuits
		// queued tasks without ever starting their goroutine's run.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = Result{Index: i, Err: ctx.Err()}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = c.runSubTask(ctx, i, tasks[i])
		}(i)
	}
	wg.Wait()

	errs := make([]error, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("subtask %d: %w", r.Index, r.Err))
		}
	}
	return results, errors.Join(errs...)
}

// runSubTask sets up a fresh session and subagent loop for one task, runs it,
// and extracts the subagent's final assistant text. It never panics out to the
// caller: setup and run failures are reported on the returned Result.
func (c *Coordinator) runSubTask(ctx context.Context, index int, task SubTask) Result {
	res := Result{Index: index}

	name := task.Agent
	if name == "" {
		name = "task"
	}

	loop, err := c.Agent(name)
	if err != nil {
		res.Err = fmt.Errorf("dispatching subtask: %w", err)
		return res
	}

	sessionID, err := c.newSubSession(ctx, name, task)
	if err != nil {
		res.Err = fmt.Errorf("dispatching subtask: %w", err)
		return res
	}
	res.SessionID = sessionID

	userMsg := message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: task.Prompt}},
	}
	if err := loop.Run(ctx, sessionID, userMsg); err != nil {
		res.Err = err
		return res
	}

	output, err := c.lastAssistantText(ctx, sessionID)
	if err != nil {
		res.Err = fmt.Errorf("dispatching subtask: %w", err)
		return res
	}
	res.Output = output
	return res
}

// newSubSession creates a fresh session for a dispatched subagent and returns
// its ID. The session records the spawning agent and the resolved model so the
// run is attributable, and is titled from the task or its prompt.
func (c *Coordinator) newSubSession(ctx context.Context, agentName string, task SubTask) (string, error) {
	if c.deps.Sessions == nil {
		return "", errors.New("creating subtask session: sessions repo is nil")
	}

	title := task.Title
	if title == "" {
		title = session.TitleFromFirstMessage(message.Message{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: task.Prompt}},
		})
	}

	s := &session.Session{
		ProjectPath: c.projectPath(),
		Title:       title,
		Model:       c.modelFor(agentName),
		Agent:       agentName,
	}
	if err := c.deps.Sessions.Create(ctx, s); err != nil {
		return "", fmt.Errorf("creating subtask session: %w", err)
	}
	return s.ID, nil
}

// lastAssistantText returns the text of the most recent assistant message in
// the session, scanning backward so it captures the final reply regardless of
// how the run ended (normal finish, step limit, loop detection, or a folded
// provider failure all append a trailing assistant message). It returns the
// empty string when the session has no assistant message.
func (c *Coordinator) lastAssistantText(ctx context.Context, sessionID string) (string, error) {
	msgs, err := c.deps.Sessions.Messages(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("loading subtask session messages: %w", err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.RoleAssistant {
			continue
		}
		var text string
		for _, block := range msgs[i].Content {
			if tb, ok := block.(message.TextBlock); ok {
				text += tb.Text
			}
		}
		return text, nil
	}
	return "", nil
}

// modelFor returns the resolved model ID for the named agent, or the empty
// string when the agent is unknown. It reads under the Coordinator lock so it
// is safe to call concurrently with dispatched runs.
func (c *Coordinator) modelFor(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, def := range c.agents {
		if def.name == name {
			return def.model
		}
	}
	return ""
}

// projectPath returns the project path recorded on dispatched subagent
// sessions. It mirrors the Coordinator's skill-loading path by using the
// current working directory, falling back to the empty string when it is
// unavailable so session creation never blocks on an unset path.
func (c *Coordinator) projectPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
