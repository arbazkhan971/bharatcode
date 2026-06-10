package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/diffutil"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// WriteTool creates a new file or overwrites a previously viewed file.
type WriteTool struct {
	deps Dependencies
	diag editDiagnoser
}

//go:embed write.md
var writeDescription string

var writeSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "content"],
  "properties": {
    "path": {"type": "string", "minLength": 1},
    "content": {"type": "string"}
  }
}`)

// newWriteTool constructs the workspace file writer.
func newWriteTool(deps Dependencies) *WriteTool {
	t := &WriteTool{deps: deps}
	if deps.LSP != nil {
		t.diag = deps.LSP
	}
	return t
}

// Name returns the tool name.
func (t *WriteTool) Name() string { return "write" }

// Description returns the model-facing tool description.
func (t *WriteTool) Description() string { return writeDescription }

// Schema returns the JSON argument schema.
func (t *WriteTool) Schema() json.RawMessage { return writeSchema }

// Run executes the write tool.
func (t *WriteTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}

	path, err := resolveToolPath(in.Path, t.deps.WorkDir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if !isInsideWorkDir(path, t.deps.WorkDir) {
		return errorResult("path is outside the workspace: " + path), nil
	}
	if err := t.checkPermission(ctx, path, args); err != nil {
		return errorResult(err.Error()), nil
	}

	var oldContent []byte
	exists := fsext.Exists(path)
	if exists {
		// Read-before-edit via the FileTracker baseline, uniform with
		// edit/multiedit/patch/rename: refuse to overwrite an existing file the
		// session has not read, or one that changed on disk since it was last read.
		// When no FileTracker is wired (as in some tests) this is a no-op and the
		// in-memory view guard below still refuses a blind overwrite.
		if msg := editGuard(ctx, t.deps, path, "overwriting"); msg != "" {
			return errorResult(msg), nil
		}
		if !hasViewed(sessionID(ctx, t.deps), path) {
			return errorResult("refusing to overwrite existing file that has not been viewed in this session"), nil
		}
		oldContent, err = os.ReadFile(path)
		if err != nil {
			return Result{}, fmt.Errorf("reading existing file %s: %w", path, err)
		}
	}

	dir := filepath.Dir(path)
	if err := fsext.EnsureDir(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("ensuring parent directory %s: %w", dir, err)
	}
	newContent := []byte(in.Content)
	if err := fsext.AtomicWrite(path, newContent, 0o644); err != nil {
		return Result{}, fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := recordToolWrite(ctx, t.deps, path, oldContentOrNil(exists, oldContent), newContent); err != nil {
		return Result{}, err
	}
	markViewed(sessionID(ctx, t.deps), path)

	action := "created"
	if exists {
		action = "wrote"
	}
	content := fmt.Sprintf("%s %s (%d bytes)", action, path, len(newContent))
	metadata := map[string]any{
		"path":  path,
		"bytes": len(newContent),
	}
	// Show a diff only when overwriting: a new file's diff would just echo the
	// content the model already provided.
	if exists {
		if d := diffutil.Unified(string(oldContent), in.Content); d != "" {
			content += "\n\n" + d
			metadata["diff"] = d
		}
	}
	if note := postWriteDiagnostics(ctx, t.diag, t.deps.WorkDir, path); note != "" {
		content += "\n\n" + note
		metadata["diagnostics"] = note
	}
	return Result{
		Content:  content,
		Metadata: metadata,
	}, nil
}

func (t *WriteTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	args["path"] = path
	decision, err := t.deps.Permission.Check(ctx, permission.Request{
		ToolName:  t.Name(),
		Args:      args,
		SessionID: sessionID(ctx, t.deps),
	})
	if err != nil {
		return fmt.Errorf("checking permission: %w", err)
	}
	if decision == permission.DecisionDeny {
		return fmt.Errorf("permission denied")
	}
	return nil
}

func oldContentOrNil(exists bool, oldContent []byte) []byte {
	if !exists {
		return nil
	}
	return oldContent
}
