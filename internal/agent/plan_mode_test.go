package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
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
	require.True(t, strings.HasPrefix(provider.reqs[2].SystemPrompt, "base prompt"),
		"approved turn leads with the unmodified base system prompt")
	// The approved turn carries the persistent active-goal frame: the session's
	// original request is re-injected so the agent stays anchored to it.
	require.Contains(t, provider.reqs[2].SystemPrompt, "Active goal for this session",
		"approved turn re-injects the active-goal frame")
	require.Contains(t, provider.reqs[2].SystemPrompt, "change the greeting",
		"active-goal frame carries the user's original request")
}

// TestExtractPlanTextFromAssistantMessage proves that ExtractPlanText pulls the
// plan out of a representative plan-mode assistant message. The message mirrors
// what the agent produces at the end of a plan turn: a text block with the
// phased plan, optionally preceded by a tool-use block (from the investigation
// phase). ExtractPlanText must return the concatenated text blocks, trimmed,
// and must ignore non-text blocks so the plan is clean prose.
func TestExtractPlanTextFromAssistantMessage(t *testing.T) {
	planBody := "Phase 1 - Investigate: read main.go\n\nPhase 5 - Write the plan, then STOP:\n1. Edit main.go line 42.\n2. Run go test ./...\n3. Verify: go build ./..."

	// Representative plan-mode assistant message: a tool-use block from the
	// investigation phase followed by the plan text block. ExtractPlanText
	// must return only the text, not the raw JSON of the tool-use block.
	msg := message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.ToolUseBlock{
				ID:    "inv-1",
				Name:  "view",
				Input: []byte(`{"path":"main.go"}`),
			},
			message.TextBlock{Text: planBody},
		},
	}

	got := ExtractPlanText(msg)
	require.Equal(t, planBody, got,
		"ExtractPlanText must return the exact plan text, trimmed, from a mixed-block message")

	// A message with leading/trailing whitespace around the text is trimmed.
	msgWithSpace := message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "\n  " + planBody + "\n\n"}},
	}
	require.Equal(t, planBody, ExtractPlanText(msgWithSpace),
		"ExtractPlanText must trim surrounding whitespace")

	// A tool-only message (no text blocks) returns an empty string.
	toolOnly := message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.ToolUseBlock{ID: "x", Name: "view", Input: []byte(`{}`)},
		},
	}
	require.Equal(t, "", ExtractPlanText(toolOnly),
		"ExtractPlanText must return empty string for a tool-use-only message")

	// Multiple consecutive text blocks are concatenated in order.
	multi := message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.TextBlock{Text: "Step 1: do A."},
			message.TextBlock{Text: "\nStep 2: do B."},
		},
	}
	require.Equal(t, "Step 1: do A.\nStep 2: do B.", ExtractPlanText(multi),
		"ExtractPlanText must concatenate multiple text blocks in order")
}

// TestApprovePlanTransitionsStateAndPreservesPlan proves the coordinator-side
// approve-plan state machine:
//
//   - StorePlan records the plan for the session.
//   - PlanFor retrieves it without consuming it (idempotent reads).
//   - ApprovePlan calls loop.Approve (plan mode off) AND returns the stored
//     plan, not an empty string — the plan is passed through, not dropped.
//   - After ApprovePlan the plan is consumed: a second call returns empty.
//   - A session that never had StorePlan called returns empty from PlanFor and
//     ApprovePlan without panicking.
func TestApprovePlanTransitionsStateAndPreservesPlan(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view"})
	registry.Register(&recordingTool{name: "edit"})

	// Build a Loop in plan mode and a Coordinator to manage its plan state.
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: &scriptProvider{},
		Tools:    registry,
		Sessions: testRepo(t),
		PlanMode: true,
	})

	cfg := &config.Config{}
	coord, err := NewCoordinator(cfg, Dependencies{})
	require.NoError(t, err)

	const sessionID = "session-plan-approve-test"
	const planText = "Phase 5 - Write the plan:\n1. Edit foo.go.\n2. Run go build ./...\n3. Verify: go test ./..."

	// Before any StorePlan call, PlanFor and ApprovePlan return empty without
	// panicking.
	require.Equal(t, "", coord.PlanFor(sessionID),
		"PlanFor must return empty when no plan has been stored")

	require.True(t, loop.PlanMode(), "loop must be in plan mode before ApprovePlan")
	returnedEmpty := coord.ApprovePlan(sessionID, loop)
	require.Equal(t, "", returnedEmpty,
		"ApprovePlan must return empty when no plan was stored")
	// ApprovePlan still transitions plan mode even when no plan text was stored.
	require.False(t, loop.PlanMode(),
		"ApprovePlan must leave plan mode even when no plan text was stored")

	// Reset back to plan mode for the main scenario.
	loop.SetPlanMode(true)
	require.True(t, loop.PlanMode(), "loop must be back in plan mode for the main test")

	// StorePlan records the plan; PlanFor can read it without consuming it.
	coord.StorePlan(sessionID, planText)
	require.Equal(t, planText, coord.PlanFor(sessionID),
		"PlanFor must return the stored plan text")
	require.Equal(t, planText, coord.PlanFor(sessionID),
		"PlanFor must be idempotent — reading twice must not consume the plan")

	// ApprovePlan clears plan mode on the loop AND returns the plan text.
	// The plan must not be silently dropped.
	got := coord.ApprovePlan(sessionID, loop)

	require.False(t, loop.PlanMode(),
		"ApprovePlan must transition the loop out of plan mode")
	require.Equal(t, planText, got,
		"ApprovePlan must return the stored plan text, not drop it")

	// After ApprovePlan the plan is consumed: subsequent calls return empty.
	require.Equal(t, "", coord.PlanFor(sessionID),
		"PlanFor must return empty after the plan has been consumed by ApprovePlan")
	require.Equal(t, "", coord.ApprovePlan(sessionID, loop),
		"second ApprovePlan call must return empty — plan consumed exactly once")
}

// TestSeedMessageFromPlanCarriesPlanText proves SeedMessageFromPlan builds the
// correct synthetic user message that seeds the execution turn with the
// approved plan. The message must contain the plan text verbatim and must use
// the correct role and session ID. An empty plan falls back to a plain "go
// ahead" message rather than an empty body.
func TestSeedMessageFromPlanCarriesPlanText(t *testing.T) {
	const sessionID = "session-seed-test"
	const planText = "1. Edit main.go line 10.\n2. Run go test ./..."

	msg := SeedMessageFromPlan(sessionID, planText)

	require.Equal(t, message.RoleUser, msg.Role,
		"seed message must have user role so it enters the conversation as the next turn prompt")
	require.Equal(t, sessionID, msg.SessionID,
		"seed message must carry the session ID")
	require.Len(t, msg.Content, 1, "seed message must have exactly one content block")
	tb, ok := msg.Content[0].(message.TextBlock)
	require.True(t, ok, "seed message content must be a TextBlock")
	require.Contains(t, tb.Text, planText,
		"seed message body must include the approved plan text verbatim")
	require.Contains(t, tb.Text, "Execute the approved plan",
		"seed message must instruct the agent to execute the approved plan")

	// Empty plan falls back to a non-empty instructional message, not an empty body.
	emptyMsg := SeedMessageFromPlan(sessionID, "")
	tb2, ok := emptyMsg.Content[0].(message.TextBlock)
	require.True(t, ok)
	require.NotEmpty(t, tb2.Text,
		"seed message from empty plan must still have a non-empty body")
	require.Contains(t, tb2.Text, "Plan approved",
		"empty-plan seed message must still tell the agent to proceed")
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
