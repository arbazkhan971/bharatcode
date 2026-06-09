package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/skills"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRunDrivesToolLoopAndPersistsMessages(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	view := &recordingTool{name: "view", result: "hello"}
	edit := &recordingTool{name: "edit", result: "edited"}
	registry.Register(view)
	registry.Register(edit)
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "I will inspect it."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "view", Input: json.RawMessage(`{"path":"testdata/hello.txt"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.ToolUseEndEvent{ID: "call-2", Name: "edit", Input: json.RawMessage(`{"path":"testdata/hello.txt"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 12, OutputTokens: 6}},
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
		SystemPrompt: "test prompt",
	})
	err := loop.Run(ctx, sessionID, userMessage("please update it"))
	require.NoError(t, err)

	require.Equal(t, []string{`{"path":"testdata/hello.txt"}`}, view.calls)
	require.Equal(t, []string{`{"path":"testdata/hello.txt"}`}, edit.calls)
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, messages, 6)
	require.Equal(t, message.RoleUser, messages[0].Role)
	require.Equal(t, message.RoleAssistant, messages[1].Role)
	require.Equal(t, message.RoleUser, messages[2].Role)
	require.Equal(t, message.RoleAssistant, messages[5].Role)
}

func TestRunInvokesSkillToolAndInjectsBody(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Load a real skill set from a temp dir holding one SKILL.md. The body
	// is a distinctive multi-word marker, deliberately different from the
	// description, so the assertion proves the tool returns the BODY and not
	// the summary the system prompt already advertises.
	const (
		skillDescription = "Cut a tagged release"
		skillBody        = "RELEASE-BODY-MARKER-7c2f: run the release checklist step by step."
	)
	skillsRoot := filepath.Join(t.TempDir(), ".bharatcode", "skills")
	writeSkillFixture(t, skillsRoot, "release",
		"---\nname: release\ndescription: "+skillDescription+"\n---\n"+skillBody+"\n")
	set, err := skills.LoadSkills(skillsRoot)
	require.NoError(t, err)
	loaded, ok := set.Get("release")
	require.True(t, ok, "fixture skill must load")
	require.Equal(t, skillBody, loaded.Body, "fixture body must survive parsing")

	registry := newFakeRegistry()
	registry.Register(newSkillTool(set))

	provider := &scriptProvider{scripts: [][]llm.Event{
		// Turn 1: the model invokes the skill tool by name.
		{
			llm.DeltaTextEvent{Text: "Loading the release skill."},
			llm.ToolUseEndEvent{ID: "call-1", Name: skillToolName, Input: json.RawMessage(`{"name":"release"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		// Turn 2: text-only reply ends the turn — the loop must have continued.
		{
			llm.DeltaTextEvent{Text: "Skill loaded. Proceeding."},
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
		SystemPrompt: "test prompt",
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("use the release skill")))

	// The loop continued past the tool call: two provider turns ran and the
	// final persisted message is the second turn's assistant text.
	require.Len(t, provider.reqs, 2)

	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	last := messages[len(messages)-1]
	require.Equal(t, message.RoleAssistant, last.Role)
	require.Contains(t, textOf(last), "Skill loaded. Proceeding.")

	// The skill BODY actually reached the conversation as the tool result.
	var skillResult *message.ToolResultBlock
	for _, msg := range messages {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.ToolUseID == "call-1" {
				rb := b
				skillResult = &rb
			}
		}
	}
	require.NotNil(t, skillResult, "expected a tool-result block for the skill call")
	require.False(t, skillResult.IsError)
	require.Equal(t, skillBody, skillResult.Content, "tool must return the skill body verbatim")
	require.Contains(t, skillResult.Content, "RELEASE-BODY-MARKER-7c2f")
	// Returning the summary instead of the body must fail this test.
	require.NotEqual(t, skillDescription, skillResult.Content)
	require.NotContains(t, skillResult.Content, skillDescription)
}

func TestCoordinatorWiresSkillToolIntoAgents(t *testing.T) {
	// Point the skill loader at a hermetic temp root so the test never reads
	// the developer's real ~/.bharatcode/skills and only sees this fixture.
	skillsRoot := filepath.Join(t.TempDir(), ".bharatcode", "skills")
	writeSkillFixture(t, skillsRoot, "release",
		"---\nname: release\ndescription: Cut a tagged release\n---\nbody text here\n")
	restore := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{skillsRoot} }
	t.Cleanup(func() { skillSearchDirs = restore })

	coord, err := NewCoordinator(nil, Dependencies{
		Tools:     tools.NewRegistry(tools.Dependencies{}),
		Sessions:  testRepo(t),
		Providers: map[string]llm.Provider{"fake": &scriptProvider{}},
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	// The unrestricted "coder" agent must see the skill tool, and so must
	// the read-only "task" agent (its allow-list includes "skill"). This
	// exercises effectiveRegistry -> combinedTools.extra -> readOnlyTaskTools,
	// which the Loop-level test above bypasses.
	for _, name := range []string{"coder", "task"} {
		loop, err := coord.Agent(name)
		require.NoError(t, err, "agent %q", name)
		require.True(t, hasLLMTool(loop, skillToolName), "agent %q must expose the skill tool", name)
	}
}

func TestCoordinatorWiresMCPResourcesToolIntoAgents(t *testing.T) {
	// No MCP client is configured, yet the read-only "mcp_resources" tool must
	// still be registered so the task agent's allow-list (which includes it) does
	// not fail Loop tool validation, and so the coder agent can call it.
	coord, err := NewCoordinator(nil, Dependencies{
		Tools:     tools.NewRegistry(tools.Dependencies{}),
		Sessions:  testRepo(t),
		Providers: map[string]llm.Provider{"fake": &scriptProvider{}},
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	for _, name := range []string{"coder", "task"} {
		loop, err := coord.Agent(name)
		require.NoError(t, err, "agent %q", name)
		require.True(t, hasLLMTool(loop, "mcp_resources"), "agent %q must expose mcp_resources", name)
		require.True(t, hasLLMTool(loop, "mcp_prompts"), "agent %q must expose mcp_prompts", name)
	}
}

func hasLLMTool(loop *Loop, name string) bool {
	for _, tool := range loop.llmTools() {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestSkillToolUnknownSkillReturnsError(t *testing.T) {
	skillsRoot := filepath.Join(t.TempDir(), ".bharatcode", "skills")
	writeSkillFixture(t, skillsRoot, "release",
		"---\nname: release\ndescription: Cut a tagged release\n---\nbody text here\n")
	set, err := skills.LoadSkills(skillsRoot)
	require.NoError(t, err)

	tool := newSkillTool(set)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"name":"missing"}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown skill: missing")

	// A missing name is an error result, not a Go error.
	res, err = tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "skill name is required")
}

func TestMaxStepsGrantsToolFreeHandoffTurn(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	bash := &recordingTool{name: "bash", result: "ok"}
	registry.Register(bash)

	// MaxSteps is 3: the first two turns keep calling a tool, and the third is
	// the tools-disabled handoff turn where the model can only reply with text.
	// The distinctive summary marker proves the handoff text is recorded rather
	// than a canned "step limit reached" string.
	const summary = "HANDOFF-MARKER-4d1: limit reached, ran bash twice, remaining: finish edits."
	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("1", "bash", `{"command":"echo a"}`), llm.EndEvent{}},
		{toolCall("2", "bash", `{"command":"echo b"}`), llm.EndEvent{}},
		{
			llm.DeltaTextEvent{Text: summary},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
		// A guard turn that would keep calling tools if the loop ever ran a
		// fourth step; it must never be consumed.
		{toolCall("3", "bash", `{"command":"echo c"}`), llm.EndEvent{}},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "test prompt",
		MaxSteps:     3,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("do a lot of work")))

	// Exactly MaxSteps provider calls ran: the loop stopped at the step limit
	// and never consumed the guard turn.
	require.Len(t, provider.reqs, 3)

	// Every turn before the final one offered tools; the final handoff turn sent
	// no tools so the model could only reply with text.
	require.NotEmpty(t, provider.reqs[0].Tools, "non-final turns must offer tools")
	require.NotEmpty(t, provider.reqs[1].Tools)
	require.Empty(t, provider.reqs[len(provider.reqs)-1].Tools, "final turn must send no tools")

	// The final turn's system prompt carries the max-steps handoff instruction;
	// earlier turns do not.
	finalPrompt := provider.reqs[len(provider.reqs)-1].SystemPrompt
	require.Contains(t, finalPrompt, "test prompt", "base prompt is preserved")
	require.Contains(t, finalPrompt, "Maximum steps reached")
	require.Contains(t, finalPrompt, "step limit")
	require.Contains(t, finalPrompt, "tasks that still remain")
	require.Contains(t, finalPrompt, "recommendations")
	require.NotContains(t, provider.reqs[0].SystemPrompt, "Maximum steps reached")

	// The recorded final reply is the model's handoff summary, not a canned
	// dead-end string.
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	last := messages[len(messages)-1]
	require.Equal(t, message.RoleAssistant, last.Role)
	require.Equal(t, summary, textOf(last))
	require.NotContains(t, textOf(last), "step limit reached")

	// The tool ran only on the two non-final turns; the handoff turn executed no
	// tools even though a guard tool call was scripted.
	require.Len(t, bash.calls, 2)
}

func TestMaxStepsFinalTurnFlagResetsAfterRun(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "bash", result: "ok"})

	// MaxSteps is 2. The first Run keeps calling a tool, so step 0 is normal and
	// step 1 is the tools-disabled handoff turn that hits the step limit. The
	// second Run on the same Loop must start fresh: its first step is a normal,
	// tool-enabled turn that ends with text, proving finalTurn was cleared on the
	// first Run's exit and did not leak across runs.
	provider := &scriptProvider{scripts: [][]llm.Event{
		// First Run.
		{toolCall("1", "bash", `{"command":"echo a"}`), llm.EndEvent{}}, // step 0
		{llm.DeltaTextEvent{Text: "handoff one"}, llm.EndEvent{}},       // step 1 (final)
		// Second Run.
		{llm.DeltaTextEvent{Text: "normal reply"}, llm.EndEvent{}}, // step 0, ends turn
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		SystemPrompt: "test prompt",
		MaxSteps:     2,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("first")))
	require.False(t, loop.finalTurn.Load(), "finalTurn must be cleared after Run")

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("second")))
	require.Len(t, provider.reqs, 3)
	// The second Run's first turn offered tools and used the base prompt: the
	// handoff state did not leak across runs.
	require.NotEmpty(t, provider.reqs[2].Tools)
	require.NotContains(t, provider.reqs[2].SystemPrompt, "Maximum steps reached")
}

func TestLoopDetectionStopsBeforeThirdIdenticalToolRun(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	bash := &recordingTool{name: "bash", result: "x"}
	registry.Register(bash)
	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("1", "bash", `{"command":"echo x"}`), llm.EndEvent{}},
		{toolCall("2", "bash", `{"command":"echo x"}`), llm.EndEvent{}},
		{toolCall("3", "bash", `{"command":"echo x"}`), llm.EndEvent{}},
		{toolCall("4", "bash", `{"command":"echo x"}`), llm.EndEvent{}},
	}}
	bus := pubsub.NewTopic[Event]("agent-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      bus,
	})
	err := loop.Run(ctx, sessionID, userMessage("loop"))
	require.NoError(t, err)
	require.Len(t, bash.calls, 2)

	var sawLoop bool
	for {
		select {
		case event := <-events:
			if event.Kind == EventLoopDetected {
				sawLoop = true
			}
		default:
			require.True(t, sawLoop)
			messages, err := repo.Messages(ctx, sessionID)
			require.NoError(t, err)
			last := messages[len(messages)-1]
			require.Contains(t, textOf(last), ErrLoopDetected.Error())
			return
		}
	}
}

// driveDetector replays a sequence of (call,result) steps through the detector
// exactly as the agent loop does: wouldRepeat is consulted before a call runs,
// and record is invoked after it produces a result. It returns the 1-based step
// index at which the guard tripped (and which gate fired), or 0 if it never did.
// This keeps the unit tests faithful to loop.go without spinning up a Loop.
func driveDetector(steps []detectorStep) (trippedAt int, gate string) {
	d := &loopDetector{}
	for i, step := range steps {
		callHash, err := toolCallHash(step.tool, json.RawMessage(step.args))
		if err != nil {
			return i + 1, "hash-error"
		}
		if d.wouldRepeat(callHash) {
			return i + 1, "predict"
		}
		if d.record(callHash, resultHash(step.result), step.isError) {
			return i + 1, "cycle"
		}
	}
	return 0, ""
}

type detectorStep struct {
	tool    string
	args    string
	result  string
	isError bool
}

// TestLoopDetectorToleratesChangingOutput asserts the result-aware guard does
// NOT trip when the same call keeps producing different output: four identical
// calls whose results differ each time are legitimate progress, not a loop. The
// old call-only counter tripped on the third such call; this test fails against
// that behavior.
func TestLoopDetectorToleratesChangingOutput(t *testing.T) {
	trippedAt, gate := driveDetector([]detectorStep{
		{tool: "bash", args: `{"command":"tail -n1 log"}`, result: "line-1"},
		{tool: "bash", args: `{"command":"tail -n1 log"}`, result: "line-2"},
		{tool: "bash", args: `{"command":"tail -n1 log"}`, result: "line-3"},
		{tool: "bash", args: `{"command":"tail -n1 log"}`, result: "line-4"},
	})
	require.Equal(t, 0, trippedAt, "same call with changing output must not trip (gate=%q)", gate)
}

// TestLoopDetectorTripsOnIdenticalErrorThrice asserts that the same call
// returning the same error result three times trips the guard. The guard must
// fire on the third call before it runs (the predictive gate), so the futile
// retry is never executed.
func TestLoopDetectorTripsOnIdenticalErrorThrice(t *testing.T) {
	trippedAt, gate := driveDetector([]detectorStep{
		{tool: "bash", args: `{"command":"make"}`, result: "permission denied", isError: true},
		{tool: "bash", args: `{"command":"make"}`, result: "permission denied", isError: true},
		{tool: "bash", args: `{"command":"make"}`, result: "permission denied", isError: true},
	})
	require.Equal(t, 3, trippedAt, "identical error thrice must trip on the third call")
	require.Equal(t, "predict", gate, "the third identical call must trip before running")
}

// TestLoopDetectorTripsOnAlternatingCycle asserts that an A,B,A,B oscillation of
// two distinct (call,result) steps trips the guard. The predictive gate cannot
// catch this because consecutive calls differ; the cycle is found by record.
func TestLoopDetectorTripsOnAlternatingCycle(t *testing.T) {
	a := detectorStep{tool: "edit", args: `{"path":"x","s":"foo"}`, result: "applied"}
	b := detectorStep{tool: "edit", args: `{"path":"x","s":"bar"}`, result: "applied"}
	trippedAt, gate := driveDetector([]detectorStep{a, b, a, b})
	require.Equal(t, 4, trippedAt, "A,B,A,B must trip once the fourth step lands")
	require.Equal(t, "cycle", gate, "the alternating pattern must trip via record")
}

// TestLoopDetectorWhitespaceLossless guards the removal of the lossy trailing-
// whitespace trim: two calls whose only difference is trailing whitespace in an
// argument are DISTINCT and must not be collapsed into a loop. The old detector
// stripped trailing whitespace, so it would have hashed these identically; this
// test fails against that behavior.
func TestLoopDetectorWhitespaceLossless(t *testing.T) {
	clean, err := toolCallHash("bash", json.RawMessage(`{"command":"echo hi"}`))
	require.NoError(t, err)
	trailing, err := toolCallHash("bash", json.RawMessage(`{"command":"echo hi   "}`))
	require.NoError(t, err)
	require.NotEqual(t, clean, trailing, "trailing whitespace must remain significant in the call hash")

	// And driving three such "almost identical" calls must not trip, because the
	// trailing-space variant is genuinely a different call.
	trippedAt, _ := driveDetector([]detectorStep{
		{tool: "bash", args: `{"command":"echo hi"}`, result: "hi"},
		{tool: "bash", args: `{"command":"echo hi "}`, result: "hi "},
		{tool: "bash", args: `{"command":"echo hi  "}`, result: "hi  "},
	})
	require.Equal(t, 0, trippedAt, "whitespace-differing calls must not collapse into a loop")
}

func TestRunRecoversFromPanickingToolAndContinues(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(&panickingTool{name: "boom", panicMsg: "kaboom"})
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Running the tool."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "boom", Input: json.RawMessage(`{}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}
	bus := pubsub.NewTopic[Event]("agent-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      bus,
	})

	// (a) Run returns without the panic escaping the agent goroutine.
	err := loop.Run(ctx, sessionID, userMessage("explode please"))
	require.NoError(t, err)

	// (c) An EventRunError was published for the panicking tool.
	var sawRunError bool
	for {
		stop := false
		select {
		case event := <-events:
			if event.Kind == EventRunError && event.ToolName == "boom" {
				sawRunError = true
				require.Error(t, event.Err)
				require.Contains(t, event.Err.Error(), "panicked")
			}
		default:
			stop = true
		}
		if stop {
			break
		}
	}
	require.True(t, sawRunError, "expected an EventRunError for the panicking tool")

	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)

	// (b) The tool produced an IsError result whose content mentions the panic.
	var toolResult *message.ToolResultBlock
	for _, msg := range messages {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok {
				rb := b
				toolResult = &rb
			}
		}
	}
	require.NotNil(t, toolResult, "expected a tool-result block in the session")
	require.True(t, toolResult.IsError)
	require.Contains(t, toolResult.Content, "panicked")
	require.Contains(t, toolResult.Content, "kaboom")

	// (d) The loop continued: the scripted "Done." assistant message was processed.
	require.Len(t, provider.reqs, 2)
	last := messages[len(messages)-1]
	require.Equal(t, message.RoleAssistant, last.Role)
	require.Contains(t, textOf(last), "Done.")
}

func TestInterruptCancelsRun(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	provider := &blockingProvider{started: make(chan struct{})}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx, sessionID, userMessage("wait"))
	}()
	<-provider.started
	loop.Interrupt()

	select {
	case err := <-errCh:
		require.Error(t, err)
		require.True(t, errors.Is(err, context.Canceled), err.Error())
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after Interrupt")
	}
}

