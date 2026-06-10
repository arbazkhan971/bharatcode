// Package extension defines BharatCode's in-process extension API: a stable
// surface that built-in and third-party extensions use to add agent-callable
// tools, user-invokable commands, and lifecycle-event handlers to a running
// agent without editing core packages.
//
// An extension is a unit of code that, given an API, registers what it
// contributes. Compiled extensions implement Extension and register themselves
// with Register at init time; the host then calls Setup on each. Directory
// extensions live under the user and project extension directories and declare
// their commands in an extension.json manifest (see loader.go) — they cannot
// supply Go code, so they contribute commands only, not tools or handlers.
//
// The Host is the concrete registry that backs the API. It collects everything
// extensions register and fans lifecycle events out to their handlers via
// Dispatch, which the agent loop calls at the points named by the Event
// constants below.
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// Event names an agent lifecycle point an extension can hook via On. The agent
// loop calls Dispatch at each of these points so handlers can observe — and, for
// the events that support it, veto — what the agent is about to do.
type Event string

const (
	// BeforeToolCall fires immediately before the agent executes a tool. A
	// handler that returns HookResult{Block: true} vetoes the call: the tool does
	// not run and the model receives an error result naming the reason.
	BeforeToolCall Event = "before_tool_call"
	// BeforeProviderRequest fires immediately before the agent streams a request
	// to the model provider. A handler that returns HookResult{Block: true} aborts
	// the request, failing the turn with the reason (useful for a circuit breaker
	// or a policy gate); otherwise it is an observation point.
	BeforeProviderRequest Event = "before_provider_request"
	// SessionStart fires once, when a session's first turn begins. It is an
	// observation point: a returned Block is ignored.
	SessionStart Event = "session_start"
	// BeforeCompact fires immediately before the agent condenses a conversation.
	// It is an observation point: a returned Block is ignored.
	BeforeCompact Event = "before_compact"
)

// Command is a user-invokable command an extension contributes. Name is the
// invocation token (without a leading slash); Description is shown in listings;
// Prompt is the template text seeded into an agent turn when the command runs.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	// Source names the extension that contributed the command, for diagnostics
	// and listing. It is filled in by the host/loader, not by the extension.
	Source string `json:"-"`
}

// HookPayload carries the per-event context handed to a Handler. Only the fields
// relevant to the firing Event are populated; the rest are zero.
type HookPayload struct {
	// Event is the lifecycle point that fired. It lets one handler registered for
	// several events discriminate.
	Event Event
	// SessionID identifies the session the event belongs to, when known.
	SessionID string
	// AgentName is the name of the agent whose loop fired the event.
	AgentName string

	// ToolName and ToolInput are set for BeforeToolCall: the tool about to run and
	// its (already repaired) JSON arguments.
	ToolName  string
	ToolInput json.RawMessage

	// Model and Provider are set for BeforeProviderRequest: the model id and
	// provider name the turn will use. MessageCount is the size of the history
	// about to be sent.
	Model        string
	Provider     string
	MessageCount int

	// Reason is set for BeforeCompact: a short word describing why compaction is
	// running (for example "manual", "auto", or "fit").
	Reason string
}

// HookResult is what a Handler returns. For events that support veto
// (BeforeToolCall, BeforeProviderRequest) a Block of true stops the pending
// action and Reason explains why. For observation-only events Block is ignored.
type HookResult struct {
	Block  bool
	Reason string
}

// Handler reacts to a lifecycle Event. A returned error is logged and treated as
// pass-through (it never blocks the action) so a buggy handler cannot wedge the
// agent; use HookResult{Block: true} to deliberately veto.
type Handler func(ctx context.Context, p HookPayload) (HookResult, error)

// ExecEnv is the read-only view of the agent's execution environment that an
// extension may consult — where the agent is running and the process
// environment it runs under.
type ExecEnv interface {
	// WorkDir returns the resolved project working directory.
	WorkDir() string
	// Getenv returns the value of the named environment variable, or "".
	Getenv(key string) string
	// Environ returns a copy of the process environment as KEY=value strings.
	Environ() []string
}

// API is the surface handed to each extension's Setup. It is intentionally
// small: register tools and commands, subscribe to lifecycle events, and read
// the execution environment.
type API interface {
	// RegisterTool adds an agent-callable tool. It errors on a duplicate name so a
	// programming mistake fails loudly rather than silently shadowing a tool.
	RegisterTool(t tools.Tool) error
	// RegisterCommand adds a user-invokable command. It errors on a duplicate name.
	RegisterCommand(c Command) error
	// On subscribes handler to event. Handlers fire in registration order.
	On(event Event, handler Handler)
	// GetCommands returns every registered command in deterministic order.
	GetCommands() []Command
	// Env returns the execution-environment accessor.
	Env() ExecEnv
}

// Extension is a compiled unit that contributes to the agent. Register makes an
// Extension known; the host calls Setup once during Load, passing the API the
// extension uses to register its tools, commands, and handlers.
type Extension interface {
	// Name is the extension's stable identifier, used in diagnostics.
	Name() string
	// Setup registers the extension's contributions against api. A returned error
	// is logged and the extension is skipped; it never aborts host construction.
	Setup(api API) error
}

// Host is the concrete API implementation and lifecycle-event dispatcher. It is
// safe for concurrent use: registration happens at Load time and Dispatch is
// called from the agent loop, possibly while a UI thread reads the command list.
type Host struct {
	env ExecEnv

	mu        sync.RWMutex
	toolByID  map[string]tools.Tool
	toolOrder []string
	cmdByName map[string]Command
	cmdOrder  []string
	handlers  map[Event][]Handler
	loaded    []string
}

