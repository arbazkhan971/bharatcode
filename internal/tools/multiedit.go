package tools

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// MultiEditTool applies ordered text replacements with one atomic rewrite.
type MultiEditTool struct {
	deps Dependencies
}

//go:embed multiedit.md
var multiEditDescription string

var multiEditSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "edits"],
  "properties": {
    "path": {"type": "string", "minLength": 1},
    "edits": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["old", "new"],
        "properties": {
          "old": {"type": "string"},
          "new": {"type": "string"},
          "replace_all": {"type": "boolean"}
        }
      }
    }
  }
}`)

// newMultiEditTool constructs the ordered batch edit tool.
func newMultiEditTool(deps Dependencies) *MultiEditTool {
	return &MultiEditTool{deps: deps}
}

// Name returns the tool name.
func (t *MultiEditTool) Name() string { return "multiedit" }

// Description returns the model-facing tool description.
func (t *MultiEditTool) Description() string { return multiEditDescription }

// Schema returns the JSON argument schema.
func (t *MultiEditTool) Schema() json.RawMessage { return multiEditSchema }

// Run executes the multiedit tool.
func (t *MultiEditTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Path  string `json:"path"`
		Edits []struct {
			Old        string `json:"old"`
			New        string `json:"new"`
			ReplaceAll bool   `json:"replace_all,omitempty"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}
	if len(in.Edits) == 0 {
		return errorResult("edits must contain at least one edit"), nil
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
	next := string(oldContent)
	replacements := 0
	for i, edit := range in.Edits {
		if edit.Old == "" {
			return errorResult(fmt.Sprintf("edits[%d].old is required", i)), nil
		}
		count := strings.Count(next, edit.Old)
		if count == 0 {
			return errorResult(fmt.Sprintf(
				"edits[%d].old was not found in %s. old must match exactly, including all whitespace and newlines.",
				i, path,
			)), nil
		}
		if count > 1 && !edit.ReplaceAll {
			return errorResult(fmt.Sprintf(
				"Found %d occurrences of edits[%d].old in %s. Each old must be unique — provide more surrounding context to make it unique (or set replace_all to true to replace every match).",
				count, i, path,
			)), nil
		}
		n := 1
		if edit.ReplaceAll {
			n = -1
			replacements += count
		} else {
			replacements++
		}
		next = strings.Replace(next, edit.Old, edit.New, n)
	}
	newContent := []byte(next)
	if string(oldContent) == next {
		return errorResult("edits produced no changes"), nil
	}

	beforeHash := sha256.Sum256(oldContent)
	if err := fsext.AtomicWrite(path, newContent, 0o644); err != nil {
		return Result{}, fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := recordToolWrite(ctx, t.deps, path, oldContent, newContent); err != nil {
		return Result{}, err
	}
	afterHash := sha256.Sum256(newContent)
	return Result{
		Content: fmt.Sprintf("edited %s (%d replacement(s))", path, replacements),
		Metadata: map[string]any{
			"path":         path,
			"replacements": replacements,
			"before_hash":  hex.EncodeToString(beforeHash[:]),
			"after_hash":   hex.EncodeToString(afterHash[:]),
		},
	}, nil
}

func recordToolWrite(ctx context.Context, deps Dependencies, path string, oldContent, newContent []byte) error {
	if deps.FileTracker == nil || deps.SessionID == "" {
		return nil
	}
	_, err := deps.FileTracker.RecordWrite(ctx, deps.SessionID, path, oldContent, newContent)
	if err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(deps.SessionID, path)
	return nil
}

func (t *MultiEditTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
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
