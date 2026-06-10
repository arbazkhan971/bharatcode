package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// TestCombinedToolsListDeduplicatesExtensionTools asserts that when an
// extension tool shares a name with a base tool, combinedTools.List() returns
// exactly one entry for that name — the base tool — and the extension tool is
// silently dropped (with a warning). This prevents duplicate tool definitions
// from reaching provider requests, which would cause 400 errors or misrouting.
func TestCombinedToolsListDeduplicatesExtensionTools(t *testing.T) {
	// Use a real tools.Registry so combinedTools.base accepts it.
	// NewRegistry registers built-in tools including "bash", which is the base
	// tool whose name we want to collide with.
	baseReg := tools.NewRegistry(tools.Dependencies{})

	// Look up the base "bash" tool's description to confirm the surviving entry
	// is the base one, not the extension one.
	baseBashTool, ok := baseReg.Get("bash")
	require.True(t, ok, "base registry must contain a 'bash' tool")
	baseBashDesc := baseBashTool.Description()

	// An extension tool also named "bash" — the collision case.
	extBash := &recordingTool{name: "bash", desc: "extension bash (must not appear)"}

	// A second extension tool with a unique name must still appear.
	extUnique := &recordingTool{name: "ext_unique", desc: "unique extension tool"}

	ct := combinedTools{
		base:  baseReg,
		extra: []tools.Tool{extBash, extUnique},
	}

	listed := ct.List()

	// Count how many tools named "bash" appear in the list.
	var bashCount int
	var foundBashDesc string
	for _, tool := range listed {
		if tool.Name() == "bash" {
			bashCount++
			foundBashDesc = tool.Description()
		}
	}
	require.Equal(t, 1, bashCount,
		"combinedTools.List must return exactly one tool named %q; got %d", "bash", bashCount)
	require.Equal(t, baseBashDesc, foundBashDesc,
		"the surviving %q tool must be the base tool, not the extension tool", "bash")

	// The unique extension tool must still be present.
	var hasUnique bool
	for _, tool := range listed {
		if tool.Name() == "ext_unique" {
			hasUnique = true
		}
	}
	require.True(t, hasUnique,
		"combinedTools.List must still include extension tools that do not collide with base tools")
}

// namedProvider is a minimal fake that exposes a fixed model list. It is used
// to prove provider selection rather than stream behaviour.
type namedProvider struct {
	name   string
	models []llm.Model
}

func (p *namedProvider) Name() string { return p.name }
func (p *namedProvider) Stream(_ context.Context, _ llm.Request) (<-chan llm.Event, error) {
	ch := make(chan llm.Event)
	close(ch)
	return ch, nil
}
func (p *namedProvider) Models() []llm.Model  { return p.models }
func (p *namedProvider) SupportsTools() bool  { return false }
func (p *namedProvider) SupportsImages() bool { return false }

// newCoordinatorWithProviders builds a started Coordinator wired with the
// supplied provider map. The "coder" agent defaults to the first model of the
// first provider (alphabetical), which is how the production default config
// starts up when the user has not yet configured a preferred model.
func newCoordinatorWithProviders(t *testing.T, providers map[string]llm.Provider) *Coordinator {
	t.Helper()
	coord, err := NewCoordinator(nil, Dependencies{
		Tools:     tools.NewRegistry(tools.Dependencies{}),
		Sessions:  testRepo(t),
		Providers: providers,
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))
	return coord
}

// TestSetActiveModelRebindsCoderAgent asserts that SetActiveModel updates the
// stored provider and model on the "coder" agentDef, so a subsequent Agent()
// call returns a Loop bound to the new provider.
func TestSetActiveModelRebindsCoderAgent(t *testing.T) {
	deepseek := &namedProvider{
		name:   "deepseek",
		models: []llm.Model{{ID: "deepseek-chat", Provider: "deepseek", ContextWindow: 65536}},
	}
	chatgpt := &namedProvider{
		name:   "chatgpt",
		models: []llm.Model{{ID: "gpt-5.1-codex", Provider: "chatgpt", ContextWindow: 128000}},
	}

	coord := newCoordinatorWithProviders(t, map[string]llm.Provider{
		"chatgpt":  chatgpt,
		"deepseek": deepseek,
	})

	// Before activation the coder agent resolves to one of the two providers
	// (whichever the alphabetical fallback picked; irrelevant for this assertion).
	// Activate gpt-5.1-codex and assert the returned provider is the chatgpt one.
	provider, err := coord.SetActiveModel("coder", "gpt-5.1-codex")
	require.NoError(t, err)
	require.Equal(t, "chatgpt", provider.Name(),
		"SetActiveModel should return the chatgpt provider for gpt-5.1-codex, not deepseek")

	// The stored agentDef must also reflect the change so a subsequent Agent()
	// call constructs a Loop with the correct provider.
	loop, err := coord.Agent("coder")
	require.NoError(t, err)
	require.Equal(t, "chatgpt", loop.cfg.Provider.Name(),
		"Agent() should return a Loop bound to the chatgpt provider after SetActiveModel")
	require.Equal(t, "gpt-5.1-codex", loop.cfg.Model,
		"Agent() should return a Loop with the correct model id after SetActiveModel")
}

