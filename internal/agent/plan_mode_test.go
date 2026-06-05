package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestPlanModeRefusesWritesThenExecutesAfterApproval proves the full plan-mode
// lifecycle on a single Loop, deterministically and offline:
//
//   - In plan mode, a write-class tool call (edit) is refused as read-only: the
//     tool never executes, the result is an IsError "not allowed" block, the
//     plan turn still completes, and the system prompt carried the plan-mode
//     instruction.
//   - After Approve, the same Loop runs the same tool and it executes: plan mode
//     no longer restricts execution tools and the plan-mode prompt is gone.
//
// The write tool is registered so refusal happens at the allow-list check
// inside runTool (the "tool not allowed" path) rather than the earlier
// "unknown tool" path, proving the read-only restriction itself.
func TestPlanModeRefusesWritesThenExecutesAfterApproval(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	// A read-only tool (in readOnlySet) and a write-class tool (not in it).
	view := &recordingTool{name: "view", result: "file contents"}
	edit := &recordingTool{name: "edit", result: "edited"}
	registry.Register(view)
	registry.Register(edit)

	// One provider serves BOTH Runs; its scripts drain FIFO across them.
	//   Plan Run:    turn 1 calls edit (must be refused), turn 2 ends the turn.
	//   Approved Run: turn 1 calls edit (must execute), turn 2 ends the turn.
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Here is my plan; first I would edit the file."},
			llm.ToolUseEndEvent{ID: "plan-1", Name: "edit", Input: json.RawMessage(`{"path":"main.go"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Plan ready. Awaiting approval."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
		{
			llm.DeltaTextEvent{Text: "Approved; editing now."},
			llm.ToolUseEndEvent{ID: "exec-1", Name: "edit", Input: json.RawMessage(`{"path":"main.go"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "base prompt",
		PlanMode:     true,
	})

	require.True(t, loop.PlanMode(), "loop must start in plan mode")

	// --- Plan turn: write is refused, the turn still completes. ---
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("change the greeting")))

	// The edit tool never ran in plan mode.
	require.Empty(t, edit.calls, "write-class tool must not execute in plan mode")
	// The plan turn completed: both scripted turns were consumed.
	require.Len(t, provider.reqs, 2, "plan turn should run two provider turns")

	// The refusal surfaced as an IsError tool-result mentioning the restriction.
	planResult := toolResultFor(t, repo, ctx, sessionID, "plan-1")
	require.True(t, planResult.IsError, "refused write must be an error result")
	require.Contains(t, planResult.Content, "not allowed",
		"refusal must explain the tool is not allowed (read-only)")

	// The plan-mode instruction reached the provider via the system prompt.
	require.Contains(t, provider.reqs[0].SystemPrompt, "Plan mode is active",
		"plan turn must prompt the agent to produce a plan")

	// In plan mode, the offered tool list excludes write-class tools.
	require.True(t, hasLLMTool(loop, "view"), "read-only tool stays offered in plan mode")
	require.False(t, hasLLMTool(loop, "edit"), "write-class tool is withheld in plan mode")

	// --- Approve: execution tools become available. ---
	loop.Approve()
	require.False(t, loop.PlanMode(), "Approve must leave plan mode")
	require.True(t, hasLLMTool(loop, "edit"), "write-class tool is offered after approval")

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("go ahead")))

	// The edit tool executed exactly once after approval.
	require.Len(t, edit.calls, 1, "write-class tool must execute after approval")
	require.Equal(t, `{"path":"main.go"}`, edit.calls[0])

	// The exec tool-result is a success (no error).
	execResult := toolResultFor(t, repo, ctx, sessionID, "exec-1")
	require.False(t, execResult.IsError, "approved write must succeed")
	require.Equal(t, "edited", execResult.Content)

	// The post-approval provider request no longer carries the plan-mode prompt.
	require.Len(t, provider.reqs, 4, "approved turn should run two more provider turns")
	require.NotContains(t, provider.reqs[2].SystemPrompt, "Plan mode is active",
		"approved turn must not prompt for a plan")
	require.Equal(t, "base prompt", provider.reqs[2].SystemPrompt,
		"approved turn uses the unmodified base system prompt")
}

// toolResultFor returns the tool-result block for the given tool-use ID from the
// session's persisted messages, failing the test if none is found.
func toolResultFor(t *testing.T, repo interface {
	Messages(context.Context, string) ([]message.Message, error)
}, ctx context.Context, sessionID, toolUseID string,
) message.ToolResultBlock {
	t.Helper()
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	for _, msg := range messages {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.ToolUseID == toolUseID {
				return b
			}
		}
	}
	t.Fatalf("no tool-result block found for tool-use %q", toolUseID)
	return message.ToolResultBlock{}
}

