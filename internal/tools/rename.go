package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// RenameSource is the LSP capability consumed by the rename tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type RenameSource interface {
	Rename(ctx context.Context, path string, line, col int, newName string) (lsp.WorkspaceEdit, error)
}

type renameTool struct {
	deps   Dependencies
	source RenameSource
}

type renameArgs struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	NewName string `json:"new_name"`
}

var schemaRename = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "line", "new_name"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative path to the file containing the symbol to rename."
    },
    "line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based line number of the symbol, as reported by symbols/grep/view."
    },
    "column": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based column of the symbol on that line. Defaults to 1 (start of line)."
    },
    "new_name": {
      "type": "string",
      "description": "The new identifier to rename the symbol to, across every reference the language server finds."
    }
  }
}`)

//go:embed rename.md
var renameDescription string

func newRenameTool(deps Dependencies) Tool {
	t := &renameTool{deps: deps}
	// A nil *lsp.Manager assigned to the RenameSource interface would produce a
	// non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the source
	// when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *renameTool) Name() string {
	return "rename"
}

func (t *renameTool) Description() string {
	return renameDescription
}

func (t *renameTool) Schema() json.RawMessage {
	return schemaRename
}

func (t *renameTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args renameArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid rename arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("rename is unavailable: no LSP manager configured"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult("rename requires a path"), nil
	}
	if args.Line < 1 {
		return errorResult("rename requires a 1-based line (>= 1)"), nil
	}
	if strings.TrimSpace(args.NewName) == "" {
		return errorResult("rename requires a non-empty new_name"), nil
	}
	col := args.Column
	if col < 1 {
		col = 1
	}

	path, err := resolveToolPath(args.Path, t.deps.WorkDir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if !isInsideWorkDir(path, t.deps.WorkDir) {
		return errorResult("path is outside the workspace: " + path), nil
	}
	if err := t.checkPermission(ctx, path, raw); err != nil {
		return errorResult(err.Error()), nil
	}

	// LSP positions are 0-based; the model speaks the 1-based coordinates that
	// symbols/grep/view emit.
	edit, err := t.source.Rename(ctx, path, args.Line-1, col-1, args.NewName)
	if err != nil {
		return Result{}, fmt.Errorf("renaming symbol at %s:%d:%d: %w", args.Path, args.Line, col, err)
	}
	if len(edit.Changes) == 0 {
		return Result{Content: "No rename performed: the language server reported no edits (the symbol may not be renamable)."}, nil
	}

	return t.applyWorkspaceEdit(ctx, edit, args.NewName)
}

// applyWorkspaceEdit applies every file change in edit, writing each file
// atomically and recording the write so later reads see the change. Files are
// processed in sorted path order so the summary is deterministic. Before
// touching anything it validates that every target stays inside the workspace,
// failing the whole rename rather than applying a partial set.
func (t *renameTool) applyWorkspaceEdit(ctx context.Context, edit lsp.WorkspaceEdit, newName string) (Result, error) {
	paths := make([]string, 0, len(edit.Changes))
	for p := range edit.Changes {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if !isInsideWorkDir(p, t.deps.WorkDir) {
			return errorResult("rename would edit a file outside the workspace: " + p), nil
		}
	}

	type pending struct {
		path       string
		oldContent []byte
		newContent []byte
		edits      int
	}
	updates := make([]pending, 0, len(paths))
	totalEdits := 0
	for _, p := range paths {
		edits := edit.Changes[p]
		if len(edits) == 0 {
			continue
		}
		oldContent, err := os.ReadFile(p)
		if err != nil {
			return Result{}, fmt.Errorf("reading file %s: %w", p, err)
		}
		newText, err := applyTextEdits(string(oldContent), edits)
		if err != nil {
			return errorResult(fmt.Sprintf("applying rename edits to %s: %v", p, err)), nil
		}
		if newText == string(oldContent) {
			continue
		}
		updates = append(updates, pending{path: p, oldContent: oldContent, newContent: []byte(newText), edits: len(edits)})
		totalEdits += len(edits)
	}

	if len(updates) == 0 {
		return Result{Content: "No rename performed: the edits left every file unchanged."}, nil
	}

	for _, u := range updates {
		if err := fsext.AtomicWrite(u.path, u.newContent, 0o644); err != nil {
			return Result{}, fmt.Errorf("writing file %s: %w", u.path, err)
		}
		if err := t.recordWrite(ctx, u.path, u.oldContent, u.newContent); err != nil {
			return Result{}, err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "renamed to %q: %d edit(s) across %d file(s)\n", newName, totalEdits, len(updates))
	for _, u := range updates {
		rel := u.path
		if r, err := filepath.Rel(t.deps.WorkDir, u.path); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		fmt.Fprintf(&b, "  %s (%d edit(s))\n", rel, u.edits)
	}

	return Result{
		Content:  strings.TrimRight(b.String(), "\n"),
		Metadata: map[string]any{"files": len(updates), "edits": totalEdits},
	}, nil
}

func (t *renameTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	args := map[string]any{}
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

func (t *renameTool) recordWrite(ctx context.Context, path string, oldContent, newContent []byte) error {
	if t.deps.FileTracker == nil || t.deps.SessionID == "" {
		return nil
	}
	if _, err := t.deps.FileTracker.RecordWrite(ctx, t.deps.SessionID, path, oldContent, newContent); err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(t.deps.SessionID, path)
	return nil
}
