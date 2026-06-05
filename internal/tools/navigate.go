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
)

// NavigateSource is the LSP capability consumed by the navigate tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type NavigateSource interface {
	Definition(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	References(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Hover(ctx context.Context, path string, line, col int) (string, error)
}

type navigateTool struct {
	source  NavigateSource
	workDir string
}

type navigateArgs struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
	Action string `json:"action,omitempty"`
}

var schemaNavigate = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "line"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative path to the file containing the symbol to inspect."
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
    "action": {
      "type": "string",
      "enum": ["definition", "references", "hover"],
      "description": "definition: jump to where the symbol is declared. references: list every use site, including the declaration. hover: the language server's type/signature/doc for the symbol. Defaults to definition."
    }
  }
}`)

//go:embed navigate.md
var navigateDescription string

func newNavigateTool(deps Dependencies) Tool {
	t := &navigateTool{workDir: deps.WorkDir}
	// A nil *lsp.Manager assigned to the NavigateSource interface would produce
	// a non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the source
	// when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *navigateTool) Name() string {
	return "navigate"
}

func (t *navigateTool) Description() string {
	return navigateDescription
}

func (t *navigateTool) Schema() json.RawMessage {
	return schemaNavigate
}

func (t *navigateTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args navigateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid navigate arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("navigate is unavailable: no LSP manager configured"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult("navigate requires a path"), nil
	}
	if args.Line < 1 {
		return errorResult("navigate requires a 1-based line (>= 1)"), nil
	}
	col := args.Column
	if col < 1 {
		col = 1
	}

	action := strings.TrimSpace(strings.ToLower(args.Action))
	if action == "" {
		action = "definition"
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
	// symbols/grep/view emit.
	line0, col0 := args.Line-1, col-1

	switch action {
	case "definition":
		locs, err := t.source.Definition(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("resolving definition at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return locationsResult(root, locs, "No definition found."), nil
	case "references":
		locs, err := t.source.References(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding references at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return locationsResult(root, locs, "No references found."), nil
	case "hover":
		text, err := t.source.Hover(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("reading hover at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Content: "No hover information found."}, nil
		}
		return Result{Content: strings.TrimRight(text, "\n")}, nil
	default:
		return errorResult(fmt.Sprintf("unknown navigate action %q (want definition, references, or hover)", action)), nil
	}
}

// locationsResult renders LSP locations as a sorted, deduplicated list of
// `path:line:column: <source line>` entries, paths made workspace-relative
// where possible. The trailing source line is the trimmed text at the location
// so the model sees the code at each site, not just coordinates (matching how
// goose/opencode surface navigation results); it is omitted when the file or
// line cannot be read. An empty input returns the supplied empty message.
func locationsResult(root string, locs []lsp.Location, emptyMsg string) Result {
	if len(locs) == 0 {
		return Result{Content: emptyMsg}
	}

	sort.Slice(locs, func(i, j int) bool {
		if locs[i].Path != locs[j].Path {
			return locs[i].Path < locs[j].Path
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Character < locs[j].Range.Start.Character
	})

	lineCache := map[string][]string{}
	var b strings.Builder
	var last string
	for _, l := range locs {
		path := l.Path
		if rel, err := filepath.Rel(root, l.Path); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
		entry := fmt.Sprintf("%s:%d:%d", path, l.Range.Start.Line+1, l.Range.Start.Character+1)
		if entry == last {
			continue
		}
		last = entry
		b.WriteString(entry)
		if snippet := sourceLine(lineCache, l.Path, l.Range.Start.Line); snippet != "" {
			b.WriteString(": ")
			b.WriteString(snippet)
		}
		b.WriteByte('\n')
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}
}

// sourceLine returns the trimmed text of the zero-based line in path, reading
// and caching the file's lines on first access. It returns "" when the file
// cannot be read or the line is out of range, so callers fall back to bare
// coordinates rather than failing.
func sourceLine(cache map[string][]string, path string, line int) string {
	lines, ok := cache[path]
	if !ok {
		if data, err := os.ReadFile(path); err == nil {
			lines = strings.Split(string(data), "\n")
		}
		cache[path] = lines
	}
	if line < 0 || line >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line])
}