func TestCoordinatorBuiltinsListDeterministically(t *testing.T) {
	provider := &scriptProvider{}
	coord, err := NewCoordinator(nil, Dependencies{
		Sessions:  testRepo(t),
		Providers: map[string]llm.Provider{"fake": provider},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"coder", "task"}, coord.List())
}

func TestRenderPromptIncludesEnvironmentAndTools(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", desc: "Read a file."})
	prompt, err := renderPrompt(context.Background(), "coder", "", registry, nil)
	require.NoError(t, err)
	require.Contains(t, prompt, "Working directory:")
	require.Contains(t, prompt, "Platform:")
	require.Contains(t, prompt, "Git branch:")
	require.Contains(t, prompt, "view: Read a file.")
}

func TestInjectInstructionsAppendsWhenProvided(t *testing.T) {
	base := "You are BharatCode's primary coding agent."
	instr := "PROJECT-RULE: never log secrets."

	out := injectInstructions(base, []instructionSource{{Dir: "/repo/app", Content: instr}})

	// Base prompt is preserved.
	require.Contains(t, out, base)
	// Injected instructions are present, under the delimited header.
	require.Contains(t, out, projectInstructionsHeader)
	require.Contains(t, out, instr)
	// Each source is wrapped in a path-attributed XML block so the model can
	// attribute every rule to the directory it came from.
	require.Contains(t, out, "<project_context>")
	require.Contains(t, out, `<project_instructions path="/repo/app">`)
	require.Contains(t, out, "</project_instructions>")
	require.Contains(t, out, "</project_context>")
	// The injected section comes after the base prompt.
	require.Less(t, strings.Index(out, base), strings.Index(out, instr))
	// The header introduces the instructions (delimited section).
	require.Less(t, strings.Index(out, projectInstructionsHeader), strings.Index(out, instr))
}

