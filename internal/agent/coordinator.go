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
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/message"
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
var readOnlyTaskTools = []string{"diagnostics", "glob", "grep", "ls", "mcp_prompts", "mcp_resources", "navigate", "skill", "symbols", "view", "web_fetch", "web_search"}

// readOnlySet is the membership form of readOnlyTaskTools, used by the Loop's
// plan-mode restriction to decide whether a tool is read-only. Both share one
// source of truth so the read-only definition cannot drift between the task
// agent's allow-list and plan mode.
var readOnlySet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(readOnlyTaskTools))
	for _, name := range readOnlyTaskTools {
		m[name] = struct{}{}
	}
	return m
}()

// Dependencies bundles shared collaborators for Coordinator-created loops.
type Dependencies struct {
	Tools       *tools.Registry
	Permission  *permission.Checker
	Sessions    *session.Repo
	FileTracker *filetracker.Tracker
	Ledger      *ledger.Ledger
	Hooks       *hooks.Engine
	MCP         *mcp.Client
	Bus         *pubsub.Topic[Event]
	Providers   map[string]llm.Provider
	// Router, when set, is forwarded to every Loop the Coordinator creates,
	// enabling cost-aware model routing. It is nil by default, leaving routing
	// off and each Loop pinned to its configured model — the non-breaking
	// default. Inject a Router (for example, a CostAwareRouter) to opt in.
	Router Router
	// ToolAuditor, when set, is forwarded to every Loop the Coordinator creates
	// so each agent's tool invocations are recorded in the append-only audit log.
	// It is nil by default, leaving tool auditing off.
	ToolAuditor ToolAuditor
	// LLMAuditor, when set, is forwarded to every Loop the Coordinator creates so
	// each agent's model-provider turns are recorded in the append-only audit
	// log. It is nil by default, leaving LLM auditing off.
	LLMAuditor LLMAuditor
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

	// taskTool dispatches subagents on demand. It is built once in Start over
	// the configured agent set and folded into every agent's effective tool
	// set; the per-agent allow-list then gates who may actually call it (the
	// read-only "task" agent's allow-list excludes it, so dispatched subagents
	// cannot recurse).
	taskTool *taskTool

	// plans holds the most recent plan text for each session, written after a
	// plan-mode turn completes and consumed by ApprovePlan when the user
	// approves. planStore carries its own mutex so it is safe for concurrent
	// access from the TUI thread and any other goroutine that calls StorePlan,
	// PlanFor, or ApprovePlan.
	plans planStore
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

	// Build the subagent-dispatch tool over the configured agent set so the
	// model can fan out work to subagents. It is built after the agent set is
	// known but before prompts render, so it appears in every agent's tool
	// listing; the per-agent allow-list decides who may call it.
	c.taskTool = c.loadTaskTool()

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

// planStore holds the most recent extracted plan for each session, keyed by
// session ID. Plans are written by StorePlan (after a plan-mode turn ends and
// the caller has extracted the plan text) and cleared by ApprovePlan (once the
// plan is handed to the execution turn). The zero value is ready to use.
type planStore struct {
	mu    sync.Mutex
	plans map[string]string
}

func (ps *planStore) set(sessionID, planText string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.plans == nil {
		ps.plans = make(map[string]string)
	}
	ps.plans[sessionID] = planText
}

func (ps *planStore) get(sessionID string) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.plans[sessionID]
}

func (ps *planStore) take(sessionID string) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	plan := ps.plans[sessionID]
	delete(ps.plans, sessionID)
	return plan
}

// ExtractPlanText extracts the textual plan the model produced during a
// plan-mode turn. It concatenates every TextBlock in msg's content in order,
// trimming leading and trailing whitespace from the result. A message with no
// text blocks (e.g. one that contains only tool-use blocks) returns an empty
// string. The function is intentionally pure — it only inspects msg and has
// no side effects — so it can be called freely from the TUI or any other layer
// that receives the plan-turn EventLLMResponse message.
func ExtractPlanText(msg message.Message) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if tb, ok := block.(message.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return strings.TrimSpace(sb.String())
}

// StorePlan records planText as the pending plan for sessionID. It is called
// by the TUI (or any layer driving the Loop) after the plan-mode turn ends and
// the plan text has been extracted via ExtractPlanText. Storing the plan makes
// it retrievable via PlanFor and consumable by ApprovePlan, so the approval
// path can carry the plan into the execution turn without the caller needing
// to track it separately.
func (c *Coordinator) StorePlan(sessionID, planText string) {
	c.plans.set(sessionID, planText)
}

