package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
)

// SymbolSource is the LSP capability consumed by the symbols tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type SymbolSource interface {
	WorkspaceSymbols(ctx context.Context, query string) ([]lsp.Symbol, error)
	DocumentSymbols(ctx context.Context, path string) ([]lsp.Symbol, error)
}

type symbolsTool struct {
	source  SymbolSource
	workDir string
}

type symbolsArgs struct {
	Query string `json:"query,omitempty"`
	Path  string `json:"path,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

var schemaSymbols = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "query": {
      "type": "string",
      "description": "Symbol name to search for. With path set, filters that file's symbols (case-insensitive substring); omit to list every symbol in the file. Without path, queries the workspace and must be non-empty."
    },
    "path": {
      "type": "string",
      "description": "Optional file path. When set, lists symbols defined in that file (an outline) instead of searching the whole workspace."
    },
    "kind": {
      "type": "string",
      "description": "Restrict results to symbols of these kinds: a comma-separated list of labels like \"function\", \"method\", \"class\", \"struct\", \"interface\", \"variable\", \"constant\", \"enum\". Omit to list every kind. Matches the kind labels shown in the output."
    }
  }
}`)

//go:embed symbols.md
var symbolsDescription string

func newSymbolsTool(deps Dependencies) Tool {
	t := &symbolsTool{workDir: deps.WorkDir}
	// A nil *lsp.Manager assigned to the SymbolSource interface would produce a
	// non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the
	// source when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *symbolsTool) Name() string {
	return "symbols"
}

func (t *symbolsTool) Description() string {
	return symbolsDescription
}

func (t *symbolsTool) Schema() json.RawMessage {
	return schemaSymbols
}

