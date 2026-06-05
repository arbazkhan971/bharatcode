// Package lsp provides a small Language Server Protocol diagnostics client.
package lsp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Severity is the diagnostic severity reported by a language server.
type Severity int

const (
	// Error reports a diagnostic that should block normal execution.
	Error Severity = iota + 1
	// Warning reports a diagnostic that may still allow execution.
	Warning
	// Information reports an informational diagnostic.
	Information
	// Hint reports a diagnostic hint.
	Hint
)

// DiagnosticTag is metadata a language server attaches to a diagnostic to
// classify it beyond its severity, mirroring the LSP DiagnosticTag enumeration.
type DiagnosticTag int

const (
	// Unnecessary marks code the server considers dead or redundant, such as an
	// unused import or an unreachable branch. Editors typically fade it out.
	Unnecessary DiagnosticTag = 1
	// Deprecated marks the use of a symbol the server reports as deprecated.
	// Editors typically render it with a strike-through.
	Deprecated DiagnosticTag = 2
)

// String returns the lowercase name of the tag ("unnecessary", "deprecated"),
// or "tag(N)" for an unrecognized value.
func (t DiagnosticTag) String() string {
	switch t {
	case Unnecessary:
		return "unnecessary"
	case Deprecated:
		return "deprecated"
	default:
		return fmt.Sprintf("tag(%d)", int(t))
	}
}

// Diagnostic describes one issue reported by a language server.
type Diagnostic struct {
	Path     string
	Range    Range
	Severity Severity
	Message  string
	Source   string
	// Code is the diagnostic's rule identifier when the server supplies one,
	// such as "E0425" (rustc), "2304" (tsserver), or "unused-import". The LSP
	// wire value may be a string or an integer; both are normalized to a string.
	Code string
	// Tags carries the diagnostic's classification tags (Unnecessary, Deprecated)
	// when the server supplies them, so consumers can tell dead code and
	// deprecated usages apart from ordinary warnings. It is empty when the server
	// attaches none.
	Tags []DiagnosticTag
	// Related carries the diagnostic's relatedInformation entries: other source
	// locations the server links to this issue, such as the conflicting prior
	// declaration behind a "redeclared" error or the unused import's use site.
	// It is empty when the server attaches none.
	Related []RelatedInformation
}

// RelatedInformation is one location the language server links to a diagnostic,
// pairing a source Location with the explanatory Message shown there (e.g.
// "other declaration of 'x'").
type RelatedInformation struct {
	Location Location
	Message  string
}

// Range identifies the start and end positions of a diagnostic.
type Range struct {
	Start Position
	End   Position
}

// Position identifies a zero-based line and character offset.
type Position struct {
	Line      int
	Character int
}

// Location identifies a range within a file, as returned by go-to-definition.
type Location struct {
	Path  string
	Range Range
}

// SymbolKind is the kind of program construct a symbol names, mirroring the LSP
// SymbolKind enumeration.
type SymbolKind int

const (
	// File names a file symbol.
	File SymbolKind = iota + 1
	// Module names a module symbol.
	Module
	// Namespace names a namespace symbol.
	Namespace
	// Package names a package symbol.
	Package
	// Class names a class symbol.
	Class
	// Method names a method symbol.
	Method
	// Property names a property symbol.
	Property
	// Field names a field symbol.
	Field
	// Constructor names a constructor symbol.
	Constructor
	// Enum names an enum symbol.
	Enum
	// Interface names an interface symbol.
	Interface
	// Function names a function symbol.
	Function
	// Variable names a variable symbol.
	Variable
	// Constant names a constant symbol.
	Constant
	// String names a string symbol.
	String
	// Number names a number symbol.
	Number
	// Boolean names a boolean symbol.
	Boolean
	// Array names an array symbol.
	Array
	// Object names an object symbol.
	Object
	// Key names a key symbol.
	Key
	// Null names a null symbol.
	Null
	// EnumMember names an enum member symbol.
	EnumMember
	// Struct names a struct symbol.
	Struct
	// Event names an event symbol.
	Event
	// Operator names an operator symbol.
	Operator
	// TypeParameter names a type parameter symbol.
	TypeParameter
)