func TestInjectInstructionsEmptyLeavesBaseUnchanged(t *testing.T) {
	base := "You are BharatCode's primary coding agent."

	require.Equal(t, base, injectInstructions(base, nil))
	require.Equal(t, base, injectInstructions(base, []instructionSource{}))
	require.Equal(t, base, injectInstructions(base, []instructionSource{{Dir: "/repo", Content: "   \n\t  "}}))
	// No delimiter header leaks in when there is nothing to inject.
	require.NotContains(t, injectInstructions(base, nil), projectInstructionsHeader)
}

func TestRenderPromptInjectsProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	instr := "PROJECT-MARKER-9f3a: enforce gofumpt on save."
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(instr), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	require.NoError(t, os.Chdir(dir))

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", desc: "Read a file."})

	prompt, err := renderPrompt(context.Background(), "coder", "", registry, nil)
	require.NoError(t, err)

	require.Contains(t, prompt, projectInstructionsHeader)
	require.Contains(t, prompt, instr)
	// The AGENTS.md rule renders inside a path-attributed block naming the
	// directory it was loaded from. os.Getwd may resolve symlinks (e.g.
	// /var -> /private/var on macOS), so attribute against the working
	// directory the prompt actually saw rather than the raw temp dir.
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Contains(t, prompt, `<project_instructions path="`+wd+`">`)
}

