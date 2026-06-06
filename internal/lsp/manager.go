package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// Manager owns language-server processes for one BharatCode session.
type Manager struct {
	bus *pubsub.Topic[Diagnostic]

	mu            sync.Mutex
	root          string
	specs         map[string]languageSpec
	clients       map[string]*client
	missingWarned map[string]struct{}
	published     map[diagnosticKey]struct{}
	discovery     map[string]bool
}

type diagnosticKey struct {
	path    string
	rng     Range
	message string
}

// NewManager constructs a session-scoped LSP manager.
func NewManager(cfg *config.Config, bus *pubsub.Topic[Diagnostic]) *Manager {
	if cfg == nil {
		cfg = config.Default()
	}
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	return &Manager{
		bus:           bus,
		root:          root,
		specs:         buildSpecs(cfg),
		clients:       make(map[string]*client),
		missingWarned: make(map[string]struct{}),
		published:     make(map[diagnosticKey]struct{}),
		discovery:     make(map[string]bool),
	}
}

// Diagnostics returns diagnostics for path, starting a server if needed.
func (m *Manager) Diagnostics(ctx context.Context, path string) ([]Diagnostic, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving diagnostics path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	diagnostics, err := c.diagnostics(ctx, abs)
	if err != nil {
		return nil, err
	}
	m.publish(ctx, diagnostics)
	return diagnostics, nil
}

// NotifyChange informs the language server for path that its on-disk contents
// changed (e.g. after an edit) so a subsequent Diagnostics call reflects the new
// text instead of the version the server first opened. It is a no-op (nil error)
// for files with no configured language server.
func (m *Manager) NotifyChange(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving change path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return c.change(ctx, abs)
}

// Hover returns the hover text the language server reports for the position in
// path, starting a server if needed. An empty string with a nil error means no
// server is configured for the file or the server reported no hover.
func (m *Manager) Hover(ctx context.Context, path string, line, col int) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving hover path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return "", nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	text, err := c.hover(ctx, abs, line, col)
	if err != nil {
		return "", err
	}
	return text, nil
}

// SignatureHelp returns the language server's signature help for the call at the
// position in path (the function signature and which argument the cursor is on),
// starting a server if needed. An empty string with a nil error means no server
// is configured for the file or the position is not inside a call.
func (m *Manager) SignatureHelp(ctx context.Context, path string, line, col int) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving signature help path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return "", nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	text, err := c.signatureHelp(ctx, abs, line, col)
	if err != nil {
		return "", err
	}
	return text, nil
}

