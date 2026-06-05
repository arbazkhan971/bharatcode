package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// EditTool performs one exact text replacement in an existing file.
type EditTool struct {
	deps Dependencies
}

//go:embed edit.md
var editDescription string

var editSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "old_string", "new_string"],
  "properties": {
    "path": {"type": "string", "minLength": 1},
    "old_string": {"type": "string"},
    "new_string": {"type": "string"},
    "replace_all": {"type": "boolean"}
  }
}`)

// newEditTool constructs the exact replacement tool.
func newEditTool(deps Dependencies) *EditTool {
	return &EditTool{deps: deps}
}

// Name returns the tool name.
func (t *EditTool) Name() string { return "edit" }

// Description returns the model-facing tool description.
func (t *EditTool) Description() string { return editDescription }

// Schema returns the JSON argument schema.
func (t *EditTool) Schema() json.RawMessage { return editSchema }

// Run executes the edit tool.
func (t *EditTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}
	if in.OldString == "" {
		return errorResult("old_string is required"), nil
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

	oldContent, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading file %s: %w", path, err)
	}
	text := string(oldContent)
	count := strings.Count(text, in.OldString)
	if count == 0 {
		return errorResult("old_string was not found in " + path + ". old_string must match exactly, including all whitespace and newlines."), nil
	}
	if count > 1 && !in.ReplaceAll {
		return errorResult(fmt.Sprintf(
			"Found %d occurrences of old_string in %s. Each old_string must be unique — provide more surrounding context to make it unique (or set replace_all to true to replace every match).",
			count, path,
		)), nil
	}

	replacements := 1
	if in.ReplaceAll {
		replacements = -1
	}
	newText := strings.Replace(text, in.OldString, in.NewString, replacements)
	if newText == text {
		return errorResult("edit produced no changes"), nil
	}
	if err := fsext.AtomicWrite(path, []byte(newText), 0o644); err != nil {
		return Result{}, fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := t.recordWrite(ctx, path, oldContent, []byte(newText)); err != nil {
		return Result{}, err
	}

	return Result{
		Content: fmt.Sprintf("edited %s (%d replacement(s))", path, countForReport(count, in.ReplaceAll)),
		Metadata: map[string]any{
			"path":         path,
			"replacements": countForReport(count, in.ReplaceAll),
		},
	}, nil
}

func (t *EditTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	args["path"] = path
	decision, err := t.deps.Permission.Check(ctx, permission.Request{
		ToolName:  t.Name(),
		Args:      args,
		SessionID: t.deps.SessionID,
	})
	if err != nil {
		return fmt.Errorf("checking permission: %w", err)
	}
	if decision == permission.DecisionDeny {
		return fmt.Errorf("permission denied")
	}
	return nil
}

func (t *EditTool) recordWrite(ctx context.Context, path string, oldContent, newContent []byte) error {
	if t.deps.FileTracker == nil || t.deps.SessionID == "" {
		return nil
	}
	_, err := t.deps.FileTracker.RecordWrite(ctx, t.deps.SessionID, path, oldContent, newContent)
	if err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(t.deps.SessionID, path)
	return nil
}

func countForReport(count int, replaceAll bool) int {
	if replaceAll {
		return count
	}
	return 1
}