func testRepo(t *testing.T) *session.Repo {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return session.NewRepo(database)
}

func testSession(t *testing.T, repo *session.Repo) string {
	t.Helper()
	s := &session.Session{
		ID:          "session-" + time.Now().Format("150405.000000000"),
		ProjectPath: t.TempDir(),
		Title:       "New session",
		Model:       "fake-model",
		Agent:       "coder",
	}
	require.NoError(t, repo.Create(context.Background(), s))
	return s.ID
}

func userMessage(text string) message.Message {
	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}

type fakeRegistry struct {
	mu    sync.RWMutex
	tools map[string]tools.Tool
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{tools: map[string]tools.Tool{}}
}

func (r *fakeRegistry) Register(t tools.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *fakeRegistry) Get(name string) (tools.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *fakeRegistry) List() []tools.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]tools.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func toolCall(id, name, input string) llm.Event {
	return llm.ToolUseEndEvent{ID: id, Name: name, Input: json.RawMessage(input)}
}

func textOf(msg message.Message) string {
	var out string
	for _, block := range msg.Content {
		if b, ok := block.(message.TextBlock); ok {
			out += b.Text
		}
	}
	return out
}

type recordingTool struct {
	name    string
	desc    string
	result  string
	isError bool
	mu      sync.Mutex
	calls   []string
}

