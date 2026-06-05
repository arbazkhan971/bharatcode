package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
)

// CodeActionSource is the LSP capability consumed by the codeactions tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type CodeActionSource interface {
	CodeActions(ctx context.Context, file string, rng lsp.Range) ([]lsp.CodeAction, error)
}

type codeActionsTool struct {
	source  CodeActionSource
	workDir string
}

type codeActionsArgs struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

var schemaCodeActions = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "line"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative path to the file to inspect."
    },
    "line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based line where the action should apply, as reported by diagnostics/symbols/grep/view."
    },
    "column": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based start column on that line. Defaults to 1 (start of line)."
    },
    "end_line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based end line of the selection. Defaults to line (a single-line selection)."
    },
    "end_column": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based end column. Defaults to column (a cursor position rather than a span)."
    }
  }
}`)

//go:embed codeactions.md
var codeActionsDescription string

func newCodeActionsTool(deps Dependencies) Tool {
	t := &codeActionsTool{workDir: deps.WorkDir}
	// A nil *lsp.Manager assigned to the CodeActionSource interface would produce
	// a non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the source
	// when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *codeActionsTool) Name() string {
	return "codeactions"
}

func (t *codeActionsTool) Description() string {
	return codeActionsDescription
}

func (t *codeActionsTool) Schema() json.RawMessage {
	return schemaCodeActions
}

func (t *codeActionsTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args codeActionsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid codeactions arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("codeactions is unavailable: no LSP manager configured"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult("codeactions requires a path"), nil
	}
	if args.Line < 1 {
		return errorResult("codeactions requires a 1-based line (>= 1)"), nil
	}

	col := args.Column
	if col < 1 {
		col = 1
	}
	endLine := args.EndLine
	if endLine < 1 {
		endLine = args.Line
	}
	endCol := args.EndColumn
	if endCol < 1 {
		endCol = col
	}

	root, err := workspaceRoot(t.workDir)
	if err != nil {
		return Result{}, err
	}
	path, rerr := resolveWorkspacePath(root, args.Path)
	if rerr != nil {
		return errorResult(rerr.Error()), nil
	}

	// LSP positions are 0-based; the model speaks the 1-based coordinates that
	// diagnostics/symbols/grep/view emit.
	rng := lsp.Range{
		Start: lsp.Position{Line: args.Line - 1, Character: col - 1},
		End:   lsp.Position{Line: endLine - 1, Character: endCol - 1},
	}

	actions, err := t.source.CodeActions(ctx, path, rng)
	if err != nil {
		return Result{}, fmt.Errorf("getting code actions at %s:%d:%d: %w", args.Path, args.Line, col, err)
	}
	return codeActionsResult(actions), nil
}

// codeActionsResult renders code actions as a sorted, numbered list. Each entry
// shows the title, the action kind in brackets when present, and a note of how
// the action would be applied (an inline edit, a server command, or both).
// Duplicate titles are collapsed. An empty input reports directly.
func codeActionsResult(actions []lsp.CodeAction) Result {
	if len(actions) == 0 {
		return Result{Content: "No code actions available."}
	}

	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Kind != actions[j].Kind {
			return actions[i].Kind < actions[j].Kind
		}
		return actions[i].Title < actions[j].Title
	})

	var b strings.Builder
	var last string
	n := 0
	for _, a := range actions {
		title := strings.TrimSpace(a.Title)
		if title == "" {
			title = "(untitled action)"
		}
		var line strings.Builder
		line.WriteString(title)
		if a.Kind != "" {
			fmt.Fprintf(&line, " [%s]", a.Kind)
		}
		switch note := codeActionApplyNote(a); note {
		case "":
		default:
			fmt.Fprintf(&line, " (%s)", note)
		}
		entry := line.String()
		if entry == last {
			continue
		}
		last = entry
		n++
		fmt.Fprintf(&b, "%d. %s\n", n, entry)
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}
}

// codeActionApplyNote summarizes how an action would take effect so the model
// can tell self-contained edits apart from server-side commands.
func codeActionApplyNote(a lsp.CodeAction) string {
	hasEdit := len(a.Edit.Changes) > 0
	hasCommand := a.Command != nil && a.Command.Command != ""
	switch {
	case hasEdit && hasCommand:
		return "edit + command"
	case hasEdit:
		files := len(a.Edit.Changes)
		if files == 1 {
			return "edit, 1 file"
		}
		return fmt.Sprintf("edit, %d files", files)
	case hasCommand:
		return "command: " + a.Command.Command
	default:
		return ""
	}
}