// TestPlanModeOnlyRestrictsExistingAllowList proves plan mode intersects with a
// configured allow-list rather than replacing it: a custom agent restricted to
// {"view"} does not gain other read-only tools (grep, ls) when plan mode is on.
func TestPlanModeOnlyRestrictsExistingAllowList(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view"})
	registry.Register(&recordingTool{name: "grep"})
	registry.Register(&recordingTool{name: "edit"})

	loop := New(Config{
		Name:          "custom",
		Model:         "fake-model",
		Provider:      &scriptProvider{},
		Tools:         registry,
		Sessions:      testRepo(t),
		ToolAllowList: []string{"view"},
		PlanMode:      true,
	})

	// view is in the allow-list and read-only: allowed.
	require.True(t, loop.toolAllowed("view"))
	// grep is read-only but NOT in the configured allow-list: plan mode must not
	// expand the set, so it stays refused.
	require.False(t, loop.toolAllowed("grep"))
	// edit is neither allow-listed nor read-only: refused.
	require.False(t, loop.toolAllowed("edit"))

	// After approval the allow-list still governs: only view is allowed.
	loop.Approve()
	require.True(t, loop.toolAllowed("view"))
	require.False(t, loop.toolAllowed("grep"))
	require.False(t, loop.toolAllowed("edit"))
}

// TestPlanModeOffPreservesDefaultBehavior proves the non-breaking default: with
// plan mode off and no allow-list, every tool is allowed and the system prompt
// is unmodified, exactly as before plan mode existed.
func TestPlanModeOffPreservesDefaultBehavior(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit"})
	registry.Register(&recordingTool{name: "bash"})

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     &scriptProvider{},
		Tools:        registry,
		Sessions:     testRepo(t),
		SystemPrompt: "base prompt",
	})

	require.False(t, loop.PlanMode())
	require.True(t, loop.toolAllowed("edit"))
	require.True(t, loop.toolAllowed("bash"))
	require.Equal(t, "base prompt", loop.systemPrompt())
	require.False(t, strings.Contains(loop.systemPrompt(), "Plan mode is active"))
}

// TestPlanModePromptPhasedWorkflow proves the plan-mode system prompt carries
// the verbatim hard read-only rule and the full phased read-only workflow when
// plan mode is set, and that none of those phrases leak into the base prompt
// once plan mode is cleared. This pins the prompt content the loop appends in
// systemPrompt() so a regression in the const is caught deterministically.
func TestPlanModePromptPhasedWorkflow(t *testing.T) {
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     &scriptProvider{},
		Tools:        newFakeRegistry(),
		Sessions:     testRepo(t),
		SystemPrompt: "base prompt",
		PlanMode:     true,
	})

	prompt := loop.systemPrompt()

	// The base prompt is still present, with the plan-mode reminder appended.
	require.True(t, strings.HasPrefix(prompt, "base prompt"),
		"plan-mode prompt must extend, not replace, the base system prompt")
	// Framed as a system reminder.
	require.Contains(t, prompt, "<system-reminder>")
	require.Contains(t, prompt, "</system-reminder>")

	// The verbatim hard read-only rule, including the supersede clause.
	wantVerbatim := []string{
		"Plan mode is active",
		"you MUST NOT make any edits",
		"non-readonly tools (including changing configs or making commits)",
		"This supersedes any other instructions you have received",
	}
	for _, phrase := range wantVerbatim {
		require.Contains(t, prompt, phrase,
			"plan-mode prompt must carry the verbatim hard rule: %q", phrase)
	}

	// Every phase of the read-only workflow, in the right shape: investigate,
	// clarify before designing, design, review, then write the plan and stop.
	wantPhases := []string{
		"Phase 1 - Investigate",
		"Do focused exploration yourself",
		"Phase 2 - Clarify",
		"BEFORE you start designing",
		"Phase 3 - Design",
		"Phase 4 - Review",
		"Phase 5 - Write the plan, then STOP",
		"Verification section",
		"wait for approval",
	}
	for _, phrase := range wantPhases {
		require.Contains(t, prompt, phrase,
			"plan-mode prompt must describe the phased workflow: %q", phrase)
	}

	// BharatCode has no subagent spawning, so the prompt must keep exploration
	// in-process and must not promise a delegate.
	require.Contains(t, prompt, "There is no subagent to delegate to")

	// With plan mode cleared, none of the plan-mode phrases survive.
	loop.Approve()
	require.False(t, loop.PlanMode())
	cleared := loop.systemPrompt()
	require.Equal(t, "base prompt", cleared,
		"approved prompt must equal the unmodified base prompt")
	for _, phrase := range append(append([]string{}, wantVerbatim...), wantPhases...) {
		require.NotContains(t, cleared, phrase,
			"cleared prompt must not contain plan-mode phrase: %q", phrase)
	}
}
