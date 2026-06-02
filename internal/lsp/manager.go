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

// References returns every location referencing the symbol at the position in
// path, including its declaration, starting a server if needed. A nil slice
// with a nil error means no server is configured for the file or the symbol has
// no references.
func (m *Manager) References(ctx context.Context, path string, line, col int) ([]Location, error) {
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

	locations, err := c.references(ctx, abs, line, col)
	if err != nil {
		return nil, err
	}
	return locations, nil
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