func (t *recordingTool) Name() string {
	return t.name
}

func (t *recordingTool) Description() string {
	if t.desc != "" {
		return t.desc
	}
	return "Test tool " + t.name
}

func (t *recordingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *recordingTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, string(args))
	return tools.Result{Content: t.result, IsError: t.isError}, nil
}

type panickingTool struct {
	name     string
	panicMsg string
}

func (t *panickingTool) Name() string {
	return t.name
}

func (t *panickingTool) Description() string {
	return "Tool that panics for " + t.name
}

func (t *panickingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *panickingTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	_ = args
	panic(t.panicMsg)
}

type scriptProvider struct {
	mu      sync.Mutex
	scripts [][]llm.Event
	reqs    []llm.Request
	// contextWindow overrides the reported model context window. When zero, it
	// defaults to 8192 so existing tests are unaffected.
	contextWindow int
	// models overrides the reported model catalog. When nil, the provider
	// reports a single "fake-model" so existing tests are unaffected. Routing
	// tests set this to expose two priced, named models.
	models []llm.Model
}

func (p *scriptProvider) Name() string {
	return "fake"
}

func (p *scriptProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	var events []llm.Event
	if len(p.scripts) > 0 {
		events = p.scripts[0]
		p.scripts = p.scripts[1:]
	}
	p.mu.Unlock()
	ch := make(chan llm.Event, len(events))
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch, nil
}

func (p *scriptProvider) Models() []llm.Model {
	if p.models != nil {
		return p.models
	}
	window := p.contextWindow
	if window == 0 {
		window = 8192
	}
	return []llm.Model{{
		ID:            "fake-model",
		Provider:      "fake",
		ContextWindow: window,
		SupportsTools: true,
	}}
}

func (p *scriptProvider) SupportsTools() bool {
	return true
}

func (p *scriptProvider) SupportsImages() bool {
	return false
}

type blockingProvider struct {
	started chan struct{}
}

func (p *blockingProvider) Name() string {
	return "fake"
}

func (p *blockingProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	_ = req
	ch := make(chan llm.Event)
	close(p.started)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (p *blockingProvider) Models() []llm.Model {
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: 8192}}
}

func (p *blockingProvider) SupportsTools() bool {
	return true
}

func (p *blockingProvider) SupportsImages() bool {
	return false
}

// stopTurnTool is a fake tool that always returns StopTurn=true, signalling
// the agent loop to end the turn after recording this tool's result.
type stopTurnTool struct {
	name   string
	result string
	mu     sync.Mutex
	calls  []string
}

func (t *stopTurnTool) Name() string { return t.name }

func (t *stopTurnTool) Description() string { return "stop-turn test tool " + t.name }

func (t *stopTurnTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *stopTurnTool) Run(_ context.Context, args json.RawMessage) (tools.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, string(args))
	return tools.Result{Content: t.result, StopTurn: true}, nil
}

// TestStopTurnEndsAfterToolResult asserts that when a tool returns
// StopTurn=true, the agent loop ends the turn cleanly (EventTurnFinished, not
// an error) after the tool's result is recorded, without calling the provider
// for another step.
func TestStopTurnEndsAfterToolResult(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	stopper := &stopTurnTool{name: "stop", result: "STOP-RESULT-7a9c"}
	normal := &recordingTool{name: "normal", result: "normal output"}
	registry.Register(stopper)
	registry.Register(normal)

	// The model emits two tool calls in a single batch: stop then normal.
	// The loop must run stop, detect StopTurn=true, record stop's real result,
	// synthesize a placeholder for normal (which did not run), and finish.
	// The guard turn should never be consumed.
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("stop-1", "stop", `{}`),
			toolCall("norm-1", "normal", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
		// Guard: must never be reached.
		{llm.DeltaTextEvent{Text: "should not run"}, llm.EndEvent{}},
	}}

	bus := pubsub.NewTopic[Event]("agent-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          bus,
		SystemPrompt: "test",
	})
	err := loop.Run(ctx, sessionID, userMessage("do the stopper"))
	require.NoError(t, err)

	// Only one provider call ran — the loop did not continue past StopTurn.
	require.Len(t, provider.reqs, 1)

	// The stop tool ran exactly once; the normal tool was never executed.
	require.Len(t, stopper.calls, 1)
	require.Empty(t, normal.calls, "normal tool must not run when stop precedes it")

	// An EventTurnFinished was published (not an error event).
	var sawFinished bool