// Definition returns the locations the language server resolves the symbol at
// the position in path to, starting a server if needed. A nil slice with a nil
// error means no server is configured for the file or the symbol is undefined.
func (m *Manager) Definition(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving definition path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	locations, err := c.definition(ctx, abs, line, col)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// Declaration returns the locations the language server resolves the symbol at
// the position in path to via textDocument/declaration, starting a server if
// needed. For languages that separate declaration from definition (a C/C++
// header vs its source file, a TypeScript ambient `declare`), this lands on the
// declaration site rather than the implementation Definition jumps to. A nil
// slice with a nil error means no server is configured for the file or the
// symbol has no declaration.
func (m *Manager) Declaration(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving declaration path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	locations, err := c.declaration(ctx, abs, line, col)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// TypeDefinition returns the locations of the type of the symbol at the
// position in path, starting a server if needed. A nil slice with a nil error
// means no server is configured for the file or the symbol has no type
// definition.
func (m *Manager) TypeDefinition(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving type definition path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	locations, err := c.typeDefinition(ctx, abs, line, col)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// Implementation returns the locations implementing the symbol at the position
// in path (e.g. the concrete types satisfying an interface), starting a server
// if needed. A nil slice with a nil error means no server is configured for the
// file or the symbol has no implementations.
func (m *Manager) Implementation(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving implementation path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	locations, err := c.implementation(ctx, abs, line, col)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// References returns every location referencing the symbol at the position in
// path, starting a server if needed. When includeDeclaration is true the
// symbol's own declaration is included among the results; when false only the
// use sites are returned. A nil slice with a nil error means no server is
// configured for the file or the symbol has no references.
func (m *Manager) References(ctx context.Context, path string, line, col int, includeDeclaration bool) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving references path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	locations, err := c.references(ctx, abs, line, col, includeDeclaration)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// IncomingCalls returns the locations of the symbols that call the function at
// the position in path (who calls this), starting a server if needed. A nil
// slice with a nil error means no server is configured for the file or the
// symbol is not callable / has no callers.
func (m *Manager) IncomingCalls(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving incoming calls path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return c.incomingCalls(ctx, abs, line, col)
}

// OutgoingCalls returns the locations of the symbols that the function at the
// position in path calls (what this calls), starting a server if needed. A nil
// slice with a nil error means no server is configured for the file or the
// symbol is not callable / makes no calls.
func (m *Manager) OutgoingCalls(ctx context.Context, path string, line, col int) ([]Location, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving outgoing calls path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return c.outgoingCalls(ctx, abs, line, col)
}

// PrepareRename checks whether the symbol at the position in path can be
// renamed, starting a server if needed. A nil result with a nil error means no
// server is configured for the file or the server reports the position is not
// renamable. On success the Range is what would be selected for editing and
// Placeholder is the current symbol name (empty when the server returned only
// a range or the defaultBehavior form).
func (m *Manager) PrepareRename(ctx context.Context, path string, line, col int) (*PrepareRenameResult, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving prepare rename path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return c.prepareRename(ctx, abs, line, col)
}

// Rename returns the file edits the language server would apply to rename the
// symbol at the position in path to newName, starting a server if needed. An
// empty WorkspaceEdit with a nil error means no server is configured for the
// file or the symbol cannot be renamed.
func (m *Manager) Rename(ctx context.Context, path string, line, col int, newName string) (WorkspaceEdit, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceEdit{}, fmt.Errorf("resolving rename path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return WorkspaceEdit{}, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return WorkspaceEdit{}, err
	}
	if !ok {
		return WorkspaceEdit{}, nil
	}

	edit, err := c.rename(ctx, abs, line, col, newName)
	if err != nil {
		return WorkspaceEdit{}, err
	}
	return edit, nil
}

// DocumentSymbols returns the symbols the language server reports for the file,
// starting a server if needed. A nil slice with a nil error means no server is
// configured for the file or the server reported no symbols.
func (m *Manager) DocumentSymbols(ctx context.Context, path string) ([]Symbol, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving document symbols path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	symbols, err := c.documentSymbol(ctx, abs)
	if err != nil {
		return nil, err
	}
	return symbols, nil
}

// Format returns the text edits the language server would apply to reformat the
// file, starting a server if needed. A nil slice with a nil error means no
// server is configured for the file or the file is already formatted.
func (m *Manager) Format(ctx context.Context, path string) ([]TextEdit, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving format path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	edits, err := c.format(ctx, abs)
	if err != nil {
		return nil, err
	}
	return edits, nil
}

// FormatRange returns the edits the language server would apply to reformat just
// the given range of the file, starting a server if needed. A nil slice with a
// nil error means no server is configured for the file or the server reports no
// edits for the range.
func (m *Manager) FormatRange(ctx context.Context, path string, rng Range) ([]TextEdit, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving range format path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	edits, err := c.formatRange(ctx, abs, rng)
	if err != nil {
		return nil, err
	}
	return edits, nil
}

// CodeActions returns the quick fixes and refactorings the language server
// offers for the range in file, starting a server if needed. A nil slice with a
// nil error means no server is configured for the file or the server offers no
// actions for the range.
// CodeActions returns the quick fixes and refactorings the language server
// offers for the range in file. When only is non-empty it restricts the request
// to those LSP CodeActionKinds: this is passed through to the server's request
// context, letting it produce whole-file "source.*" actions (organize imports,
// fix-all) that some servers compute only when explicitly asked for them. A nil
// or empty only requests every available action.
func (m *Manager) CodeActions(ctx context.Context, file string, rng Range, only []string) ([]CodeAction, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("resolving code actions path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return nil, nil
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	actions, err := c.codeAction(ctx, abs, rng, only)
	if err != nil {
		return nil, err
	}
	return actions, nil
}

// ResolveCodeAction asks the language server serving file to populate the edit
// of an action it returned without one, via a codeAction/resolve round-trip. The
// action must carry the resolve data the server originally sent (CodeAction.Data,
// preserved by CodeActions). A nil error with the action's edit still empty means
// the server resolved to no edit. An error means no server is configured for the
// file or the resolve request failed.
func (m *Manager) ResolveCodeAction(ctx context.Context, file string, action CodeAction) (CodeAction, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return CodeAction{}, fmt.Errorf("resolving code action path: %w", err)
	}

	spec, ok := m.specForPath(ctx, abs)
	if !ok {
		return CodeAction{}, fmt.Errorf("no language server configured for %s", file)
	}

	c, ok, err := m.client(ctx, spec, abs)
	if err != nil {
		return CodeAction{}, err
	}
	if !ok {
		return CodeAction{}, fmt.Errorf("no language server available for %s", file)
	}

	return c.resolveCodeAction(ctx, action)
}

// WorkspaceSymbols returns the symbols matching query across the workspace,
// starting servers if needed. Every discovered language server is queried and
// the matches are aggregated. A nil slice with a nil error means no server is
// configured or no server reported a match.
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	// Resolve the server root from a path inside the workspace, since
	// rootForPath searches upward from a file's directory.
	rootMarker := filepath.Join(m.root, "_")
	var symbols []Symbol
	for _, spec := range m.workspaceSpecs(ctx) {
		c, ok, err := m.client(ctx, spec, rootMarker)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		matches, err := c.workspaceSymbol(ctx, query)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, matches...)
	}
	return symbols, nil
}

// workspaceSpecs returns the discovered language specs whose files are present
// in the workspace, sorted by name for a deterministic query order.
func (m *Manager) workspaceSpecs(ctx context.Context) []languageSpec {
	names := make([]string, 0, len(m.specs))
	for name := range m.specs {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]languageSpec, 0, len(names))
	for _, name := range names {
		spec := m.specs[name]
		if !m.languageDiscovered(ctx, spec) {
			continue
		}
		specs = append(specs, spec)
	}
	return specs
}

// SupportedExtensions returns the file extensions (lowercased, leading dot,
// deduplicated and sorted) for which this manager has a language server
// configured. It is the source of truth for which files a workspace-wide
// diagnostics scan should open, so the scan tracks the configured language set —
// including servers added or overridden via config — without keeping a second
// hardcoded list in sync. A custom server registered for a language with no
// known extensions contributes nothing, matching the scan's file-driven model.
func (m *Manager) SupportedExtensions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]struct{})
	for _, spec := range m.specs {
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

// Shutdown terminates every running language-server process.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	clients := make([]*client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*client)
	m.mu.Unlock()

	if len(clients) == 0 {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var firstErr error
	for _, c := range clients {
		if err := c.shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("shutting down language servers: %w", firstErr)
	}
	return nil
}

func (m *Manager) specForPath(ctx context.Context, path string) (languageSpec, bool) {
	ext := filepath.Ext(path)
	for _, spec := range m.specs {
		if _, ok := spec.extension[ext]; !ok {
			continue
		}
		if !m.languageDiscovered(ctx, spec) {
			return languageSpec{}, false
		}
		return spec, true
	}
	return languageSpec{}, false
}

func (m *Manager) client(ctx context.Context, spec languageSpec, path string) (*client, bool, error) {
	m.mu.Lock()
	if c, ok := m.clients[spec.name]; ok {
		m.mu.Unlock()
		return c, true, nil
	}
	if _, ok := m.missingWarned[spec.name]; ok {
		m.mu.Unlock()
		return nil, false, nil
	}
	m.mu.Unlock()

	c, err := startClient(ctx, spec, m.rootForPath(path, spec))
	if err != nil {
		if isMissingServer(err) {
			m.warnMissing(ctx, spec, path)
			return nil, false, nil
		}
		return nil, false, err
	}

	m.mu.Lock()
	if existing, ok := m.clients[spec.name]; ok {
		m.mu.Unlock()
		_ = c.shutdown(ctx)
		return existing, true, nil
	}
	m.clients[spec.name] = c
	m.mu.Unlock()
	return c, true, nil
}

func (m *Manager) warnMissing(ctx context.Context, spec languageSpec, path string) {
	m.mu.Lock()
	if _, ok := m.missingWarned[spec.name]; ok {
		m.mu.Unlock()
		return
	}
	m.missingWarned[spec.name] = struct{}{}
	m.mu.Unlock()

	m.publish(ctx, []Diagnostic{{
		Path:     path,
		Severity: Warning,
		Message:  fmt.Sprintf("Language server %q is not available", spec.command),
		Source:   "lsp",
	}})
}

func (m *Manager) publish(ctx context.Context, diagnostics []Diagnostic) {
	if m.bus == nil {
		return
	}
	for _, diagnostic := range diagnostics {
		key := diagnosticKey{
			path:    diagnostic.Path,
			rng:     diagnostic.Range,
			message: diagnostic.Message,
		}

		m.mu.Lock()
		if _, ok := m.published[key]; ok {
			m.mu.Unlock()
			continue
		}
		m.published[key] = struct{}{}
		m.mu.Unlock()
		m.bus.Publish(ctx, diagnostic)
	}
}

func (m *Manager) languageDiscovered(ctx context.Context, spec languageSpec) bool {
	m.mu.Lock()
	if found, ok := m.discovery[spec.name]; ok {
		m.mu.Unlock()
		return found
	}
	m.mu.Unlock()

	found := false
	for ext := range spec.extension {
		if hasFileWithExt(ctx, m.root, ext) {
			found = true
			break
		}
	}

	m.mu.Lock()
	m.discovery[spec.name] = found
	m.mu.Unlock()
	return found
}

func (m *Manager) rootForPath(path string, spec languageSpec) string {
	current := filepath.Dir(path)
	for {
		for _, marker := range spec.rootFiles {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return m.root
}

func buildSpecs(cfg *config.Config) map[string]languageSpec {
	specs := make(map[string]languageSpec, len(defaultLanguageSpecs))
	for _, spec := range defaultLanguageSpecs {
		specs[spec.name] = spec
	}
	for _, server := range cfg.LSP {
		if server.Disabled {
			continue
		}
		for _, language := range server.Languages {
			spec, ok := specs[language]
			if !ok {
				spec = languageSpec{
					name:       language,
					extension:  extSet(),
					languageID: language,
				}
			}
			spec.command = server.Command
			spec.args = append([]string(nil), server.Args...)
			if len(server.RootFiles) > 0 {
				spec.rootFiles = append([]string(nil), server.RootFiles...)
			}
			specs[language] = spec
		}
	}
	return specs
}

func hasFileWithExt(ctx context.Context, root, ext string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "target", ".venv":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) == ext {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func isMissingServer(err error) bool {
	return err != nil &&
		(errors.Is(err, exec.ErrNotFound) ||
			os.IsNotExist(err) ||
			strings.Contains(err.Error(), "executable file not found"))
}