// TestSetActiveModelErrorOnUnknownModel asserts that SetActiveModel returns an
// error wrapping llm.ErrModelNotFound when the requested model id is not
// served by any configured provider.
func TestSetActiveModelErrorOnUnknownModel(t *testing.T) {
	deepseek := &namedProvider{
		name:   "deepseek",
		models: []llm.Model{{ID: "deepseek-chat", Provider: "deepseek", ContextWindow: 65536}},
	}
	coord := newCoordinatorWithProviders(t, map[string]llm.Provider{"deepseek": deepseek})

	_, err := coord.SetActiveModel("coder", "gpt-5.1-codex")
	require.Error(t, err)
	require.True(t, errors.Is(err, llm.ErrModelNotFound),
		"SetActiveModel should wrap ErrModelNotFound for an unrecognised model id")
}

// TestSetActiveModelErrorOnUnknownAgent asserts that SetActiveModel returns an
// error wrapping ErrUnknownAgent when the named agent does not exist.
func TestSetActiveModelErrorOnUnknownAgent(t *testing.T) {
	deepseek := &namedProvider{
		name:   "deepseek",
		models: []llm.Model{{ID: "deepseek-chat", Provider: "deepseek", ContextWindow: 65536}},
	}
	coord := newCoordinatorWithProviders(t, map[string]llm.Provider{"deepseek": deepseek})

	// "deepseek-chat" exists but "ghost" agent does not.
	_, err := coord.SetActiveModel("ghost", "deepseek-chat")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnknownAgent),
		"SetActiveModel should wrap ErrUnknownAgent for an unrecognised agent name")
}

// TestLoopSetModelRebindsProvider asserts that Loop.SetModel swaps the
// provider and model seen by subsequent callProvider invocations. The test
// constructs two providers, starts the loop on the first, calls SetModel to
// switch to the second, then runs a turn and checks which provider was used.
func TestLoopSetModelRebindsProvider(t *testing.T) {
	// provider A — the startup default
	providerA := &scriptProvider{
		scripts: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "from-A"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
		},
	}
	// provider B — the one we switch to
	providerB := &scriptProvider{
		scripts: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "from-B"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
		},
		models: []llm.Model{{ID: "gpt-5.1-codex", Provider: "chatgpt", ContextWindow: 128000, SupportsTools: true}},
	}

	repo := testRepo(t)
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: providerA,
		Tools:    newFakeRegistry(),
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("coord-test", 16),
	})

	// Switch to provider B before the first (and only) Run.
	loop.SetModel("gpt-5.1-codex", providerB)

	require.Equal(t, "gpt-5.1-codex", loop.cfg.Model,
		"cfg.Model must reflect the new model id after SetModel")
	// cfg.Provider must point at providerB (not providerA).
	require.Same(t, providerB, loop.cfg.Provider,
		"cfg.Provider must be replaced with the new provider after SetModel")

	// Run one turn and confirm provider B was called (not A).
	sessionID := testSession(t, repo)
	err := loop.Run(context.Background(), sessionID, userMessage("hello"))
	require.NoError(t, err)

	providerA.mu.Lock()
	calledA := len(providerA.reqs)
	providerA.mu.Unlock()
	require.Equal(t, 0, calledA, "provider A should not have been called after SetModel switch")

	providerB.mu.Lock()
	calledB := len(providerB.reqs)
	providerB.mu.Unlock()
	require.Equal(t, 1, calledB, "provider B should have been called exactly once after SetModel switch")
}

// TestDefaultConfigChatGPTProviderResolvesGPT51Codex asserts that the embedded
// default config correctly maps gpt-5.1-codex to the chatgpt provider — not to
// deepseek or any other provider — so the registration layer is sound and the
// routing bug would only manifest if the TUI activation path skips rebinding.
func TestDefaultConfigChatGPTProviderResolvesGPT51Codex(t *testing.T) {
	cfg := config.Default()

	// Wire up the Coordinator over the default config's provider/model catalog.
	// We do not need real API keys or network access: resolveProvider only
	// matches model IDs against provider.Models() lists, which come from the
	// config; no HTTP round-trips are made.
	providers := make(map[string]llm.Provider)
	for _, p := range cfg.Providers {
		if p.Disabled {
			continue
		}
		// Use namedProvider stubs to avoid constructing real HTTP clients.
		var models []llm.Model
		for _, id := range p.Models {
			models = append(models, llm.Model{ID: id, Provider: p.Name})
		}
		providers[p.Name] = &namedProvider{name: p.Name, models: models}
	}

	coord, err := NewCoordinator(cfg, Dependencies{
		Tools:     tools.NewRegistry(tools.Dependencies{}),
		Sessions:  testRepo(t),
		Providers: providers,
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))

	provider, err := coord.SetActiveModel("coder", "gpt-5.1-codex")
	require.NoError(t, err, "gpt-5.1-codex must resolve to a provider in the default config")
	require.Equal(t, "chatgpt", provider.Name(),
		"gpt-5.1-codex must route to the chatgpt provider, not deepseek or any other provider")
}
