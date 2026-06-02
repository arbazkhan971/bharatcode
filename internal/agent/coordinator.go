package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/skills"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// readOnlyTaskTools is the allow-list for the read-only "task" agent.
// It includes "skill" because loading a skill's instruction body is a
// read-only operation and the task agent should be able to consult the
// same skills as the coder agent.
var readOnlyTaskTools = []string{"diagnostics", "glob", "grep", "ls", "skill", "view", "web_fetch", "web_search"}

// Dependencies bundles shared collaborators for Coordinator-created loops.
type Dependencies struct {
	Tools       *tools.Registry
	Permission  *permission.Checker
	Sessions    *session.Repo
	FileTracker *filetracker.Tracker
	Ledger      *ledger.Ledger
	Hooks       hookFirer
	MCP         *mcp.Client
	Bus         *pubsub.Topic[Event]
	Providers   map[string]llm.Provider
}

// Coordinator manages configured named agents.
type Coordinator struct {
	cfg    *config.Config
	deps   Dependencies
	mu     sync.RWMutex
	agents []agentDef
	ready  bool
	// skillTool lazily loads skill bodies on demand. It is built once in
	// Start over the discovered skill set and folded into every agent's
	// effective tool set, so any agent can call the "skill" tool to load a
	// skill's full SKILL.md body without bloating the base prompt.
	skillTool *skillTool
}

type agentDef struct {
	name         string
	model        string
	providerName string
	provider     llm.Provider
	systemPrompt string
	tools        []string
}

// NewCoordinator builds a Coordinator without performing prompt rendering.
func NewCoordinator(cfg *config.Config, deps Dependencies) (*Coordinator, error) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	c := &Coordinator{cfg: cfg, deps: deps}
	c.agents = append(
		c.agents,
		agentDef{name: "coder"},
		agentDef{name: "task", tools: append([]string(nil), readOnlyTaskTools...)},
	)
	for _, raw := range cfg.Agents {
		if raw.Name == "" || raw.Name == "coder" || raw.Name == "task" {
			continue
		}
		c.agents = append(c.agents, agentDef{
			name:         raw.Name,
			model:        raw.Model,
			systemPrompt: raw.SystemPrompt,
			tools:        append([]string(nil), raw.Tools...),
		})
	}
	return c, nil
}

// Start renders prompts, resolves providers, and folds MCP tools into use.
func (c *Coordinator) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build the skill-loading tool once, before rendering any prompt, so it
	// appears in every agent's effective tool set and rendered tool list. A
	// load failure is non-fatal: the tool is still registered over an empty
	// set so allow-listing "skill" never fails tool validation.
	c.skillTool = c.loadSkillTool()

	for i := range c.agents {
		c.applyConfigAgent(&c.agents[i])
		provider, providerName, model, err := c.resolveProvider(c.agents[i].model)
		if err != nil {
			return err
		}
		c.agents[i].provider = provider
		c.agents[i].providerName = providerName
		c.agents[i].model = model
		registry := c.effectiveRegistry()
		prompt, err := renderPrompt(ctx, c.agents[i].name, c.agents[i].systemPrompt, registry, c.deps.FileTracker)
		if err != nil {
			return fmt.Errorf("starting agent %q: %w", c.agents[i].name, err)
		}
		c.agents[i].systemPrompt = prompt
	}
	c.ready = true
	return nil
}

// Stop releases Coordinator resources.
func (c *Coordinator) Stop(ctx context.Context) error {
	_ = ctx
	return nil
}

// Agent returns a fresh Loop for name.
func (c *Coordinator) Agent(name string) (*Loop, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, def := range c.agents {
		if def.name == name {
			return New(Config{
				Name:          def.name,
				Model:         def.model,
				Provider:      def.provider,
				Tools:         c.effectiveRegistry(),
				Permission:    c.deps.Permission,
				Sessions:      c.deps.Sessions,
				FileTracker:   c.deps.FileTracker,
				Ledger:        c.deps.Ledger,
				Bus:           c.deps.Bus,
				Hooks:         c.deps.Hooks,
				SystemPrompt:  def.systemPrompt,
				ToolAllowList: def.tools,
			}), nil
		}
	}
	return nil, fmt.Errorf("getting agent %q: %w", name, ErrUnknownAgent)
}

