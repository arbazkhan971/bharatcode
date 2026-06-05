// Package lsp provides a small Language Server Protocol diagnostics client.
package lsp

import "encoding/json"

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

func extSet(exts ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		set[ext] = struct{}{}
	}
	return set
}
