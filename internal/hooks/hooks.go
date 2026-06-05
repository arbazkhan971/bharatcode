// Package hooks runs user-defined shell commands for lifecycle events.
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

const defaultTimeout = 5 * time.Second

// Event identifies a lifecycle point where user hooks may run.
type Event string

const (
	// PreToolUse fires before a tool is executed.
	PreToolUse Event = "PreToolUse"
	// PostToolUse fires after a tool is executed.
	PostToolUse Event = "PostToolUse"
	// SessionStart fires when a session starts.
	SessionStart Event = "SessionStart"
	// SessionEnd fires when a session ends.
	SessionEnd Event = "SessionEnd"
	// FileEdit fires after a file is edited.
	FileEdit Event = "FileEdit"
	// OnError fires when the agent loop reports an error.
	OnError Event = "OnError"
	// OnSession fires for legacy session lifecycle configuration.
	OnSession Event = "OnSession"
	// VerifyEdit fires after a write-class tool succeeds, running the
	// verify_command configured on the matching FileEdit hook. It is separate
	// from FileEdit so user hooks and auto-verify can coexist on the same hook
	// definition without ambiguity.
	VerifyEdit Event = "VerifyEdit"
)

// HookDef is a single user-defined hook command.
type HookDef struct {
	Event   Event
	Match   string
	Command string
	Timeout time.Duration
	// VerifyCommand, when non-empty, is a shell command run after a successful
	// write-class tool execution that matches this hook's Match pattern (for
	// FileEdit events only). A non-zero exit code is treated as a verification
	// failure and is surfaced to the agent as an error tool result so it can
	// re-edit or explain.
	VerifyCommand string
	VerifyTimeout time.Duration
}

// VerifySpec describes a verify command that should run after a file edit.
// It is returned by MatchingVerifiers for callers that want to run verify
// commands independently of the normal hook-fire path.
type VerifySpec struct {
	Command string
	Timeout time.Duration
	Cwd     string
}

// Decision is the aggregate result of firing matching hooks.
type Decision struct {
	Block    bool   `json:"block,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Approve  bool   `json:"approve,omitempty"`
	Continue bool   `json:"continue,omitempty"`
}

// ToolPayload is the JSON payload for PreToolUse and PostToolUse events.
//
// JSON schema:
//
//	{
//	  "event": "PreToolUse|PostToolUse",
//	  "tool": "bash",
//	  "args": {},
//	  "session_id": "session-id"
//	}
type ToolPayload struct {
	Tool      string `json:"tool"`
	Args      any    `json:"args,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Result    any    `json:"result,omitempty"`
}

// FileEditPayload is the JSON payload for FileEdit events.
//
// JSON schema:
//
//	{
//	  "event": "FileEdit",
//	  "path": "relative/or/absolute/path",
//	  "session_id": "session-id"
//	}
type FileEditPayload struct {
	Path      string `json:"path"`
	SessionID string `json:"session_id,omitempty"`
}

// SessionPayload is the JSON payload for session lifecycle events.
//
// JSON schema:
//
//	{
//	  "event": "SessionStart|SessionEnd|OnSession",
//	  "session_id": "session-id"
//	}
type SessionPayload struct {
	SessionID string `json:"session_id"`
}

// ErrorPayload is the JSON payload for OnError events.
//
// JSON schema:
//
//	{
//	  "event": "OnError",
//	  "error": "message",
//	  "session_id": "session-id"
//	}
type ErrorPayload struct {
	Error     string `json:"error"`
	SessionID string `json:"session_id,omitempty"`
}

// Engine stores compiled hook definitions and runs them through shell.Shell.
type Engine struct {
	hooks []HookDef
	sh    *shell.Shell
	cwd   string
}

type hookResult struct {
	index    int
	decision Decision
}

type decisionEnvelope struct {
	Decision Decision `json:"decision"`
}

// New builds a hook engine from configuration.
func New(cfg *config.Config, sh *shell.Shell) *Engine {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	if projectPath := config.ProjectPath(cwd); projectPath != "" {
		cwd = filepath.Dir(projectPath)
	}

	engine := &Engine{
		sh:  sh,
		cwd: cwd,
	}
	if cfg == nil {
		return engine
	}

	const defaultVerifyTimeout = 30 * time.Second

	engine.hooks = make([]HookDef, 0, len(cfg.Hooks))
	for _, hook := range cfg.Hooks {
		timeout := time.Duration(hook.Timeout) * time.Second
		verifyTimeout := time.Duration(hook.VerifyTimeoutSeconds) * time.Second
		if verifyTimeout <= 0 && hook.VerifyCommand != "" {
			verifyTimeout = defaultVerifyTimeout
		}
		engine.hooks = append(engine.hooks, HookDef{
			Event:         Event(hook.Event),
			Match:         hook.Match,
			Command:       hook.Command,
			Timeout:       timeout,
			VerifyCommand: hook.VerifyCommand,
			VerifyTimeout: verifyTimeout,
		})
	}
	return engine
}

// MatchingVerifiers returns the VerifySpec for every FileEdit hook whose
// Match pattern matches filePath and whose VerifyCommand is non-empty. The
// caller is responsible for running the returned commands. When no hooks match
// or none carry a VerifyCommand, the returned slice is empty. A nil engine
// always returns nil.
func (e *Engine) MatchingVerifiers(filePath string) []VerifySpec {
	if e == nil {
		return nil
	}
	var out []VerifySpec
	for _, hook := range e.hooks {
		if hook.Event != FileEdit {
			continue
		}
		if hook.VerifyCommand == "" {
			continue
		}
		ok, err := matches(hook.Match, filePath)
		if err != nil || !ok {
			continue
		}
		out = append(out, VerifySpec{
			Command: hook.VerifyCommand,
			Timeout: hook.VerifyTimeout,
			Cwd:     e.cwd,
		})
	}
	return out
}

