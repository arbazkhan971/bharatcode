// Package tools defines the common interface and registry for agent-callable
// tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

// Tool is one callable capability exposed to the agent.
type Tool interface {
	// Name returns the canonical tool name used in tool-call requests.
	Name() string

	// Description returns the markdown description shown to the model.
	Description() string

	// Schema returns the JSON Schema for the tool's arguments.
	Schema() json.RawMessage

	// Run executes the tool with JSON-encoded arguments.
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// Result is the value returned to the agent loop after a tool runs.
type Result struct {
	Content  string         `json:"content"`
	IsError  bool           `json:"is_error,omitempty"`
	StopTurn bool           `json:"stop_turn,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	// VerifyNeeded, when true, signals to the agent loop that this write-class
	// tool result should trigger any configured verify_command. It is set by
	// edit, multiedit, and write when they succeed, and is always false for
	// tools that do not mutate files. The loop ignores it when no verify_command
	// is configured, so the field is opt-in at the config level.
	VerifyNeeded bool `json:"verify_needed,omitempty"`
}

// Metadata keys a tool may set on Result to attach an inline image. The agent
// loop forwards such an image to vision-capable models as a real image block so
// the model sees the pixels, not just the text placeholder in Result.Content.
const (
	// MetadataImage holds standard-base64-encoded image bytes.
	MetadataImage = "image"
	// MetadataMimeType holds the image MIME type, e.g. "image/png".
	MetadataMimeType = "mime_type"
)

// Dependencies groups collaborators injected into built-in tools.
type Dependencies struct {
	Config      *config.Config
	Permission  *permission.Checker
	Shell       *shell.Shell
	LSP         *lsp.Manager
	FileTracker *filetracker.Tracker
	Bus         *pubsub.Topic[pubsub.ToolCallPayload]
	TodoBus     *pubsub.Topic[TodoEvent]
	WorkDir     string
	SessionID   string
}

// Registry stores the available tools by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns a registry with currently implemented built-in tools.
func NewRegistry(deps Dependencies) *Registry {
	if deps.WorkDir == "" {
		deps.WorkDir = "."
	}
	abs, err := filepath.Abs(deps.WorkDir)
	if err == nil {
		deps.WorkDir = abs
	}

	r := &Registry{tools: make(map[string]Tool)}
	r.Register(newBashTool(deps))
	r.Register(newViewTool(deps))
	r.Register(newEditTool(deps))
	r.Register(newMultiEditTool(deps))
	r.Register(newWriteTool(deps))
	r.Register(newGrepTool(deps))
	r.Register(newGlobTool(deps))
	r.Register(newLSTool(deps))
	r.Register(newTodoTool(deps))
	r.Register(newDiagnosticsTool(deps))
	r.Register(newSymbolsTool(deps))
	r.Register(newNavigateTool(deps))
	r.Register(newCodeActionsTool(deps))
	r.Register(newFormatTool(deps))
	r.Register(newWebFetchTool(deps))
	r.Register(newWebSearchTool(deps))
	r.Register(newJobOutputTool(deps))
	r.Register(newJobKillTool(deps))
	return r
}

// Register adds t to the registry. Duplicate names are programming errors and
// panic.
func (r *Registry) Register(t Tool) {
	if t == nil {
		panic("tools: cannot register nil tool")
	}
	name := t.Name()
	if name == "" {
		panic("tools: cannot register unnamed tool")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		panic("tools: duplicate tool " + name)
	}
	r.tools[name] = safeTool{inner: t}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns tools sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

type safeTool struct {
	inner Tool
}

func (t safeTool) Name() string {
	return t.inner.Name()
}

func (t safeTool) Description() string {
	return t.inner.Description()
}

func (t safeTool) Schema() json.RawMessage {
	return t.inner.Schema()
}

func (t safeTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(
				ctx, "Tool panic recovered",
				"tool", t.Name(),
				"panic", r,
				"stack", string(debug.Stack()),
			)
			res = Result{Content: fmt.Sprintf("internal tool panic: %v", r), IsError: true}
			err = nil
		}
	}()
	return t.inner.Run(ctx, args)
}

func decodeArgs[T any](args json.RawMessage) (T, *Result) {
	var out T
	if len(strings.TrimSpace(string(args))) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, &out); err != nil {
		res := Result{Content: "invalid tool arguments: " + err.Error(), IsError: true}
		return out, &res
	}
	return out, nil
}

func errorResult(message string) Result {
	return Result{Content: message, IsError: true}
}

func copySchema(schema json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), schema...)
}

func recoverTool(ctx context.Context, name string, res *Result, err *error) {
	if r := recover(); r != nil {
		slog.ErrorContext(
			ctx, "Tool panic recovered",
			"tool", name,
			"panic", r,
			"stack", string(debug.Stack()),
		)
		*res = Result{Content: fmt.Sprintf("internal tool panic: %v", r), IsError: true}
		*err = nil
	}
}
