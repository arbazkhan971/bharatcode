package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
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

	out := injectInstructions(base, instr)

	// Base prompt is preserved.
	require.Contains(t, out, base)
	// Injected instructions are present, under the delimited header.
	require.Contains(t, out, projectInstructionsHeader)
	require.Contains(t, out, instr)
	// The injected section comes after the base prompt.
	require.Less(t, strings.Index(out, base), strings.Index(out, instr))
	// The header introduces the instructions (delimited section).
	require.Less(t, strings.Index(out, projectInstructionsHeader), strings.Index(out, instr))
}

func TestInjectInstructionsEmptyLeavesBaseUnchanged(t *testing.T) {
	base := "You are BharatCode's primary coding agent."

	require.Equal(t, base, injectInstructions(base, ""))
	require.Equal(t, base, injectInstructions(base, "   \n\t  "))
	// No delimiter header leaks in when there is nothing to inject.
	require.NotContains(t, injectInstructions(base, ""), projectInstructionsHeader)
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
	name   string
	desc   string
	result string
	mu     sync.Mutex
	calls  []string
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
	return tools.Result{Content: t.result}, nil
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
	return []llm.Model{{
		ID:            "fake-model",
		Provider:      "fake",
		ContextWindow: 8192,
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