// Fire runs all hooks matching event and payload, then aggregates their decisions.
func (e *Engine) Fire(ctx context.Context, event Event, payload any) (Decision, error) {
	if e == nil {
		return passThrough(), nil
	}

	data, fields, err := marshalPayload(event, payload)
	if err != nil {
		return Decision{}, fmt.Errorf("marshaling hook payload: %w", err)
	}

	matchValue := matchField(event, fields)
	sessionID := stringField(fields, "session_id")

	var matched []HookDef
	for _, hook := range e.hooks {
		if hook.Event != event {
			continue
		}
		ok, err := matches(hook.Match, matchValue)
		if err != nil {
			slog.Warn("Hook match pattern failed", "event", event, "match", hook.Match, "error", err)
			continue
		}
		if ok {
			matched = append(matched, hook)
		}
	}

	if len(matched) == 0 {
		return passThrough(), nil
	}
	if e.sh == nil {
		return Decision{}, fmt.Errorf("running hooks: shell is nil")
	}

	results := make(chan hookResult, len(matched))
	var wg sync.WaitGroup
	for i, hook := range matched {
		wg.Add(1)
		go func(index int, def HookDef) {
			defer wg.Done()
			results <- hookResult{
				index:    index,
				decision: e.runHook(ctx, def, event, sessionID, data),
			}
		}(i, hook)
	}

	wg.Wait()
	close(results)

	decisions := make([]Decision, len(matched))
	for result := range results {
		decisions[result.index] = result.decision
	}

	return aggregate(decisions), nil
}

func (e *Engine) runHook(
	ctx context.Context,
	hook HookDef,
	event Event,
	sessionID string,
	payload []byte,
) Decision {
	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	job, err := e.sh.Run(ctx, pipePayloadCommand(payload, hook.Command), shell.RunOpts{
		Cwd:     e.cwd,
		Timeout: timeout,
		Env: map[string]string{
			"BHARATCODE_EVENT":      string(event),
			"BHARATCODE_SESSION_ID": sessionID,
		},
	})
	if err != nil {
		slog.Warn("Hook command failed to start", "event", event, "error", err)
		return passThrough()
	}

	if strings.TrimSpace(job.Stderr) != "" {
		slog.Info("Hook command wrote stderr", "event", event, "stderr", job.Stderr)
	}
	if job.Status == shell.StatusTimeout {
		slog.Warn("Hook command timed out", "event", event, "timeout", timeout.String())
		return passThrough()
	}
	if job.Status != shell.StatusCompleted {
		slog.Warn("Hook command exited without success", "event", event, "status", job.Status, "exit_code", job.ExitCode)
		return passThrough()
	}

	decision, err := parseDecision(job.Stdout)
	if err != nil {
		slog.Warn("Hook command returned invalid decision JSON", "event", event, "error", err)
		return passThrough()
	}
	return decision
}

func marshalPayload(event Event, payload any) ([]byte, map[string]any, error) {
	fields := map[string]any{}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("marshaling payload: %w", err)
		}
		if len(data) > 0 && string(data) != "null" {
			if err := json.Unmarshal(data, &fields); err != nil {
				return nil, nil, fmt.Errorf("normalizing payload object: %w", err)
			}
		}
	}
	fields["event"] = string(event)

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling normalized payload: %w", err)
	}
	return data, fields, nil
}

func parseDecision(stdout string) (Decision, error) {
	if strings.TrimSpace(stdout) == "" {
		return passThrough(), nil
	}

	var envelope decisionEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return Decision{}, fmt.Errorf("parsing decision envelope: %w", err)
	}
	if envelope.Decision == (Decision{}) {
		return passThrough(), nil
	}
	decision := envelope.Decision
	if !decision.Block && !decision.Approve {
		decision.Continue = true
	}
	return decision, nil
}

func aggregate(decisions []Decision) Decision {
	for _, decision := range decisions {
		if decision.Block {
			return Decision{
				Block:  true,
				Reason: decision.Reason,
			}
		}
	}
	for _, decision := range decisions {
		if decision.Approve {
			return Decision{Approve: true}
		}
	}
	return passThrough()
}

func passThrough() Decision {
	return Decision{Continue: true}
}

func matches(pattern, value string) (bool, error) {
	if pattern == "" {
		return true, nil
	}
	if len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
		re, err := regexp.Compile(strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/"))
		if err != nil {
			return false, fmt.Errorf("compiling regex: %w", err)
		}
		return re.MatchString(value), nil
	}
	ok, err := filepath.Match(pattern, value)
	if err != nil {
		return false, fmt.Errorf("matching glob: %w", err)
	}
	return ok, nil
}

func matchField(event Event, fields map[string]any) string {
	switch event {
	case PreToolUse, PostToolUse:
		return stringField(fields, "tool")
	case FileEdit:
		if path := stringField(fields, "path"); path != "" {
			return path
		}
		return stringField(fields, "file_path")
	case SessionStart, SessionEnd, OnSession:
		return stringField(fields, "session_id")
	case OnError:
		return stringField(fields, "error")
	default:
		return ""
	}
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func pipePayloadCommand(payload []byte, command string) string {
	return fmt.Sprintf("printf %%s %s | ( %s )", shellQuote(string(payload)), command)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
