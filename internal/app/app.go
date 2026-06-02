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
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/shell"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

const closeTimeout = 5 * time.Second

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
}

// App is the assembled BharatCode service graph.
type App struct {
	Cfg         *config.Config
	DB          *db.DB
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

	app.Bus = newBus()
	closers = append(closers, closeStep{name: "pubsub", close: func(context.Context) error {
		app.Bus.Close()
		return nil
	}})

	app.Cfg, err = config.LoadFrom(rootCtx, globalConfigPath, projectConfigPath)
	if err != nil {
		return rollback(fmt.Errorf("constructing config: %w", err))
	}

	app.Ledger = ledger.New(app.DB, &app.Cfg.Ledger, app.Cfg.Models, app.Bus.Ledger)
	app.Sessions = session.NewRepo(app.DB)
	app.FileTracker = filetracker.NewTracker(app.DB, app.Bus.FileChanges)

	app.LLM, err = llm.NewRegistry(app.Cfg)
	if err != nil {
		return rollback(fmt.Errorf("constructing llm: %w", err))
	}

	app.Permission = permission.New(app.Cfg, app.Bus.Permission)
	app.Permission.SetYolo(opts.YOLO)

	app.Shell = shell.New(app.Bus.Shell)
	closers = append(closers, closeStep{name: "shell", close: func(context.Context) error {
		app.Shell.Shutdown()
		return nil
	}})

	app.Hooks = hooks.New(app.Cfg, app.Shell)
	app.LSP = lsp.NewManager(app.Cfg, app.Bus.LSP)
	closers = append(closers, closeStep{name: "lsp", close: app.LSP.Shutdown})

	app.MCP = mcp.NewClient(app.Cfg, app.Permission, app.Bus.MCP)
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
		Agent:       pubsub.NewTopic[agent.Event]("app_agent", 128),
		Permission:  pubsub.NewTopic[pubsub.PermissionRequest]("app_permissions", 16),
		Shell:       pubsub.NewTopic[pubsub.ShellJobPayload]("app_shell_jobs", 256),
		ToolCalls:   pubsub.NewTopic[pubsub.ToolCallPayload]("app_tool_calls", 256),
		Todo:        pubsub.NewTopic[tools.TodoEvent]("app_todo", 64),
	}
}

func (a *App) closeSteps() []closeStep {
	return []closeStep{
		{name: "db", close: closeDB(a.DB)},
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

func configuredProviders(cfg *config.Config, reg *llm.Registry) map[string]llm.Provider {
	out := make(map[string]llm.Provider)
	for _, provider := range cfg.Providers {
		if provider.Disabled {
			continue
		}
		name := strings.ToLower(provider.Name)
		p, err := reg.Get(name)
		if err == nil {
			out[name] = p
		}
	}
	return out
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