// Symbol describes one named program construct reported by a language server,
// such as a function, type, or variable. The same shape carries both document
// symbols (textDocument/documentSymbol) and workspace symbols
// (workspace/symbol).
type Symbol struct {
	Name          string
	Kind          SymbolKind
	Path          string
	Range         Range
	ContainerName string
	// Detail is the server-supplied signature or type for a document symbol,
	// such as "func(x int) error" (gopls) or "i32" (rust-analyzer). It is empty
	// for workspace symbols, whose wire shape carries no detail.
	Detail string
	// Depth is the symbol's nesting level within a hierarchical document-symbol
	// response: 0 for a top-level construct, 1 for a method of a top-level class,
	// and so on. It lets a consumer render the outline as an indented tree.
	// Workspace symbols are flat and always report depth 0.
	Depth int
}

// WorkspaceEdit describes the file edits a rename produces, keyed by file path.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit
}

// CodeAction describes one quick fix or refactoring a language server offers for
// a range, such as "remove unused import". An action may carry an Edit applied
// directly, a Command the server runs on request, or both. A bare Command
// response is normalized into a CodeAction with only its Command set.
type CodeAction struct {
	Title   string
	Kind    string
	Edit    WorkspaceEdit
	Command *Command
	// IsPreferred is the server's hint that this action is the recommended one for
	// the context (e.g. the canonical quick fix for a diagnostic), letting a
	// consumer pick a default without guessing.
	IsPreferred bool
	// Disabled, when non-empty, is the human-readable reason the server marked the
	// action unavailable in the current context. A disabled action is still listed
	// (so the reason is visible) but cannot be applied.
	Disabled string
	// Data is the raw JSON of the original action object as the server sent it.
	// Servers that advertise resolveProvider often return an action with an empty
	// Edit and rely on a follow-up codeAction/resolve request — which echoes this
	// exact object back — to compute the edit lazily. It is nil for bare Command
	// responses, which are not resolvable.
	Data json.RawMessage
}

// Command names a server-side command a code action runs, identified by the
// Command field. Arguments are left opaque, since their shape is defined by the
// individual command.
type Command struct {
	Title     string
	Command   string
	Arguments []json.RawMessage
}

// TextEdit replaces the text in Range with NewText.
type TextEdit struct {
	Range   Range
	NewText string
}

type languageSpec struct {
	name       string
	extension  map[string]struct{}
	command    string
	args       []string
	rootFiles  []string
	languageID string
}

var defaultLanguageSpecs = []languageSpec{
	{
		name:       "go",
		extension:  extSet(".go"),
		command:    "gopls",
		rootFiles:  []string{"go.work", "go.mod", ".git"},
		languageID: "go",
	},
	{
		name:       "typescript",
		extension:  extSet(".ts", ".tsx", ".js", ".jsx"),
		command:    "tsserver",
		rootFiles:  []string{"tsconfig.json", "jsconfig.json", "package.json", ".git"},
		languageID: "typescript",
	},
	{
		name:       "python",
		extension:  extSet(".py"),
		command:    "pyright-langserver",
		args:       []string{"--stdio"},
		rootFiles:  []string{"pyproject.toml", "setup.py", "requirements.txt", ".git"},
		languageID: "python",
	},
	{
		name:       "rust",
		extension:  extSet(".rs"),
		command:    "rust-analyzer",
		rootFiles:  []string{"Cargo.toml", ".git"},
		languageID: "rust",
	},
	{
		name:       "c",
		extension:  extSet(".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"),
		command:    "clangd",
		rootFiles:  []string{"compile_commands.json", "compile_flags.txt", ".git"},
		languageID: "c",
	},
}

// DefaultExtensions returns the file extensions (lowercased, leading dot,
// deduplicated and sorted) covered by the built-in language servers. It is the
// fallback source of truth for callers that need the supported-extension set
// without a live Manager (whose SupportedExtensions also folds in configured
// servers), keeping every such list derived from defaultLanguageSpecs.
func DefaultExtensions() []string {
	set := make(map[string]struct{})
	for _, spec := range defaultLanguageSpecs {
		for ext := range spec.extension {
			set[strings.ToLower(ext)] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for ext := range set {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

func extSet(exts ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		set[ext] = struct{}{}
	}
	return set
}