loop:
	for {
		select {
		case ev := <-events:
			if ev.Kind == EventTurnFinished {
				sawFinished = true
			}
		default:
			break loop
		}
	}
	require.True(t, sawFinished, "EventTurnFinished must be published on StopTurn")

	// Verify history: the stop tool's real result is persisted, and the
	// normal tool has a synthetic error result (not missing) so history is
	// well-formed with no orphaned tool_use blocks.
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)

	var stopResult, normalResult *message.ToolResultBlock
	for _, msg := range messages {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok {
				rb := b
				switch rb.ToolUseID {
				case "stop-1":
					stopResult = &rb
				case "norm-1":
					normalResult = &rb
				}
			}
		}
	}
	require.NotNil(t, stopResult, "stop tool result must be persisted")
	require.False(t, stopResult.IsError, "stop tool result must not be an error")
	require.Equal(t, "STOP-RESULT-7a9c", stopResult.Content)

	require.NotNil(t, normalResult, "normal tool must have a synthesized result (no orphaned tool_use)")
	require.True(t, normalResult.IsError, "synthesized result for unexecuted tool must be marked as error")
}

// testLedgerFailingRecord opens a fresh DB and returns a Ledger configured
// with an empty pricing table so every Record call returns ErrUnknownModel.
// This exercises the ledger-failure path without touching the filesystem
// ledger or mocking unexported types.
func testLedgerFailingRecord(t *testing.T) *ledger.Ledger {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	cfg := &config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5}
	// Passing nil models means the pricing table is empty: Record always
	// returns ErrUnknownModel, which is the failure path we want to exercise.
	return ledger.New(database, cfg, nil, nil)
}

