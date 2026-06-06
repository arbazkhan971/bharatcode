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
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
)

// Metadata keys the navigate tool sets on results so downstream consumers
// (the agent loop, the TUI) can react to individual locations rather than
// re-parsing the free-form coordinate text — the same pattern as
// MetadataTestFailures in the bash tool.
const (
	// MetadataLocations holds a []navigateLocation for the resolved sites.
	// Only set when at least one location was found.
	MetadataLocations = "locations"
	// MetadataTotal holds the true count of distinct locations before any
	// navigateLocationCap truncation is applied, so callers know the rendered
	// list is complete when total == len(MetadataLocations).
	MetadataTotal = "total"
	// MetadataHoverText holds the hover text string from a hover action. It
	// matches the Content field exactly (including any truncation notice), so
	// consumers can read the text directly from metadata without reparsing the
	// free-form Content string.
	MetadataHoverText = "text"
	// MetadataSignatureText holds the signature-help text from a signature
	// action, mirroring MetadataHoverText for the signature action.
	MetadataSignatureText = "text"
)

// navigateLocation is one resolved site returned in navigate metadata. Path is
// workspace-relative when the file is inside the workspace, otherwise absolute.
// Line and Column are 1-based, matching the coordinates in the rendered Content.
type navigateLocation struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// NavigateSource is the LSP capability consumed by the navigate tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type NavigateSource interface {
	Definition(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Declaration(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	TypeDefinition(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Implementation(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	References(ctx context.Context, path string, line, col int, includeDeclaration bool) ([]lsp.Location, error)
	IncomingCalls(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	OutgoingCalls(ctx context.Context, path string, line, col int) ([]lsp.Location, error)
	Hover(ctx context.Context, path string, line, col int) (string, error)
	SignatureHelp(ctx context.Context, path string, line, col int) (string, error)
	PrepareRename(ctx context.Context, path string, line, col int) (*lsp.PrepareRenameResult, error)
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
      "enum": ["definition", "declaration", "type_definition", "implementation", "references", "incoming_calls", "outgoing_calls", "hover", "signature", "prepare_rename"],
      "description": "definition: jump to where the symbol is defined (its implementation). declaration: jump to where the symbol is declared, which differs from its definition in languages that separate the two (a C/C++ header vs source file, a TypeScript ambient declaration); falls back to the definition for languages that do not. type_definition: jump to the declaration of the symbol's type. implementation: list the concrete implementations of an interface/abstract method. references: list every use site, including the declaration. incoming_calls: list the functions that call this one (callers). outgoing_calls: list the functions this one calls (callees). hover: the language server's type/signature/doc for the symbol. signature: the call signature(s) at the position, marking which argument the cursor is on (point at a call's arguments). prepare_rename: check whether the symbol at this position can be renamed and return its current name and the range that would be edited — a lightweight read-only preflight before calling the rename tool. Defaults to definition."
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

func (t *navigateTool) IsReadOnly() bool { return true }
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
		res := locationsResult(root, locs, "No definition found.", "definition")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "declaration":
		locs, err := t.source.Declaration(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("resolving declaration at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := locationsResult(root, locs, "No declaration found.", "declaration")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "type_definition":
		locs, err := t.source.TypeDefinition(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("resolving type definition at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := locationsResult(root, locs, "No type definition found.", "type definition")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "implementation":
		locs, err := t.source.Implementation(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding implementations at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := locationsResult(root, locs, "No implementations found.", "implementation")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "references":
		includeDecl := args.IncludeDeclaration == nil || *args.IncludeDeclaration
		locs, err := t.source.References(ctx, path, line0, col0, includeDecl)
		if err != nil {
			return Result{}, fmt.Errorf("finding references at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := referencesResult(root, locs)
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "incoming_calls":
		locs, err := t.source.IncomingCalls(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding callers at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := callsResult(root, locs, "caller", "callers", "No callers found.")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "outgoing_calls":
		locs, err := t.source.OutgoingCalls(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("finding callees at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		res := callsResult(root, locs, "callee", "callees", "No callees found.")
		if len(locs) > 0 {
			res.Metadata = navigateLocationsMetadata(root, locs)
		}
		return res, nil
	case "hover":
		text, err := t.source.Hover(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("reading hover at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Content: "No hover information found."}, nil
		}
		bounded := boundHoverText(strings.TrimRight(text, "\n"))
		return Result{
			Content: bounded,
			Metadata: map[string]any{
				"path":            args.Path,
				"line":            args.Line,
				"column":          col,
				MetadataHoverText: bounded,
			},
		}, nil
	case "signature":
		text, err := t.source.SignatureHelp(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("reading signature help at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Content: "No signature help found."}, nil
		}
		bounded := boundHoverText(strings.TrimRight(text, "\n"))
		return Result{
			Content: bounded,
			Metadata: map[string]any{
				"path":                args.Path,
				"line":                args.Line,
				"column":              col,
				MetadataSignatureText: bounded,
			},
		}, nil
	case "prepare_rename":
		pr, err := t.source.PrepareRename(ctx, path, line0, col0)
		if err != nil {
			return Result{}, fmt.Errorf("checking rename at %s:%d:%d: %w", args.Path, args.Line, col, err)
		}
		return prepareRenameResult(root, args.Path, pr), nil
	default:
		return errorResult(fmt.Sprintf("unknown navigate action %q (want definition, declaration, type_definition, implementation, references, incoming_calls, outgoing_calls, hover, signature, or prepare_rename)", action)), nil
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

// navigateSnippetMax caps how many characters of a location's source-line
// snippet renderLocations appends after the `path:line:column:` coordinate. The
// snippet is an inline annotation, one per location, so a single reference into
// a minified or generated file (whose lines run to many thousands of characters)
// would otherwise dominate the output budget. The cap mirrors the view tool's
// per-line truncation (truncateLine), kept smaller here since these snippets are
// supplementary context rather than the primary content; the trailing marker
// truncateLine appends records that the line was clipped.
const navigateSnippetMax = 200

// navigateHoverByteCap bounds how many bytes of hover/signature text the
// navigate tool emits. A language server's hover for a heavily documented
// symbol — a long doc comment, an inlined type with many fields, a re-exported
// declaration that drags in its whole definition — can run to many kilobytes;
// surfacing it whole floods the context for marginal value. The cap mirrors the
// bounded-output philosophy of navigateLocationCap/navigateSnippetMax: the text
// is cut on a line boundary where possible and a trailing notice records how
// many lines were elided so the model knows the content was truncated rather
// than complete, and can re-read the source directly if it needs the rest.
const navigateHoverByteCap = 4000

// boundHoverText trims text to navigateHoverByteCap bytes, preferring to cut on
// a line boundary, and appends a notice naming how many lines were dropped.
// Text already within the cap is returned unchanged. The cut is backed off to a
// valid rune boundary so a multibyte character is never split.
func boundHoverText(text string) string {
	if len(text) <= navigateHoverByteCap {
		return text
	}
	cut := navigateHoverByteCap
	// Prefer a line boundary so the shown text ends on a whole line rather than
	// mid-word; fall back to the raw byte cap for a single oversized line.
	if nl := strings.LastIndexByte(text[:cut], '\n'); nl > 0 {
		cut = nl
	}
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	remainder := strings.TrimPrefix(text[cut:], "\n")
	elided := strings.Count(remainder, "\n")
	if remainder != "" {
		elided++
	}
	return text[:cut] + fmt.Sprintf("\n... [%d more %s truncated]", elided, plural(elided, "line", "lines"))
}

// navigateLocationsMetadata builds the MetadataLocations/MetadataTotal payload
// for a non-empty location slice. It applies the same sort and dedup logic as
// renderLocations so the structured slice and the rendered Content are always
// consistent. Paths are workspace-relative when possible, matching renderLocations.
func navigateLocationsMetadata(root string, locs []lsp.Location) map[string]any {
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].Path != locs[j].Path {
			return locs[i].Path < locs[j].Path
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Character < locs[j].Range.Start.Character
	})
	var out []navigateLocation
	var last string
	for _, l := range locs {
		p := l.Path
		if rel, err := filepath.Rel(root, l.Path); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.ToSlash(rel)
		}
		coord := fmt.Sprintf("%s:%d:%d", p, l.Range.Start.Line+1, l.Range.Start.Character+1)
		if coord == last {
			continue
		}
		last = coord
		out = append(out, navigateLocation{
			Path:   p,
			Line:   l.Range.Start.Line + 1,
			Column: l.Range.Start.Character + 1,
		})
	}
	return map[string]any{
		MetadataLocations: out,
		MetadataTotal:     len(out),
	}
}

// locationsResult renders LSP locations as a sorted, deduplicated list of
// `path:line:column: <source line>` entries, paths made workspace-relative
// where possible. The trailing source line is the trimmed text at the location
// so the model sees the code at each site, not just coordinates (matching how
// goose/opencode surface navigation results); it is omitted when the file or
// line cannot be read. When more than one location is found a summary header
// "N <noun>s across M file(s):" is prepended so the model sees the scope
// before scanning the list, matching how referencesResult and callsResult
// shape their output. Single and empty results are unaffected. An empty input
// returns the supplied empty message.
func locationsResult(root string, locs []lsp.Location, emptyMsg, noun string) Result {
	if len(locs) == 0 {
		return Result{Content: emptyMsg}
	}
	body, total, files := renderLocations(root, locs)
	if total <= 1 {
		return Result{Content: body}
	}
	header := fmt.Sprintf("%d %s across %d %s:",
		total, pluralize(noun, total),
		files, pluralize("file", files))
	return Result{Content: header + "\n" + body}
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

// prepareRenameResult formats the outcome of a prepare_rename action. A nil
// result means the server reported the position is not renamable. Otherwise it
// renders the symbol name and range so the agent can confirm before calling
// rename.
func prepareRenameResult(root, relPath string, pr *lsp.PrepareRenameResult) Result {
	if pr == nil {
		return Result{Content: "Symbol at this position cannot be renamed."}
	}
	if pr.DefaultBehavior {
		return Result{
			Content:  fmt.Sprintf("Symbol at %s:%d:%d can be renamed (server uses word under cursor).", relPath, pr.Range.Start.Line+1, pr.Range.Start.Character+1),
			Metadata: map[string]any{"renamable": true, "default_behavior": true},
		}
	}
	// Build a display name: Placeholder if the server provided it, otherwise
	// the coordinates of the range so the agent can look it up.
	name := pr.Placeholder
	startLine := pr.Range.Start.Line + 1
	startCol := pr.Range.Start.Character + 1
	endLine := pr.Range.End.Line + 1
	endCol := pr.Range.End.Character + 1
	var content string
	if name != "" {
		content = fmt.Sprintf("Symbol %q at %s:%d:%d-%d:%d can be renamed.", name, relPath, startLine, startCol, endLine, endCol)
	} else {
		content = fmt.Sprintf("Symbol at %s:%d:%d-%d:%d can be renamed.", relPath, startLine, startCol, endLine, endCol)
	}
	meta := map[string]any{
		"renamable":          true,
		"range_start_line":   startLine,
		"range_start_column": startCol,
		"range_end_line":     endLine,
		"range_end_column":   endCol,
	}
	if name != "" {
		meta["placeholder"] = name
	}
	_ = root // kept for interface consistency with other result helpers
	return Result{Content: content, Metadata: meta}
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
	// Cap the snippet so a location inside a minified/generated file's very wide
	// line stays a one-line annotation instead of flooding the output, mirroring
	// the view tool's per-line truncation.
	return truncateLine(strings.TrimSpace(lines[line]), navigateSnippetMax)
}
