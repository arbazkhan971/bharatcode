// Package app wires BharatCode services into one dependency graph.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/audit"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/offline"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/shell"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

const closeTimeout = 5 * time.Second

// agentEventBufferSize is the per-subscriber buffer for the agent event
// topic. pubsub.Publish is non-blocking and drops events for any
// subscriber whose buffer is full, so a small buffer makes streaming
// token deltas lossy under render load and yields missing chat output.
// Sized to comfortably absorb a burst of token-delta events while the
// TUI catches up.
const agentEventBufferSize = 256

// ErrAlreadyClosed is returned by a second Close call.
var ErrAlreadyClosed = errors.New("app: already closed")

// Options configures a New call.
type Options struct {
	// ConfigPath overrides the user config lookup.
	ConfigPath string
	// ProjectDir is the project root. Empty uses os.Getwd().
	ProjectDir string
	// YOLO disables permission prompts for this App.
	YOLO bool
	// Verbose enables debug logging.
	Verbose bool
	// Offline forces sovereignty offline mode on regardless of the
	// BHARATCODE_OFFLINE environment variable: non-localhost providers are
	// rejected and the web_fetch/web_search tools are withheld.
	Offline bool
}

// App is the assembled BharatCode service graph.
type App struct {
	Cfg         *config.Config
	DB          *db.DB
	Audit       *audit.Store
	Bus         *Bus
	LLM         *llm.Registry
	Sessions    *session.Repo
	Ledger      *ledger.Ledger
	Permission  *permission.Checker
	Hooks       *hooks.Engine
	Shell       *shell.Shell
	LSP         *lsp.Manager
	MCP         *mcp.Client
	FileTracker *filetracker.Tracker
	Tools       *tools.Registry
	Agent       *agent.Coordinator
	Logger      *slog.Logger

	rootCtx    context.Context
	cancelRoot context.CancelFunc
	closeMu    sync.Mutex
	closed     bool
}

// Bus groups the typed topics used by app-wired services.
type Bus struct {
	Ledger      *pubsub.Topic[ledger.Summary]
	FileChanges *pubsub.Topic[filetracker.Change]
	LSP         *pubsub.Topic[lsp.Diagnostic]
	MCP         *pubsub.Topic[mcp.Event]
	Agent       *pubsub.Topic[agent.Event]
	Permission  *pubsub.Topic[pubsub.PermissionRequest]
	Shell       *pubsub.Topic[pubsub.ShellJobPayload]
	ToolCalls   *pubsub.Topic[pubsub.ToolCallPayload]
	Todo        *pubsub.Topic[tools.TodoEvent]
}

// Close closes every topic in the bundle.
func (b *Bus) Close() {
	if b == nil {
		return
	}
	b.Ledger.Close()
	b.FileChanges.Close()
	b.LSP.Close()
	b.MCP.Close()
	b.Agent.Close()
	b.Permission.Close()
	b.Shell.Close()
	b.ToolCalls.Close()
	b.Todo.Close()
}