func (t *symbolsTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args symbolsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid symbols arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("symbols are unavailable: no LSP manager configured"), nil
	}

	root, err := workspaceRoot(t.workDir)
	if err != nil {
		return Result{}, err
	}

	query := strings.TrimSpace(args.Query)
	kindSpec := strings.TrimSpace(args.Kind)

	// A full document outline (a path with no query) is rendered as an indented
	// tree so the file's structure — methods under their class, fields under their
	// struct — reads at a glance, matching how goose/opencode surface outlines. A
	// workspace search or a filtered outline stays flat: their results are not a
	// contiguous hierarchy, so indentation would misrepresent the nesting. A kind
	// filter removes nodes too, so it also drops to the flat rendering.
	tree := strings.TrimSpace(args.Path) != "" && query == "" && kindSpec == ""

	var symbols []lsp.Symbol
	if strings.TrimSpace(args.Path) != "" {
		path, rerr := resolveWorkspacePath(root, args.Path)
		if rerr != nil {
			return errorResult(rerr.Error()), nil
		}
		symbols, err = t.source.DocumentSymbols(ctx, path)
		if err != nil {
			return Result{}, fmt.Errorf("getting document symbols for %s: %w", path, err)
		}
		if query != "" {
			needle := strings.ToLower(query)
			filtered := symbols[:0]
			for _, s := range symbols {
				if strings.Contains(strings.ToLower(s.Name), needle) {
					filtered = append(filtered, s)
				}
			}
			symbols = filtered
		}
	} else {
		if query == "" {
			return errorResult("symbols requires a non-empty query when no path is given"), nil
		}
		symbols, err = t.source.WorkspaceSymbols(ctx, query)
		if err != nil {
			return Result{}, fmt.Errorf("searching workspace symbols for %q: %w", query, err)
		}
	}

	// A kind filter keeps only symbols whose rendered kind label is in the
	// requested set, letting the model ask for just the functions, methods, or
	// types in a file/workspace rather than scanning every kind — matching the
	// kind narrowing goose/opencode expose on symbol queries.
	if kindSpec != "" {
		want, unknown := symbolKindFilter(kindSpec)
		if len(unknown) > 0 {
			return errorResult(fmt.Sprintf(
				"unknown symbol kind(s): %s (want labels like function, method, class, struct, interface, variable, constant, enum)",
				strings.Join(unknown, ", "),
			)), nil
		}
		filtered := symbols[:0]
		for _, s := range symbols {
			if want[symbolKindString(s.Kind)] {
				filtered = append(filtered, s)
			}
		}
		symbols = filtered
	}

	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Path != symbols[j].Path {
			return symbols[i].Path < symbols[j].Path
		}
		if symbols[i].Range.Start.Line != symbols[j].Range.Start.Line {
			return symbols[i].Range.Start.Line < symbols[j].Range.Start.Line
		}
		return symbols[i].Name < symbols[j].Name
	})

	if len(symbols) == 0 {
		return Result{Content: "No symbols found."}, nil
	}

	var b strings.Builder
	for _, s := range symbols {
		path := s.Path
		if rel, err := filepath.Rel(root, s.Path); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
		// In tree mode indent each entry by its nesting depth so children sit
		// beneath their parent; otherwise the list is flat.
		if tree && s.Depth > 0 {
			b.WriteString(strings.Repeat("  ", s.Depth))
		}
		fmt.Fprintf(
			&b, "%s:%d:%d: %s %s",
			path,
			s.Range.Start.Line+1,
			s.Range.Start.Character+1,
			symbolKindString(s.Kind),
			s.Name,
		)
		// The server-supplied signature/type (e.g. "func(x int) error") makes the
		// outline far more useful than bare names, matching how goose/opencode
		// render document outlines. Only document symbols carry it.
		if s.Detail != "" {
			fmt.Fprintf(&b, " %s", s.Detail)
		}
		// The "(in container)" suffix is redundant in tree mode, where indentation
		// already places the symbol beneath its container, so it is only shown in
		// the flat renderings (workspace search, filtered outline).
		if s.ContainerName != "" && !tree {
			fmt.Fprintf(&b, " (in %s)", s.ContainerName)
		}
		b.WriteByte('\n')
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// symbolKindString renders an LSP symbol kind as a lowercase label. Unknown
// kinds (or the zero value some servers send) fall back to "symbol".
func symbolKindString(kind lsp.SymbolKind) string {
	switch kind {
	case lsp.File:
		return "file"
	case lsp.Module:
		return "module"
	case lsp.Namespace:
		return "namespace"
	case lsp.Package:
		return "package"
	case lsp.Class:
		return "class"
	case lsp.Method:
		return "method"
	case lsp.Property:
		return "property"
	case lsp.Field:
		return "field"
	case lsp.Constructor:
		return "constructor"
	case lsp.Enum:
		return "enum"
	case lsp.Interface:
		return "interface"
	case lsp.Function:
		return "function"
	case lsp.Variable:
		return "variable"
	case lsp.Constant:
		return "constant"
	case lsp.String:
		return "string"
	case lsp.Number:
		return "number"
	case lsp.Boolean:
		return "boolean"
	case lsp.Array:
		return "array"
	case lsp.Object:
		return "object"
	case lsp.Key:
		return "key"
	case lsp.Null:
		return "null"
	case lsp.EnumMember:
		return "enum-member"
	case lsp.Struct:
		return "struct"
	case lsp.Event:
		return "event"
	case lsp.Operator:
		return "operator"
	case lsp.TypeParameter:
		return "type-parameter"
	default:
		return "symbol"
	}
}

// validSymbolKindLabels is the set of kind labels symbolKindString can emit for
// a known kind, derived from the kind enumeration so it never drifts from the
// renderer. The catch-all "symbol" (used for unknown/zero kinds) is intentionally
// excluded: it names no specific construct, so accepting it as a filter would be
// meaningless.
var validSymbolKindLabels = func() map[string]bool {
	labels := map[string]bool{}
	for k := lsp.File; k <= lsp.TypeParameter; k++ {
		labels[symbolKindString(k)] = true
	}
	return labels
}()

// symbolKindFilter parses a comma-separated list of kind labels (as
// symbolKindString renders them, e.g. "function,method") into a lookup set,
// case-insensitively. Labels that name no known kind are returned separately so
// the caller can reject the request with a clear error rather than silently
// filtering everything out. Blank entries (from stray commas) are skipped.
func symbolKindFilter(spec string) (map[string]bool, []string) {
	want := map[string]bool{}
	var unknown []string
	for _, part := range strings.Split(spec, ",") {
		label := strings.ToLower(strings.TrimSpace(part))
		if label == "" {
			continue
		}
		if !validSymbolKindLabels[label] {
			unknown = append(unknown, label)
			continue
		}
		want[label] = true
	}
	return want, unknown
}
