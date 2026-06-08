// Package cmd is the Cobra command tree for the bharatcode binary.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/offline"
	"github.com/arbazkhan971/bharatcode/internal/selfupdate"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui"
	"github.com/spf13/cobra"
)

// startupUpdateTimeout bounds the opt-in auto-update probe+install at launch so
// a slow or unreachable network can never delay the TUI by more than a moment.
// The whole step is best-effort: any failure is a non-fatal warning.
const startupUpdateTimeout = 6 * time.Second

type rootOptions struct {
	configPath      string
	verbose         bool
	yolo            bool
	projectDir      string
	offline         bool
	continueSession bool
	profile         string
}

var (
	newApp = app.New
	// runTUI launches the interactive TUI. initialSessionID is non-empty when
	// --continue / -c was passed and a prior session was found for the project.
	runTUI = func(ctx context.Context, application *app.App, initialSessionID string) error {
		loop, err := application.Agent.Agent("coder")
		if err != nil {
			return fmt.Errorf("resolving default agent: %w", err)
		}
		return tui.Run(ctx, tui.Dependencies{
			Agent:            loop,
			Coordinator:      application.Agent,
			Sessions:         application.Sessions,
			Cfg:              application.Cfg,
			Bus:              application.Bus.Agent,
			Permission:       application.Permission,
			Ledger:           application.Ledger,
			FileTracker:      application.FileTracker,
			Logger:           application.Logger,
			MCP:              application.MCP,
			InitialSessionID: initialSessionID,
		})
	}
)

// Execute parses os.Args and exits with the matched command's status.
func Execute() {
	root := newRootCmd()
	root.SetArgs(os.Args[1:])
	if err := executeCommand(context.Background(), root); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:           "bharatcode",
		Short:         "AI coding assistant for the terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q", args[0])
			}
			ctx := cmd.Context()
			application, err := buildApp(ctx, opts)
			if err != nil {
				return err
			}
			defer closeApp(ctx, application)
			// Opt-in, best-effort self-update before the TUI takes over the
			// screen. Gated to the interactive path so non-interactive commands
			// and tests never trigger a network self-replace.
			maybeAutoUpdate(ctx, application, opts, cmd.ErrOrStderr())
			initialSessionID := resolveInitialSession(ctx, application, opts)
			if err := runTUI(ctx, application, initialSessionID); err != nil {
				return fmt.Errorf("running tui: %w", err)
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "path to user config")
	root.PersistentFlags().BoolVar(&opts.verbose, "verbose", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&opts.yolo, "yolo", false, "approve tool calls without prompting")
	root.PersistentFlags().StringVar(&opts.projectDir, "project-dir", "", "project directory")
	root.PersistentFlags().BoolVar(&opts.offline, "offline", false, "offline mode: reject non-localhost providers and disable web tools (code will not leave this machine)")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "", "name of a config profile overlay to apply (looks for <name>.json alongside config.json)")
	root.Flags().BoolVarP(&opts.continueSession, "continue", "c", false, "continue the most recent session for this project")
	root.SetContext(withRootOptions(context.Background(), opts))

	root.AddCommand(
		newInitCmd(),
		newRunCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newAuthCmd(),
		newModelsCmd(),
		newSessionsCmd(),
		newRevertCmd(),
		newShareCmd(),
		newImportHistoryCmd(),
		newStatsCmd(),
		newBudgetCmd(),
		newUpdateProvidersCmd(),
		newUpdateCmd(),
		newConfigCmd(),
		newTelemetryCmd(),
		newVersionCmd(),
		NewDoctorCmd(),
		newSkillsCmd(),
		newRecipesCmd(),
		newAuditCmd(),
		newEvalCmd(),
		newCompletionCmd(),
	)
	return root
}

