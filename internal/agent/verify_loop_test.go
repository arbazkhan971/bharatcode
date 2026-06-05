package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

// fakeVerifyHookSource is a verifyHookSource that returns a fixed set of
// VerifySpecs for any path, independent of match patterns.
type fakeVerifyHookSource struct {
	specs []hooks.VerifySpec
}

func (f *fakeVerifyHookSource) MatchingVerifiers(_ string) []hooks.VerifySpec {
	return f.specs
}

// captureVerifyRunner records every RunVerify call and returns a scripted
// sequence of (output, error) pairs. Once the script is exhausted it returns
// a sentinel error so a test can detect unexpected extra calls.
type captureVerifyRunner struct {
	calls  []verifyCall
	script []verifyOutcome
}

type verifyCall struct {
	command string
	timeout time.Duration
}

type verifyOutcome struct {
	output string
	err    error
}

func (r *captureVerifyRunner) RunVerify(_ context.Context, command, _ string, timeout time.Duration) (string, error) {
	r.calls = append(r.calls, verifyCall{command: command, timeout: timeout})
	idx := len(r.calls) - 1
	if idx >= len(r.script) {
		return "unexpected extra verify call", errors.New("unexpected extra verify call")
	}
	return r.script[idx].output, r.script[idx].err
}

// ─── verify-loop integration tests ───────────────────────────────────────────

// TestVerifyRunsAfterSuccessfulWriteClassTool asserts that when a write-class
// tool succeeds and VerifyHooks returns a matching spec, RunVerify is called
// once with the configured command.
func TestVerifyRunsAfterSuccessfulWriteClassTool(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "edit", `{"path":"main.go","old_string":"x","new_string":"y"}`),
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{
		script: []verifyOutcome{{output: "", err: nil}},
	}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("verify-test", 16),
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "go build ./...", Timeout: 5 * time.Second},
		}},
		VerifyRunner: runner,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit the file")))

	require.Len(t, runner.calls, 1, "verify must run exactly once after the edit")
	require.Equal(t, "go build ./...", runner.calls[0].command)
	require.Equal(t, 5*time.Second, runner.calls[0].timeout)
}

// TestVerifyFailureIsInjectedAsToolResultError asserts that when the verify
// command exits non-zero, the loop overrides the tool result with an IsError
// result containing the verify output so the model sees the failure.
func TestVerifyFailureIsInjectedAsToolResultError(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	const verifyOut = "build failed: undefined: Foo"

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "edit", `{"path":"main.go","old_string":"x","new_string":"y"}`),
			llm.EndEvent{},
		},
		// Model sees the error and replies with text (second turn).
		{llm.DeltaTextEvent{Text: "I see the build failed, fixing."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{
		script: []verifyOutcome{{output: verifyOut, err: fmt.Errorf("exit status 1")}},
	}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "go build ./...", Timeout: 10 * time.Second},
		}},
		VerifyRunner: runner,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit the file")))

	// The tool result stored in the session must be the verify error, not
	// the original "edited" success message.
	msgs, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	var toolResult *message.ToolResultBlock
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.ToolUseID == "c1" {
				rb := b
				toolResult = &rb
			}
		}
	}
	require.NotNil(t, toolResult, "must find tool result block for c1")
	require.True(t, toolResult.IsError, "tool result must be an error when verify fails")
	require.Contains(t, toolResult.Content, verifyOut, "verify output must appear in the injected error")
	require.Contains(t, toolResult.Content, "verify_command failed", "content must identify it as a verify failure")
}

// TestVerifyDoesNotRunOnNonWriteTools asserts that when a read-only tool
// (e.g. "view") runs, the verify runner is never called.
func TestVerifyDoesNotRunOnNonWriteTools(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	// "view" is not in writeClassTools, so verify must be skipped.
	registry.Register(&recordingTool{name: "view", result: "file contents"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "view", `{"path":"main.go"}`),
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "I read the file."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "go build ./...", Timeout: 5 * time.Second},
		}},
		VerifyRunner: runner,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("read the file")))

	require.Empty(t, runner.calls, "verify must not run for non-write tools")
}

// TestVerifyDoesNotRunOnFailedWrite asserts that when a write-class tool
// returns an error result (e.g. permission denied), the verify runner is never
// called because there is nothing valid to verify.
func TestVerifyDoesNotRunOnFailedWrite(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&erroringTool{name: "edit"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "edit", `{"path":"main.go","old_string":"x","new_string":"y"}`),
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Edit failed."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "go build ./...", Timeout: 5 * time.Second},
		}},
		VerifyRunner: runner,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit the file")))

	require.Empty(t, runner.calls, "verify must not run when write-class tool returns IsError")
}

// TestVerifySkippedWhenNoVerifyHooksConfigured asserts that a Loop without
// any VerifyHooks configured does not call the runner and does not panic, and
// the original tool success message is preserved.
func TestVerifySkippedWhenNoVerifyHooksConfigured(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote file"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "write", `{"path":"main.go","content":"package main"}`),
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	// No VerifyHooks and no VerifyRunner: must run cleanly.
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write the file")))

	msgs, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	var toolResult *message.ToolResultBlock
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.ToolUseID == "c1" {
				rb := b
				toolResult = &rb
			}
		}
	}
	require.NotNil(t, toolResult)
	require.False(t, toolResult.IsError, "original success result must be preserved when no verify is configured")
}

