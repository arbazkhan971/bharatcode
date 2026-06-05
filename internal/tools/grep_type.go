package tools

import (
	"path/filepath"
	"sort"
	"strings"
)

// grepTypeExtensions maps a ripgrep-style file-type name to the file
// extensions (without a leading dot) it covers. It powers the grep tool's
// `type` filter, the analogue of ripgrep's `--type go` / Claude Code's Grep
// `type`.
//
// Both the rg path and the Go fallback derive their filter from this single
// table — the rg path through a synthetic `--type-add` definition rather than
// rg's own (much larger) built-in type set — so the two paths select exactly
// the same files on any machine, regardless of whether ripgrep is installed.
// Aliases (e.g. python→py) point at the same logical set.
var grepTypeExtensions = map[string][]string{
	"go":       {"go"},
	"py":       {"py", "pyi", "pyw"},
	"python":   {"py", "pyi", "pyw"},
	"js":       {"js", "jsx", "mjs", "cjs"},
	"jsx":      {"jsx"},
	"ts":       {"ts", "tsx", "mts", "cts"},
	"tsx":      {"tsx"},
	"rust":     {"rs"},
	"java":     {"java"},
	"kotlin":   {"kt", "kts"},
	"kt":       {"kt", "kts"},
	"scala":    {"scala", "sc"},
	"c":        {"c", "h"},
	"cpp":      {"cpp", "cc", "cxx", "hpp", "hh", "hxx", "h"},
	"cs":       {"cs"},
	"csharp":   {"cs"},
	"rb":       {"rb"},
	"ruby":     {"rb"},
	"php":      {"php"},
	"swift":    {"swift"},
	"sh":       {"sh", "bash", "zsh"},
	"shell":    {"sh", "bash", "zsh"},
	"html":     {"html", "htm"},
	"css":      {"css", "scss", "sass", "less"},
	"json":     {"json"},
	"yaml":     {"yaml", "yml"},
	"yml":      {"yaml", "yml"},
	"toml":     {"toml"},
	"md":       {"md", "markdown"},
	"markdown": {"md", "markdown"},
	"sql":      {"sql"},
	"proto":    {"proto"},
	"vue":      {"vue"},
	"txt":      {"txt", "text"},
}

// resolveGrepType returns the extensions for a type name (case-insensitive,
// trimmed) and whether the name is known.
func resolveGrepType(name string) ([]string, bool) {
	exts, ok := grepTypeExtensions[strings.ToLower(strings.TrimSpace(name))]
	return exts, ok
}

// grepTypeSet builds a set of extensions for membership checks, or nil when the
// name is empty or unknown.
func grepTypeSet(name string) map[string]bool {
	exts, ok := resolveGrepType(name)
	if !ok {
		return nil
	}
	set := make(map[string]bool, len(exts))
	for _, e := range exts {
		set[e] = true
	}
	return set
}

// extInTypeSet reports whether a file name's extension is in the set.
func extInTypeSet(name string, set map[string]bool) bool {
	if set == nil {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	return set[ext]
}

// grepTypeNames returns the supported type names, sorted, for error messages.
func grepTypeNames() string {
	names := make([]string, 0, len(grepTypeExtensions))
	for k := range grepTypeExtensions {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