// New constructs the App in dependency order. Signal handling belongs to the
// caller; cancelling ctx cancels the App root context, and Close still performs
// resource cleanup.
func New(ctx context.Context, opts Options) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	logger := newLogger(opts.Verbose)
	rootCtx, cancel := context.WithCancel(ctx)
	app := &App{
		Logger:     logger,
		rootCtx:    rootCtx,
		cancelRoot: cancel,
	}

	var closers []closeStep
	rollback := func(cause error) (*App, error) {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), closeTimeout)
		defer closeCancel()
		if err := closeSteps(closeCtx, closers, logger); err != nil {
			return nil, fmt.Errorf("%w; rollback: %v", cause, err)
		}
		cancel()
		return nil, cause
	}

	projectDir, globalConfigPath, projectConfigPath, dbPath, err := resolvePaths(opts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("constructing util paths: %w", err)
	}

	app.DB, err = db.Open(rootCtx, dbPath)
	if err != nil {
		return rollback(fmt.Errorf("constructing db: %w", err))
	}
	closers = append(closers, closeStep{name: "db", close: func(context.Context) error {
		return app.DB.Close()
	}})

	auditPath := filepath.Join(filepath.Dir(dbPath), "audit.db")
	app.Audit, err = audit.Open(rootCtx, auditPath)
	if err != nil {
		return rollback(fmt.Errorf("constructing audit log: %w", err))
	}
	closers = append(closers, closeStep{name: "audit", close: func(context.Context) error {
		return app.Audit.Close()
	}})

	app.Bus = newBus()
	closers = append(closers, closeStep{name: "pubsub", close: func(context.Context) error {
		app.Bus.Close()
		return nil
	}})

	app.Cfg, err = config.LoadFrom(rootCtx, globalConfigPath, projectConfigPath)
	if err != nil {
		return rollback(fmt.Errorf("constructing config: %w", err))
	}

	// Sovereignty offline mode: enabled by flag or the BHARATCODE_OFFLINE env
	// var. When active, every configured provider must be a localhost endpoint
	// (so prompts and code never leave the machine) and the egress tools are
	// withheld below. Reject the run early with an actionable error rather than
	// silently contacting a remote model.
	offlineMode := opts.Offline || offline.EnabledFromEnv()
	if offlineMode {
		if err := offline.CheckProviders(app.Cfg); err != nil {
			return rollback(err)
		}
		// A remote MCP server is an egress channel too: its tool arguments (which
		// can carry source code) are sent to whatever URL it lives at. Reject any
		// non-loopback http/sse server before the MCP client starts below.
		if err := offline.CheckMCPServers(app.Cfg); err != nil {
			return rollback(err)
		}
		logger.Info(offline.Banner)
	}

	app.Ledger = ledger.New(app.DB, &app.Cfg.Ledger, app.Cfg.Models, app.Bus.Ledger)
	app.Sessions = session.NewRepo(app.DB)
	// Colocate the revert snapshot store next to the database so file
	// changes can be rolled back with `bharatcode revert`.
	snapshotDir := filepath.Join(filepath.Dir(dbPath), "snapshots")
	app.FileTracker = filetracker.NewTrackerWithSnapshots(app.DB, app.Bus.FileChanges, snapshotDir)

	app.LLM, err = llm.NewRegistry(app.Cfg)
	if err != nil {
		return rollback(fmt.Errorf("constructing llm: %w", err))
	}

	app.Permission = permission.New(app.Cfg, app.Bus.Permission)
	app.Permission.SetYolo(opts.YOLO)
	// Record every permission decision in the append-only audit log so the user
	// can later prove exactly what the agent was authorized to do.
	app.Permission.SetAuditLogger(app.Audit.PermissionLogger())

	app.Shell = shell.New(app.Bus.Shell, shell.WithSandboxMode(shell.ParseSandboxMode(app.Cfg.Sandbox.Mode)))
	closers = append(closers, closeStep{name: "shell", close: func(context.Context) error {
		app.Shell.Shutdown()
		return nil
	}})

	app.Hooks = hooks.New(app.Cfg, app.Shell)
	app.LSP = lsp.NewManager(app.Cfg, app.Bus.LSP)
	closers = append(closers, closeStep{name: "lsp", close: app.LSP.Shutdown})

	app.MCP = mcp.NewClient(app.Cfg, app.Permission, app.Bus.MCP)
	// Install the MCP request handlers before Start so the corresponding
	// capabilities are advertised when each server connects. Roots scope servers
	// to the workspace; the sampler answers server-requested LLM completions via
	// the app's own providers (lazily resolved, since the agent is built later);
	// elicitation auto-declines so a server prompting for structured input does
	// not hang the connection.
	app.MCP.SetRoots([]mcp.Root{{URI: "file://" + projectDir, Name: filepath.Base(projectDir)}})
	app.MCP.SetSampler(app.mcpSampler)
	app.MCP.SetElicitationHandler(autoDeclineElicitation)
	if err := app.MCP.Start(rootCtx); err != nil {
		return rollback(fmt.Errorf("constructing mcp: %w", err))
	}
	closers = append(closers, closeStep{name: "mcp", close: app.MCP.Stop})

	app.Tools = tools.NewRegistry(tools.Dependencies{
		Config:      app.Cfg,
		Permission:  app.Permission,
		Shell:       app.Shell,
		LSP:         app.LSP,
		FileTracker: app.FileTracker,
		Bus:         app.Bus.ToolCalls,
		TodoBus:     app.Bus.Todo,
		WorkDir:     projectDir,
		Offline:     offlineMode,
	})

	providers := configuredProviders(app.Cfg, app.LLM)
	app.Agent, err = agent.NewCoordinator(app.Cfg, agent.Dependencies{
		Tools:       app.Tools,
		Permission:  app.Permission,
		Sessions:    app.Sessions,
		FileTracker: app.FileTracker,
		Ledger:      app.Ledger,
		Hooks:       app.Hooks,
		MCP:         app.MCP,
		Bus:         app.Bus.Agent,
		Providers:   providers,
		Router:      routerFromConfig(app.Cfg),
		// Record every tool invocation in the append-only audit log so the
		// sovereignty proof layer captures what the agent did, not just the
		// permission decisions it was granted.
		ToolAuditor: toolAuditLogger{store: app.Audit},
	})
	if err != nil {
		return rollback(fmt.Errorf("constructing agent: %w", err))
	}
	if err := app.Agent.Start(rootCtx); err != nil {
		return rollback(fmt.Errorf("starting agent: %w", err))
	}
	closers = append(closers, closeStep{name: "agent", close: app.Agent.Stop})

	return app, nil
}