// PlanFor returns the most recent stored plan for sessionID without consuming
// it. It returns an empty string when no plan has been stored for the session.
// The TUI uses this to render the plan in the approval dialog before the user
// decides to approve or reject.
func (c *Coordinator) PlanFor(sessionID string) string {
	return c.plans.get(sessionID)
}

// ApprovePlan transitions loop out of plan mode and returns the stored plan
// for sessionID so the caller can seed the next execution turn with it. It
// calls loop.Approve() (which clears the read-only restriction and removes the
// plan-mode prompt) and then atomically consumes and returns the stored plan,
// ensuring the plan is passed through exactly once and not silently discarded.
//
// The caller is expected to start a new Run with a synthetic user message that
// includes the returned plan text, for example:
//
//	planText := c.ApprovePlan(sessionID, loop)
//	if planText != "" {
//	    loop.Run(ctx, sessionID, SeedMessageFromPlan(sessionID, planText))
//	}
//
// This pattern is what loop.go's integrator must wire: see integrationNotes in
// the lane return value for the exact synthetic message format and the Run call
// site that should inject it.
func (c *Coordinator) ApprovePlan(sessionID string, loop *Loop) string {
	loop.Approve()
	return c.plans.take(sessionID)
}

// SeedMessageFromPlan builds the synthetic user message that carries the
// approved plan into the first execution turn. The message instructs the agent
// to execute the plan exactly as written, without re-deriving intent from
// scratch. Callers pass this message as the userMsg argument to loop.Run so
// the execution turn is seeded with the plan rather than the original
// free-form prompt.
//
// When planText is empty SeedMessageFromPlan returns a plain "go ahead"
// message so the turn still runs even if no plan text was captured. Callers
// that have already confirmed a non-empty plan (e.g. via PlanFor before
// ApprovePlan) can skip that branch.
func SeedMessageFromPlan(sessionID, planText string) message.Message {
	body := "Execute the approved plan:\n\n" + planText
	if strings.TrimSpace(planText) == "" {
		body = "Plan approved. Go ahead and execute it."
	}
	return message.Message{
		SessionID: sessionID,
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: body}},
	}
}

// Agent returns a fresh Loop for name.
func (c *Coordinator) Agent(name string) (*Loop, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, def := range c.agents {
		if def.name == name {
			return New(Config{
				Name:                 def.name,
				Model:                def.model,
				Provider:             def.provider,
				Tools:                c.effectiveRegistry(),
				Permission:           c.deps.Permission,
				Sessions:             c.deps.Sessions,
				FileTracker:          c.deps.FileTracker,
				Ledger:               c.deps.Ledger,
				Bus:                  c.deps.Bus,
				Hooks:                c.deps.Hooks,
				VerifyHooks:          c.deps.Hooks,
				SystemPrompt:         def.systemPrompt,
				ToolAllowList:        def.tools,
				Router:               c.deps.Router,
				ToolAuditor:          c.deps.ToolAuditor,
				LLMAuditor:           c.deps.LLMAuditor,
				AutoCompactThreshold: c.cfg.Options.AutoCompactThreshold,
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

// loadTaskTool builds the subagent-dispatch tool over the configured agent
// names, binding it to the Coordinator's DispatchParallel. Callers hold the
// Coordinator write lock (Start), so it reads c.agents directly.
func (c *Coordinator) loadTaskTool() *taskTool {
	names := make([]string, 0, len(c.agents))
	for _, def := range c.agents {
		names = append(names, def.name)
	}
	return newTaskTool(c.DispatchParallel, names)
}

// extraTools returns the agent-package tools folded into every agent's
// effective tool set, independent of the shared tools registry.
func (c *Coordinator) extraTools() []tools.Tool {
	var out []tools.Tool
	if c.skillTool != nil {
		out = append(out, c.skillTool)
	}
	if c.taskTool != nil {
		out = append(out, c.taskTool)
	}
	// The MCP resources and prompts tools are always registered, even with no
	// MCP client, so allow-listing them (e.g. for the read-only task agent and
	// plan mode) never fails tool validation — mirroring the skill tool. With no
	// client they report unavailability rather than panicking.
	out = append(out, mcp.ResourcesToolFor(c.deps.MCP))
	out = append(out, mcp.PromptsToolFor(c.deps.MCP))
	return out
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