// NewHost returns an empty Host bound to env. A nil env is tolerated; the
// Env accessor then reports empty values.
func NewHost(env ExecEnv) *Host {
	if env == nil {
		env = emptyEnv{}
	}
	return &Host{
		env:       env,
		toolByID:  make(map[string]tools.Tool),
		cmdByName: make(map[string]Command),
		handlers:  make(map[Event][]Handler),
	}
}

// RegisterTool implements API. It rejects a nil tool, an empty name, or a name
// already registered.
func (h *Host) RegisterTool(t tools.Tool) error {
	if t == nil {
		return fmt.Errorf("registering extension tool: tool is nil")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("registering extension tool: name is empty")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.toolByID[name]; ok {
		return fmt.Errorf("registering extension tool %q: already registered", name)
	}
	h.toolByID[name] = t
	h.toolOrder = append(h.toolOrder, name)
	return nil
}

// RegisterCommand implements API with strict (error-on-duplicate) semantics, used
// by compiled extensions. The directory loader uses putCommand with override
// semantics so a project command can shadow a user one.
func (h *Host) RegisterCommand(c Command) error {
	if c.Name == "" {
		return fmt.Errorf("registering extension command: name is empty")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.cmdByName[c.Name]; ok {
		return fmt.Errorf("registering extension command %q: already registered", c.Name)
	}
	h.putCommandLocked(c)
	return nil
}

// putCommand inserts or overrides a command. The directory loader uses it so a
// project-local command shadows a same-named user command (project wins).
func (h *Host) putCommand(c Command) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.putCommandLocked(c)
}

func (h *Host) putCommandLocked(c Command) {
	if _, ok := h.cmdByName[c.Name]; !ok {
		h.cmdOrder = append(h.cmdOrder, c.Name)
	}
	h.cmdByName[c.Name] = c
}

// On implements API.
func (h *Host) On(event Event, handler Handler) {
	if handler == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[event] = append(h.handlers[event], handler)
}

// GetCommands implements API, returning commands in registration order.
func (h *Host) GetCommands() []Command {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Command, 0, len(h.cmdOrder))
	for _, name := range h.cmdOrder {
		out = append(out, h.cmdByName[name])
	}
	return out
}

// Env implements API.
func (h *Host) Env() ExecEnv {
	return h.env
}

// Tools returns every registered extension tool in registration order so the
// agent wiring can fold them into the effective tool set.
func (h *Host) Tools() []tools.Tool {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]tools.Tool, 0, len(h.toolOrder))
	for _, name := range h.toolOrder {
		out = append(out, h.toolByID[name])
	}
	return out
}

// Names returns the identifiers of every extension that was loaded into this
// host, in load order. It is primarily a diagnostic for the doctor/about paths.
func (h *Host) Names() []string {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]string(nil), h.loaded...)
}

func (h *Host) addName(name string) {
	if name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loaded = append(h.loaded, name)
}

// Dispatch fans event out to every handler registered for it and aggregates
// their results. Handlers run in registration order. The first handler that
// returns Block wins and short-circuits the rest, so a veto is decisive. A
// handler error is logged and treated as pass-through, never blocking. A nil
// host (no extensions loaded) is a no-op that passes through.
//
// Whether a Block is honored is the caller's decision per event: the agent loop
// honors it for BeforeToolCall and BeforeProviderRequest and ignores it for the
// observation-only events.
func (h *Host) Dispatch(ctx context.Context, event Event, payload HookPayload) (HookResult, error) {
	if h == nil {
		return HookResult{}, nil
	}
	h.mu.RLock()
	handlers := h.handlers[event]
	h.mu.RUnlock()
	if len(handlers) == 0 {
		return HookResult{}, nil
	}

	payload.Event = event
	for _, handler := range handlers {
		res, err := handler(ctx, payload)
		if err != nil {
			slog.Warn("Extension handler failed", "event", event, "error", err)
			continue
		}
		if res.Block {
			return res, nil
		}
	}
	return HookResult{}, nil
}

// emptyEnv is the ExecEnv used when a Host is constructed without one.
type emptyEnv struct{}

func (emptyEnv) WorkDir() string      { return "" }
func (emptyEnv) Getenv(string) string { return "" }
func (emptyEnv) Environ() []string    { return nil }

// builtinRegistry holds compiled extensions registered via Register. It is a
// package-level registry, mirroring how the llm provider factories register at
// init time, so a built-in extension is wired in simply by importing its package.
var builtinRegistry struct {
	mu  sync.Mutex
	all []Extension
}

// Register makes a compiled Extension known to the host. Call it from an
// extension package's init function. A nil extension or one with an empty name
// is ignored. Registration only records the extension; Setup runs later, during
// Load, against the constructed host.
func Register(ext Extension) {
	if ext == nil || ext.Name() == "" {
		return
	}
	builtinRegistry.mu.Lock()
	defer builtinRegistry.mu.Unlock()
	builtinRegistry.all = append(builtinRegistry.all, ext)
}

// registeredExtensions returns a copy of the registered compiled extensions.
func registeredExtensions() []Extension {
	builtinRegistry.mu.Lock()
	defer builtinRegistry.mu.Unlock()
	return append([]Extension(nil), builtinRegistry.all...)
}