// Close shuts down the App in reverse construction order.
func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}

	a.closeMu.Lock()
	if a.closed {
		a.closeMu.Unlock()
		return ErrAlreadyClosed
	}
	a.closed = true
	a.closeMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx, cancel := context.WithTimeout(ctx, closeTimeout)
	defer cancel()

	if a.cancelRoot != nil {
		a.cancelRoot()
	}

	return closeSteps(closeCtx, a.closeSteps(), a.Logger)
}

func newBus() *Bus {
	return &Bus{
		Ledger:      pubsub.NewTopic[ledger.Summary]("app_ledger", 64),
		FileChanges: pubsub.NewTopic[filetracker.Change]("app_file_changes", 128),
		LSP:         pubsub.NewTopic[lsp.Diagnostic]("app_lsp_diagnostics", 256),
		MCP:         pubsub.NewTopic[mcp.Event]("app_mcp", 64),
		Agent:       pubsub.NewTopic[agent.Event]("app_agent", agentEventBufferSize),
		Permission:  pubsub.NewTopic[pubsub.PermissionRequest]("app_permissions", 16),
		Shell:       pubsub.NewTopic[pubsub.ShellJobPayload]("app_shell_jobs", 256),
		ToolCalls:   pubsub.NewTopic[pubsub.ToolCallPayload]("app_tool_calls", 256),
		Todo:        pubsub.NewTopic[tools.TodoEvent]("app_todo", 64),
	}
}

func (a *App) closeSteps() []closeStep {
	return []closeStep{
		{name: "db", close: closeDB(a.DB)},
		{name: "audit", close: closeAudit(a.Audit)},
		{name: "pubsub", close: closeBus(a.Bus)},
		{name: "shell", close: closeShell(a.Shell)},
		{name: "lsp", close: closeLSP(a.LSP)},
		{name: "mcp", close: closeMCP(a.MCP)},
		{name: "agent", close: closeAgent(a.Agent)},
	}
}

func closeAgent(c *agent.Coordinator) func(context.Context) error {
	return func(ctx context.Context) error {
		if c == nil {
			return nil
		}
		return c.Stop(ctx)
	}
}

func closeMCP(c *mcp.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		if c == nil {
			return nil
		}
		return c.Stop(ctx)
	}
}

func closeLSP(m *lsp.Manager) func(context.Context) error {
	return func(ctx context.Context) error {
		if m == nil {
			return nil
		}
		return m.Shutdown(ctx)
	}
}

func closeShell(sh *shell.Shell) func(context.Context) error {
	return func(context.Context) error {
		if sh == nil {
			return nil
		}
		sh.Shutdown()
		return nil
	}
}

func closeBus(b *Bus) func(context.Context) error {
	return func(context.Context) error {
		b.Close()
		return nil
	}
}

func closeAudit(store *audit.Store) func(context.Context) error {
	return func(context.Context) error {
		if store == nil {
			return nil
		}
		return store.Close()
	}
}

func closeDB(database *db.DB) func(context.Context) error {
	return func(context.Context) error {
		if database == nil {
			return nil
		}
		return database.Close()
	}
}

type closeStep struct {
	name  string
	close func(context.Context) error
}