// resolveInitialSession returns the ID of the most recently updated session for
// the current project when --continue / -c is set, or "" otherwise. A failure
// to list sessions is silently ignored; the TUI starts fresh in that case.
func resolveInitialSession(ctx context.Context, application *app.App, opts *rootOptions) string {
	if !opts.continueSession || application == nil || application.Sessions == nil {
		return ""
	}
	projectPath := opts.projectDir
	if projectPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectPath = cwd
		}
	}
	sessions, err := application.Sessions.List(ctx, session.ListFilter{
		ProjectPath: projectPath,
		Limit:       1,
	})
	if err != nil || len(sessions) == 0 {
		return ""
	}
	return sessions[0].ID
}

func buildApp(ctx context.Context, opts *rootOptions) (*app.App, error) {
	// Wire the OS keyring into the llm package so 'bharatcode login' tokens are
	// consulted when a provider's env var is not set, and so the TUI's first-run
	// onboarding can persist a token through the same backend key resolution reads.
	llm.SetKeyringReader(keyring)
	llm.SetKeyringWriter(keyring)

	application, err := newApp(ctx, app.Options{
		ConfigPath: opts.configPath,
		ProjectDir: opts.projectDir,
		YOLO:       opts.yolo,
		Verbose:    opts.verbose,
		Offline:    opts.offline,
		Profile:    opts.profile,
	})
	if err != nil {
		return nil, fmt.Errorf("constructing app: %w", err)
	}
	return application, nil
}

func closeApp(ctx context.Context, application *app.App) {
	if application == nil {
		return
	}
	_ = application.Close(ctx)
}

// maybeAutoUpdate performs the opt-in startup self-update. It is intentionally
// conservative and silent unless something happens: it returns immediately
// unless every guard passes — config Options.AutoUpdate is set, the process is
// not offline, the build carries a real stamped version/commit (so a dev or
// `go install` build never nags or mutates itself), and stdout is an
// interactive terminal (so pipes, CI, and tests never trigger a network
// self-replace). When all hold it runs a time-bounded check and, if a newer
// release exists, downloads and installs it in place, printing a single-line
// notice. The new binary takes effect on the next launch; the running process
// is deliberately not re-executed. Every failure is a non-fatal warning that
// never blocks startup.
// maybeAutoUpdate runs a best-effort startup update check. By default it only
// NOTIFIES: if a newer release tag exists it prints a one-line, install-method-
// aware hint (npm/brew/binary) and does nothing else. In-place self-replacement
// is opt-in (Options.AutoUpdate) AND limited to binary installs, since npm- and
// Homebrew-managed binaries would be clobbered by the package manager and must
// be upgraded through it. Any network error or uncertainty stays silent.
func maybeAutoUpdate(ctx context.Context, application *app.App, opts *rootOptions, warn io.Writer) {
	if application == nil || application.Cfg == nil {
		return
	}
	if (opts != nil && opts.offline) || offline.EnabledFromEnv() {
		return
	}
	// Only a real release build (stamped version + commit) checks for updates;
	// a dev/source build would compare a placeholder and nag spuriously.
	if version == "" || commit == "" || commit == "0000000" {
		return
	}
	if !stdoutIsTerminal() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, startupUpdateTimeout)
	defer cancel()

	status, err := selfupdate.CheckRelease(ctx, selfupdate.DefaultReleaseAPIURL, version)
	if err != nil || !status.UpdateAvailable {
		return
	}

	method := selfupdate.DetectInstallMethod()

	// Opt-in self-replace, but only where it is safe to overwrite our own
	// executable. Everywhere else (and by default) we fall through to notify.
	if application.Cfg.Options.AutoUpdate && method == selfupdate.InstallBinary {
		if err := selfupdate.Apply(ctx, selfupdate.ApplyOptions{}); err != nil {
			_, _ = fmt.Fprintf(warn, "bharatcode: auto-update skipped: %v\n", err)
			return
		}
		_, _ = fmt.Fprintln(warn, "bharatcode: updated to the latest release; restart to use it.")
		return
	}

	// Default path: tell the user how to update for their install method.
	_, _ = fmt.Fprintf(warn, "bharatcode: %s\n", status.AdviceFor(method))
}

// stdoutIsTerminal reports whether standard output is an interactive character
// device. It is the guard that keeps the startup self-update off the network in
// non-interactive contexts (pipes, redirects, CI, unit tests).
func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