// TestVerifyRespectsTimeoutConfiguration asserts that the timeout from the
// VerifySpec is forwarded to the runner unmodified.
func TestVerifyRespectsTimeoutConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "edit", `{"path":"main.go","old_string":"x","new_string":"y"}`),
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	const wantTimeout = 42 * time.Second
	runner := &captureVerifyRunner{
		script: []verifyOutcome{{output: "", err: nil}},
	}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "make test", Timeout: wantTimeout},
		}},
		VerifyRunner: runner,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit the file")))

	require.Len(t, runner.calls, 1)
	require.Equal(t, wantTimeout, runner.calls[0].timeout)
}

// ─── hooks.Engine.MatchingVerifiers unit tests ────────────────────────────────

// engineForVerifierTests builds a *hooks.Engine from a config.Config that
// contains the supplied hook entries. A nil shell is passed because these tests
// only call MatchingVerifiers (which never runs commands), not Fire.
func engineForVerifierTests(t *testing.T, cfgHooks []config.Hook) *hooks.Engine {
	t.Helper()
	return hooks.New(&config.Config{Hooks: cfgHooks}, nil)
}

// TestMatchingVerifiersReturnsMatchingFileEditHook asserts that MatchingVerifiers
// returns the VerifySpec for a FileEdit hook whose Match pattern matches the
// supplied file path and whose VerifyCommand is non-empty. The regex form
// (/\.go$/) is used because filepath.Match("*.go", "dir/foo.go") returns false
// (the glob * does not cross directory separators).
func TestMatchingVerifiersReturnsMatchingFileEditHook(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{Event: config.HookFileEdit, Match: "/\\.go$/", Command: "echo edited", VerifyCommand: "go build ./..."},
	})

	specs := engine.MatchingVerifiers("internal/foo.go")
	require.Len(t, specs, 1)
	require.Equal(t, "go build ./...", specs[0].Command)
}

// TestMatchingVerifiersFiltersOutHooksWithNoVerifyCommand asserts that a
// FileEdit hook without a VerifyCommand is not included in the result.
func TestMatchingVerifiersFiltersOutHooksWithNoVerifyCommand(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{Event: config.HookFileEdit, Match: "*.go", Command: "echo edited", VerifyCommand: ""},
	})

	// *.go matches top-level .go files (no path separator).
	specs := engine.MatchingVerifiers("main.go")
	require.Empty(t, specs, "hook without VerifyCommand must be excluded")
}

// TestMatchingVerifiersFiltersOutNonFileEditEvents asserts that hooks for
// other events (e.g. PreToolUse) are never returned, even if they have a
// VerifyCommand.
func TestMatchingVerifiersFiltersOutNonFileEditEvents(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{Event: config.HookPreToolUse, Match: "*.go", Command: "echo pre", VerifyCommand: "go test ./..."},
	})

	specs := engine.MatchingVerifiers("main.go")
	require.Empty(t, specs, "non-FileEdit hooks must never contribute verify specs")
}

// TestMatchingVerifiersFiltersOutNonMatchingPatterns asserts that when the
// Match pattern does not match the file path, the hook is not returned.
// *.ts matches only top-level TypeScript files; main.go is a Go file.
func TestMatchingVerifiersFiltersOutNonMatchingPatterns(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{Event: config.HookFileEdit, Match: "*.ts", Command: "echo ts", VerifyCommand: "npm test"},
	})

	specs := engine.MatchingVerifiers("main.go")
	require.Empty(t, specs)
}

// TestMatchingVerifiersEmptyMatchPatternMatchesAllPaths asserts that an empty
// Match pattern is a wildcard and matches every file path.
func TestMatchingVerifiersEmptyMatchPatternMatchesAllPaths(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{Event: config.HookFileEdit, Match: "", Command: "echo all", VerifyCommand: "go test ./..."},
	})

	specs := engine.MatchingVerifiers("any/random/path.rs")
	require.Len(t, specs, 1)
	require.Equal(t, "go test ./...", specs[0].Command)
}

// TestMatchingVerifiersOnNilEngineReturnsNil asserts that a nil *hooks.Engine
// returns nil without panicking.
func TestMatchingVerifiersOnNilEngineReturnsNil(t *testing.T) {
	var engine *hooks.Engine
	specs := engine.MatchingVerifiers("any/path.go")
	require.Nil(t, specs)
}

// TestMatchingVerifiersDefaultVerifyTimeoutApplied asserts that when
// VerifyTimeoutSeconds is zero in the config, the engine applies the default
// 30-second timeout to the VerifySpec. Uses a top-level filename so *.go
// matches via filepath.Match.
func TestMatchingVerifiersDefaultVerifyTimeoutApplied(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{
			Event:                config.HookFileEdit,
			Match:                "*.go",
			Command:              "echo ok",
			VerifyCommand:        "go build ./...",
			VerifyTimeoutSeconds: 0, // unset → default
		},
	})

	// *.go matches "main.go" (no directory separator).
	specs := engine.MatchingVerifiers("main.go")
	require.Len(t, specs, 1)
	require.Equal(t, 30*time.Second, specs[0].Timeout, "default verify timeout must be 30s")
}

// TestMatchingVerifiersExplicitVerifyTimeoutRespected asserts that a non-zero
// VerifyTimeoutSeconds in the config is converted correctly to the VerifySpec
// timeout. The regex form is used so the match works on paths with directories.
func TestMatchingVerifiersExplicitVerifyTimeoutRespected(t *testing.T) {
	engine := engineForVerifierTests(t, []config.Hook{
		{
			Event:                config.HookFileEdit,
			Match:                "/\\.go$/",
			Command:              "echo ok",
			VerifyCommand:        "cargo test",
			VerifyTimeoutSeconds: 60,
		},
	})

	specs := engine.MatchingVerifiers("src/lib.go")
	require.Len(t, specs, 1)
	require.Equal(t, 60*time.Second, specs[0].Timeout)
}