func closeSteps(ctx context.Context, steps []closeStep, logger *slog.Logger) error {
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.close == nil {
			continue
		}
		if err := closeOne(ctx, step); err != nil {
			if logger != nil {
				logger.WarnContext(ctx, "Subsystem close failed", "subsystem", step.name, "err", err)
			}
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func closeOne(ctx context.Context, step closeStep) error {
	done := make(chan error, 1)
	go func() {
		done <- step.close(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("closing %s: %w", step.name, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("closing %s: %w", step.name, ctx.Err())
	}
}

// configuredProviders resolves every enabled provider from the registry and
// applies the optional composable wrappers configured in cfg. Each provider is
// wrapped, innermost-first, in a FailoverProvider when it declares fallbacks,
// then in a CachingProvider when caching is enabled, so a cache hit short-
// circuits before any failover chain runs. With no fallbacks and caching off
// (the defaults) the raw registry provider is returned unchanged.
func configuredProviders(cfg *config.Config, reg *llm.Registry) map[string]llm.Provider {
	base := make(map[string]llm.Provider)
	for _, provider := range cfg.Providers {
		if provider.Disabled {
			continue
		}
		name := strings.ToLower(provider.Name)
		p, err := reg.Get(name)
		if err == nil {
			base[name] = p
		}
	}

	fallbacks := configuredFallbacks(cfg)
	out := make(map[string]llm.Provider, len(base))
	for name, primary := range base {
		out[name] = wrapProvider(name, primary, base, fallbacks, cfg.Cache)
	}
	return out
}

// configuredFallbacks indexes each provider's declared fallback names by the
// lowercased provider name. A provider with no fallbacks is omitted, so the
// common case allocates nothing per provider.
func configuredFallbacks(cfg *config.Config) map[string][]string {
	out := make(map[string][]string)
	for _, provider := range cfg.Providers {
		if provider.Disabled || len(provider.Fallbacks) == 0 {
			continue
		}
		out[strings.ToLower(provider.Name)] = provider.Fallbacks
	}
	return out
}

// wrapProvider applies the failover and caching wrappers to primary as
// configured. Failover is applied first (innermost) so that an outer cache hit
// avoids the chain entirely. Both wrappers degrade to a no-op pass-through when
// not configured, so the returned provider equals primary in the default case.
func wrapProvider(name string, primary llm.Provider, base map[string]llm.Provider, fallbacks map[string][]string, cache config.CacheConfig) llm.Provider {
	p := primary
	if chain := resolveFallbackChain(name, base, fallbacks); len(chain) > 0 {
		if fp, err := llm.NewFailoverProvider(primary, chain...); err == nil {
			p = fp
		}
	}
	if cache.Enabled {
		var store llm.ResponseCache
		if cache.MaxEntries > 0 {
			store = llm.NewLRUCache(cache.MaxEntries)
		}
		if cp, err := llm.NewCachingProvider(p, store); err == nil {
			p = cp
		}
	}
	return p
}

// resolveFallbackChain maps the configured fallback names for the named provider
// to their resolved providers, in order, skipping unknown, disabled, or
// self-referential names so a typo or a fallback to oneself never breaks the
// chain.
func resolveFallbackChain(name string, base map[string]llm.Provider, fallbacks map[string][]string) []llm.Provider {
	names := fallbacks[name]
	if len(names) == 0 {
		return nil
	}
	chain := make([]llm.Provider, 0, len(names))
	for _, fb := range names {
		fbName := strings.ToLower(fb)
		if fbName == name {
			continue
		}
		if p, ok := base[fbName]; ok {
			chain = append(chain, p)
		}
	}
	return chain
}

// routerFromConfig returns the cost-aware router to install on every agent loop,
// or nil when routing is disabled. Returning nil leaves each loop pinned to its
// configured model — the non-breaking default.
func routerFromConfig(cfg *config.Config) agent.Router {
	if cfg == nil || !cfg.Routing.Enabled {
		return nil
	}
	return agent.CostAwareRouter{
		PromptLenThreshold: cfg.Routing.PromptLenThreshold,
		ToolsImplyComplex:  cfg.Routing.ToolsImplyComplex,
	}
}

func resolvePaths(opts Options) (projectDir, globalConfigPath, projectConfigPath, dbPath string, err error) {
	projectDir = opts.ProjectDir
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return "", "", "", "", fmt.Errorf("getting working directory: %w", err)
		}
	}
	projectDir = util.ExpandPath(projectDir)
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolving project directory: %w", err)
	}

	globalConfigPath = opts.ConfigPath
	if globalConfigPath == "" {
		globalConfigPath = config.GlobalPath()
	}
	if globalConfigPath != "" {
		globalConfigPath = util.ExpandPath(globalConfigPath)
	}
	projectConfigPath = config.ProjectPath(projectDir)
	dbPath = defaultDBPath()
	return projectDir, globalConfigPath, projectConfigPath, dbPath, nil
}

func defaultDBPath() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		dataHome = "."
	}
	return filepath.Join(util.ExpandPath(dataHome), "bharatcode", "bharatcode.db")
}

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	if fileInfo, err := os.Stderr.Stat(); err == nil && fileInfo.Mode()&os.ModeCharDevice != 0 {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}
