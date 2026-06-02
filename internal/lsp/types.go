// Package lsp provides a small Language Server Protocol diagnostics client.
package lsp

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