// TestLedgerFailureDoesNotAbortTurn asserts that a billing-record error (e.g.
// unknown model in the pricing table) does not discard the already-successful
// provider response or abort the turn. The turn must finish normally and the
// assistant text must be preserved in the session.
func TestLedgerFailureDoesNotAbortTurn(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", result: "file contents"})

	const assistantText = "LEDGER-TEST-MARKER-3b1c: done reviewing the file."
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("v-1", "view", `{"path":"x.go"}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
		{
			llm.DeltaTextEvent{Text: assistantText},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 6}},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Ledger:       testLedgerFailingRecord(t),
		SystemPrompt: "test",
	})
	// Run must return nil even though the ledger Record fails on every step.
	err := loop.Run(ctx, sessionID, userMessage("review x.go"))
	require.NoError(t, err)

	// Both provider turns ran — the loop continued past the ledger error.
	require.Len(t, provider.reqs, 2)

	// The final persisted assistant message is the second-turn text, proving
	// the completed work was kept and not discarded by the ledger failure.
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	last := messages[len(messages)-1]
	require.Equal(t, message.RoleAssistant, last.Role)
	require.Contains(t, textOf(last), assistantText,
		"assistant text from a successful provider response must survive a ledger write failure")
}

// TestAbortedBatchLeavesNoOrphanedToolUse asserts that when the loop guard
// trips mid-batch (before all pending tool_use calls have run), the session
// history contains a matching tool_result for every tool_use block — including
// the unexecuted ones — so the next turn can be sent to the provider without
// a 400 "tool_use ids found without tool_result" rejection.
func TestAbortedBatchLeavesNoOrphanedToolUse(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// bash always returns the same result so the predictive loop guard fires
	// before the third identical call runs (same call, same output observed
	// twice means the third is predicted to produce the same futile result).
	bash := &recordingTool{name: "bash", result: "same-output"}
	registry.Register(bash)

	// Three identical calls in one assistant batch: the first two execute
	// (building up the detector's identical-pair signal), and the predictive
	// gate fires before b-3 runs.
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("b-1", "bash", `{"command":"echo x"}`),
			toolCall("b-2", "bash", `{"command":"echo x"}`),
			toolCall("b-3", "bash", `{"command":"echo x"}`),
			llm.EndEvent{},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		SystemPrompt: "test",
	})
	err := loop.Run(ctx, sessionID, userMessage("trip the guard"))
	require.NoError(t, err)

	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)

	// Collect all tool_use IDs from assistant messages and all tool_result
	// IDs from user messages. Every tool_use must have exactly one matching
	// tool_result, with no unmatched IDs on either side.
	toolUseIDs := map[string]bool{}
	toolResultIDs := map[string]bool{}
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case message.ToolUseBlock:
				toolUseIDs[b.ID] = true
			case message.ToolResultBlock:
				toolResultIDs[b.ToolUseID] = true
			}
		}
	}

	for id := range toolUseIDs {
		require.True(t, toolResultIDs[id],
			"tool_use %q has no matching tool_result — orphaned block would cause provider 400", id)
	}
	require.Equal(t, len(toolUseIDs), len(toolResultIDs),
		"tool_use count must equal tool_result count: no unmatched results either")
}

// barrierTool blocks inside Run until all N instances in the batch have
// started, then unblocks all of them simultaneously. It implements
// ReadOnlyTool so the loop's parallel fan-out path is used for all-read-only
// batches. If the loop ran tools sequentially, the first tool would block
// forever waiting for the second to arrive at the barrier, causing a timeout.
type barrierTool struct {
	name string
	wg   *sync.WaitGroup // counts down; reaches 0 when all goroutines have started
	gate chan struct{}   // closed when wg reaches 0 — signals all to proceed
}

func (b *barrierTool) Name() string            { return b.name }
func (b *barrierTool) Description() string     { return "barrier tool for concurrency test" }
func (b *barrierTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (b *barrierTool) IsReadOnly() bool        { return true }
func (b *barrierTool) Run(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
	b.wg.Done() // signal this goroutine has arrived
	select {
	case <-b.gate: // wait until all goroutines have arrived
	case <-ctx.Done():
		return tools.Result{Content: "ctx cancelled", IsError: true}, nil
	}
	return tools.Result{Content: b.name + " done"}, nil
}

// makeBarrierTools creates N barrierTools sharing a common gate. The caller
// must register them under distinct names matching the tool calls in the batch.
func makeBarrierTools(names ...string) []*barrierTool {
	var wg sync.WaitGroup
	wg.Add(len(names))
	gate := make(chan struct{})
	go func() { wg.Wait(); close(gate) }()
	out := make([]*barrierTool, len(names))
	for i, name := range names {
		out[i] = &barrierTool{name: name, wg: &wg, gate: gate}
	}
	return out
}

// TestParallelFanOutRunsReadOnlyBatchConcurrently proves that a batch where
// all calls are read-only tools executes them concurrently. The barrierTool
// blocks until both goroutines have started; if the loop were sequential the
// first tool would deadlock waiting for the second to start.
func TestParallelFanOutRunsReadOnlyBatchConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// Two barrier tools that each block until both are active before returning.
	barriers := makeBarrierTools("view", "grep")
	for _, bt := range barriers {
		registry.Register(bt)
	}

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			// Batch: two read-only calls issued in the same turn.
			toolCall("c1", "view", `{}`),
			toolCall("c2", "grep", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-parallel-test", 16),
		SystemPrompt: "test",
	})
	err := loop.Run(ctx, sessionID, userMessage("run parallel tools"))
	require.NoError(t, err, "parallel fan-out must complete without error or deadlock")

	// Verify both tool results are in the session history.
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	results := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			if _, ok := block.(message.ToolResultBlock); ok {
				results++
			}
		}
	}
	require.Equal(t, 2, results, "both tool results must appear in history")
}

// TestParallelFanOutSkippedForMixedBatch verifies that a batch containing one
// read-only and one write-class tool falls through to the sequential path
// (no panic, correct results, history well-formed).
func TestParallelFanOutSkippedForMixedBatch(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	readTool := &readOnlyRecordingTool{recordingTool: recordingTool{name: "view", result: "file content"}}
	writeTool := &recordingTool{name: "edit", result: "edited"}
	registry.Register(readTool)
	registry.Register(writeTool)

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("c1", "view", `{}`),
			toolCall("c2", "edit", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-mixed-test", 16),
		SystemPrompt: "test",
	})
	err := loop.Run(ctx, sessionID, userMessage("mixed batch"))
	require.NoError(t, err)

	// Both tools must have been called.
	require.Equal(t, 1, len(readTool.calls), "view must be called once")
	require.Equal(t, 1, len(writeTool.calls), "edit must be called once")
}

// readOnlyRecordingTool wraps recordingTool and implements ReadOnlyTool so
// it participates in the parallel fan-out when paired with other read-only tools,
// but a mixed batch (with a non-read-only tool) reverts to sequential.
type readOnlyRecordingTool struct {
	recordingTool
}

func (r *readOnlyRecordingTool) IsReadOnly() bool { return true }

// ─── T10: policy-driven post-write verification ──────────────────────────────

// goModWorkDir creates a temp directory containing a go.mod so
// DiscoverVerifyCommands proposes `go test ./...` for it, returning the path.
func goModWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.24\n")
	return dir
}

// TestPolicyVerifyRunsAfterSimpleAppGeneration asserts that with the default
// (strict) verification policy and a discoverable command, a successful write
// triggers verification automatically — no per-file hook required — and the
// passing command is surfaced in the tool result so the final answer can report
// it.
func TestPolicyVerifyRunsAfterSimpleAppGeneration(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote main.go"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("c1", "write", `{"path":"main.go","content":"package main"}`), llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{script: []verifyOutcome{{output: "ok", err: nil}}}
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		VerifyRunner: runner,
		WorkDir:      goModWorkDir(t),
		// No VerifyHooks and no Verification override: the strict default policy
		// must require verification on a source edit.
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write a hello-world app")))

	require.Len(t, runner.calls, 1, "policy verification must run exactly once")
	require.Equal(t, "go test ./...", runner.calls[0].command)

	tr := toolResultFor(t, repo, ctx, sessionID, "c1")
	require.False(t, tr.IsError, "passing verification must not mark the result an error")
	require.Contains(t, tr.Content, "go test ./...", "the verify command must surface in the result")
}

// TestPolicyVerifyFailureFedBackAndTriggersFix asserts that a failed
// verification is injected as an IsError tool result (so the model fixes), then
// after the model re-edits the verification runs again and passes.
func TestPolicyVerifyFailureFedBackAndTriggersFix(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote main.go"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		// First edit — verification will fail.
		{toolCall("c1", "write", `{"path":"main.go","content":"package main\nbroken"}`), llm.EndEvent{}},
		// Model reacts to the failure with a fix — verification will pass.
		{toolCall("c2", "write", `{"path":"main.go","content":"package main"}`), llm.EndEvent{}},
		// Final reply.
		{llm.DeltaTextEvent{Text: "Fixed and verified."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{script: []verifyOutcome{
		{output: "./main.go:2:1: syntax error", err: errors.New("exit status 1")},
		{output: "ok", err: nil},
	}}
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		VerifyRunner: runner,
		WorkDir:      goModWorkDir(t),
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write an app")))

	require.Len(t, runner.calls, 2, "verification must run once per edit")

	// First edit's result must carry the failure so the model saw it.
	failed := toolResultFor(t, repo, ctx, sessionID, "c1")
	require.True(t, failed.IsError, "failed verification must be an error result")
	require.Contains(t, failed.Content, "verification failed")
	require.Contains(t, failed.Content, "syntax error")

	// Second edit's result must be a clean pass.
	fixed := toolResultFor(t, repo, ctx, sessionID, "c2")
	require.False(t, fixed.IsError, "fixed edit must verify cleanly")
}

// TestPolicyVerifyAttemptsAreCapped asserts that a model stuck re-editing a
// change that never verifies does not loop forever: verification stops running
// once MaxVerifyAttempts is reached, and the loop still terminates cleanly.
func TestPolicyVerifyAttemptsAreCapped(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote main.go"})

	// The model keeps editing; every verification fails. Provide more edit turns
	// than the cap so the cap — not the script — is what stops verification.
	scripts := make([][]llm.Event, 0, 6)
	for i := 0; i < 5; i++ {
		// Distinct content each turn so the loop detector does not trip before the
		// verify cap does — we are exercising the cap, not loop detection.
		input := fmt.Sprintf(`{"path":"main.go","content":"still broken %d"}`, i)
		scripts = append(scripts, []llm.Event{
			toolCall(fmt.Sprintf("c%d", i+1), "write", input),
			llm.EndEvent{},
		})
	}
	scripts = append(scripts, []llm.Event{llm.DeltaTextEvent{Text: "Giving up; explaining."}, llm.EndEvent{}})
	provider := &scriptProvider{scripts: scripts}

	// Always-failing verifier; script long enough that the cap, not exhaustion,
	// limits the calls.
	outcomes := make([]verifyOutcome, 10)
	for i := range outcomes {
		outcomes[i] = verifyOutcome{output: "fail", err: errors.New("exit status 1")}
	}
	runner := &captureVerifyRunner{script: outcomes}

	loop := New(Config{
		Name:              "coder",
		Model:             "fake-model",
		Provider:          provider,
		Tools:             registry,
		Sessions:          repo,
		VerifyRunner:      runner,
		WorkDir:           goModWorkDir(t),
		MaxVerifyAttempts: 2,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write an app")))

	require.Len(t, runner.calls, 2, "verification must stop after MaxVerifyAttempts")
}

// TestPolicyVerifyDisabledSkipsVerification asserts that a disabled policy makes
// verification advisory only: no command is run even after a successful write.
func TestPolicyVerifyDisabledSkipsVerification(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote main.go"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("c1", "write", `{"path":"main.go","content":"package main"}`), llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{}
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		VerifyRunner: runner,
		WorkDir:      goModWorkDir(t),
		Verification: config.VerificationConfig{Disabled: true},
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write an app")))

	require.Empty(t, runner.calls, "disabled policy must not run verification")
}

// TestPolicyVerifySkippedWhenNoCommandDiscovered asserts that a workspace with
// no recognizable toolchain (no manifest) yields no verify command, so the
// no_test_command skip path is taken and the original success result is kept.
func TestPolicyVerifySkippedWhenNoCommandDiscovered(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote notes.txt"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("c1", "write", `{"path":"notes.txt","content":"hi"}`), llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{}
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		VerifyRunner: runner,
		WorkDir:      t.TempDir(), // empty: nothing for DiscoverVerifyCommands to find
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write notes")))

	require.Empty(t, runner.calls, "no discoverable command means no verification runs")
	tr := toolResultFor(t, repo, ctx, sessionID, "c1")
	require.False(t, tr.IsError)
	require.Equal(t, "wrote notes.txt", tr.Content, "original success result must be preserved")
}

// TestPolicyVerifyDoesNotRunOnReadOnlyTool asserts a non-write tool never
// triggers policy verification even with a discoverable command.
func TestPolicyVerifyDoesNotRunOnReadOnlyTool(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", result: "file contents"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("c1", "view", `{"path":"main.go"}`), llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Read it."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{}
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		VerifyRunner: runner,
		WorkDir:      goModWorkDir(t),
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("read the file")))

	require.Empty(t, runner.calls, "read-only tools must not trigger policy verification")
}

// TestPerFileHookVerifyTakesPrecedenceOverPolicy asserts that when a
// user-declared per-file verify_command handles the edit, the policy-driven path
// does not also run — verification happens exactly once, via the hook.
func TestPerFileHookVerifyTakesPrecedenceOverPolicy(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{toolCall("c1", "edit", `{"path":"main.go","old_string":"x","new_string":"y"}`), llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	runner := &captureVerifyRunner{script: []verifyOutcome{{output: "", err: nil}}}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		VerifyHooks: &fakeVerifyHookSource{specs: []hooks.VerifySpec{
			{Command: "make check", Timeout: 5 * time.Second},
		}},
		VerifyRunner: runner,
		WorkDir:      goModWorkDir(t),
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit the file")))

	require.Len(t, runner.calls, 1, "verification must run once, via the per-file hook")
	require.Equal(t, "make check", runner.calls[0].command, "the hook command must win over policy discovery")
}

// TestVerifyTriggerForPathClassification asserts the path→trigger mapping that
// gates which edits the policy requires verification for.
func TestVerifyTriggerForPathClassification(t *testing.T) {
	cases := []struct {
		path string
		want config.VerificationTrigger
	}{
		{"main.go", config.VerifyTriggerSourceEdit},
		{"internal/app/server.go", config.VerifyTriggerSourceEdit},
		{"go.mod", config.VerifyTriggerPackageManifest},
		{"package.json", config.VerifyTriggerPackageManifest},
		{"Cargo.toml", config.VerifyTriggerPackageManifest},
		{"Makefile", config.VerifyTriggerTestOrBuildFile},
		{"loop_test.go", config.VerifyTriggerTestOrBuildFile},
		{"app.spec.ts", config.VerifyTriggerTestOrBuildFile},
		{"", config.VerifyTriggerSourceEdit},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, verifyTriggerForPath(tc.path), "path %q", tc.path)
	}
}