// List returns configured agent names in deterministic order.
func (c *Coordinator) List() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.agents))
	for i, def := range c.agents {
		out[i] = def.name
	}
	return out
}

func (c *Coordinator) applyConfigAgent(def *agentDef) {
	for _, raw := range c.cfg.Agents {
		if raw.Name != def.name {
			continue
		}
		if raw.Model != "" {
			def.model = raw.Model
		}
		if raw.SystemPrompt != "" {
			def.systemPrompt = raw.SystemPrompt
		}
		if raw.Tools != nil {
			def.tools = append([]string(nil), raw.Tools...)
		}
	}
}

func (c *Coordinator) resolveProvider(modelID string) (llm.Provider, string, string, error) {
	if len(c.deps.Providers) == 0 {
		return nil, "", "", fmt.Errorf("resolving provider: no providers configured")
	}
	if modelID != "" {
		for name, provider := range c.deps.Providers {
			for _, model := range provider.Models() {
				if model.ID == modelID {
					return provider, name, modelID, nil
				}
			}
		}
		return nil, "", "", fmt.Errorf("resolving model %q: %w", modelID, llm.ErrModelNotFound)
	}
	names := make([]string, 0, len(c.deps.Providers))
	for name := range c.deps.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	provider := c.deps.Providers[names[0]]
	models := provider.Models()
	model := ""
	if len(models) > 0 {
		model = models[0].ID
	}
	return provider, names[0], model, nil
}

func (c *Coordinator) effectiveRegistry() toolSource {
	return combinedTools{
		base:  c.deps.Tools,
		mcp:   c.mcpTools(),
		extra: c.extraTools(),
	}
}

// loadSkillTool discovers the available skills and returns a skill tool
// over them. A load failure is logged and falls back to an empty set so
// the tool is always present and callable.
func (c *Coordinator) loadSkillTool() *skillTool {
	workdir, err := os.Getwd()
	if err != nil {
		slog.Warn("Loading skills for skill tool: getting working directory", "error", err)
		return newSkillTool(nil)
	}
	set, err := skills.LoadSkills(skillSearchDirs(workdir)...)
	if err != nil {
		slog.Warn("Loading skills for skill tool", "error", err)
		return newSkillTool(nil)
	}
	return newSkillTool(set)
}

// extraTools returns the agent-package tools folded into every agent's
// effective tool set, independent of the shared tools registry.
func (c *Coordinator) extraTools() []tools.Tool {
	if c.skillTool == nil {
		return nil
	}
	return []tools.Tool{c.skillTool}
}

func (c *Coordinator) mcpTools() []tools.Tool {
	if c.deps.MCP == nil {
		return nil
	}
	return c.deps.MCP.Tools()
}

type toolSource interface {
	Get(name string) (tools.Tool, bool)
	List() []tools.Tool
}

type combinedTools struct {
	base  *tools.Registry
	mcp   []tools.Tool
	extra []tools.Tool
}

func (r combinedTools) Get(name string) (tools.Tool, bool) {
	if r.base != nil {
		if t, ok := r.base.Get(name); ok {
			return t, true
		}
	}
	for _, t := range r.mcp {
		if t.Name() == name {
			return t, true
		}
	}
	for _, t := range r.extra {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

func (r combinedTools) List() []tools.Tool {
	var out []tools.Tool
	if r.base != nil {
		out = append(out, r.base.List()...)
	}
	out = append(out, r.mcp...)
	out = append(out, r.extra...)
	sort.Slice(out, func(i, j int) bool {
		return strings.Compare(out[i].Name(), out[j].Name()) < 0
	})
	return out
}
