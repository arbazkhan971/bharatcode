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
	TypeDefinition(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Implementation(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	References(ctx context.Context, path string, line, col int, includeDeclaration bool) ([]lsp.Location, error)
	IncomingCalls(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	OutgoingCalls(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Hover(ctx context.Context, path string, line, col int) (string, error)
	SignatureHelp(ctx context.Context, path string, line, col int) (string, error)
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
	// IncludeDeclaration is a *bool so its absence is distinguishable from an
	// explicit false: references defaults to including the declaration, so a
	// missing value must mean "include" rather than the zero value's "exclude".
	IncludeDeclaration *bool `json:"include_declaration,omitempty"`
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
      "enum": ["definition", "type_definition", "implementation", "references", "incoming_calls", "outgoing_calls", "hover", "signature"],
      "description": "definition: jump to where the symbol is declared. type_definition: jump to the declaration of the symbol's type. implementation: list the concrete implementations of an interface/abstract method. references: list every use site, including the declaration. incoming_calls: list the functions that call this one (callers). outgoing_calls: list the functions this one calls (callees). hover: the language server's type/signature/doc for the symbol. signature: the call signature(s) at the position, marking which argument the cursor is on (point at a call's arguments). Defaults to definition."
    },
    "include_declaration": {
      "type": "boolean",
      "description": "Only meaningful for the references action. When true (the default) the symbol's own declaration is listed among the references; set it to false to list only the use sites, excluding the declaration itself."
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
	case "type_definition":
		locs, err := t.source.TypeDefinition(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("resolving type definition at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return locationsResult(root, locs, "No type definition found."), nil
	case "implementation":
		locs, err := t.source.Implementation(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding implementations at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return locationsResult(root, locs, "No implementations found."), nil
	case "references":
		includeDecl := args.IncludeDeclaration == nil || *args.IncludeDeclaration
		locs, err := t.source.References(ctx, path, line0, col0, includeDecl)
		if err != nil {
			return Result{}, fmt.Errorf("finding references at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return referencesResult(root, locs), nil
	case "incoming_calls":
		locs, err := t.source.IncomingCalls(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding callers at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return callsResult(root, locs, "caller", "callers", "No callers found."), nil
	case "outgoing_calls":
		locs, err := t.source.OutgoingCalls(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding callees at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return callsResult(root, locs, "callee", "callees", "No callees found."), nil
	case "hover":
		text, err := t.source.Hover(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("reading hover at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Content: "No hover information found."}, nil
		}
		return Result{Content: strings.TrimRight(text, "\n")}, nil
	case "signature":
		text, err := t.source.SignatureHelp(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("reading signature help at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Content: "No signature help found."}, nil
		}
		return Result{Content: strings.TrimRight(text, "\n")}, nil
	default:
		return errorResult(fmt.Sprintf("unknown navigate action %q (want definition, type_definition, implementation, references, incoming_calls, outgoing_calls, hover, or signature)", action)), nil
	}
}

// navigateLocationCap bounds how many location entries renderLocations emits.
// A references or call-hierarchy lookup on a widely-used symbol can resolve to
// hundreds of sites; rendering them all floods the context with little marginal
// value. Capping mirrors the grep tool's grepMatchCap bounded-output philosophy.
// The summary header that referencesResult/callsResult prefix still reports the
// true total, and a trailing "... and N more" notice records what was elided so
// the model knows the list was truncated rather than complete.
const navigateLocationCap = 200

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
	body, _, _ := renderLocations(root, locs)
	return Result{Content: body}
}

// referencesResult renders reference locations like locationsResult but prefixes
// a summary line ("N reference(s) across M file(s):") so the model sees the
// scope of a symbol's usage before scanning the list, matching how goose and
// opencode surface reference searches. An empty input reports no references.
func referencesResult(root string, locs []lsp.Location) Result {
	if len(locs) == 0 {
		return Result{Content: "No references found."}
	}
	body, total, files := renderLocations(root, locs)
	header := fmt.Sprintf("%d %s across %d %s:",
		total, plural(total, "reference", "references"),
		files, plural(files, "file", "files"))
	return Result{Content: header + "\n" + body}
}

// callsResult renders call-hierarchy locations (callers or callees) like
// referencesResult: a "N <noun> across M file(s):" summary line followed by the
// `path:line:column[: source]` entries, so the model sees the scope of the call
// hierarchy before scanning the list. singular/plural name the relation
// ("caller"/"callers" or "callee"/"callees"). An empty input returns emptyMsg.
func callsResult(root string, locs []lsp.Location, singular, plural2, emptyMsg string) Result {
	if len(locs) == 0 {
		return Result{Content: emptyMsg}
	}
	body, total, files := renderLocations(root, locs)
	header := fmt.Sprintf("%d %s across %d %s:",
		total, plural(total, singular, plural2),
		files, plural(files, "file", "files"))
	return Result{Content: header + "\n" + body}
}

// plural returns singular when n == 1 and plural otherwise.
func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// renderLocations sorts and deduplicates locs, returning the formatted entry
// list (one `path:line:column[: source]` per line, no trailing newline), the
// total number of distinct entries, and the number of distinct files those
// entries span. The body is capped at navigateLocationCap entries — beyond that
// a trailing "... and N more (M total) not shown" line is appended — but the
// returned total counts every distinct entry so callers' summary headers report
// the real scope. Callers guarantee locs is non-empty.
func renderLocations(root string, locs []lsp.Location) (string, int, int) {
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].Path != locs[j].Path {
			return locs[i].Path < locs[j].Path
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Character < locs[j].Range.Start.Character
	})

	// Deduplicate by rendered coordinate first so the total count and the cap
	// both operate on distinct sites. Locations are sorted, so duplicates are
	// adjacent and a single look-back suffices.
	type entry struct {
		coord string
		path  string // absolute path, for source-line lookup
		line  int
	}
	files := map[string]struct{}{}
	var entries []entry
	var last string
	for _, l := range locs {
		path := l.Path
		if rel, err := filepath.Rel(root, l.Path); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
		coord := fmt.Sprintf("%s:%d:%d", path, l.Range.Start.Line+1, l.Range.Start.Character+1)
		if coord == last {
			continue
		}
		last = coord
		files[l.Path] = struct{}{}
		entries = append(entries, entry{coord: coord, path: l.Path, line: l.Range.Start.Line})
	}

	shown := len(entries)
	if shown > navigateLocationCap {
		shown = navigateLocationCap
	}
	lineCache := map[string][]string{}
	var b strings.Builder
	for _, e := range entries[:shown] {
		b.WriteString(e.coord)
		if snippet := sourceLine(lineCache, e.path, e.line); snippet != "" {
			b.WriteString(": ")
			b.WriteString(snippet)
		}
		b.WriteByte('\n')
	}
	if len(entries) > navigateLocationCap {
		fmt.Fprintf(&b, "... and %d more (%d total) not shown\n", len(entries)-navigateLocationCap, len(entries))
	}

	return strings.TrimRight(b.String(), "\n"), len(entries), len(files)
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
