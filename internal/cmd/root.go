// Package cmd is the Cobra command tree for the bharatcode binary.
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	configPath      string
	verbose         bool
	yolo            bool
	projectDir      string
	offline         bool
	continueSession bool
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
	root.Flags().BoolVarP(&opts.continueSession, "continue", "c", false, "continue the most recent session for this project")
	root.SetContext(withRootOptions(context.Background(), opts))

	root.AddCommand(
		newInitCmd(),
		newRunCmd(),
		newLoginCmd(),
		newLogoutCmd(),
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
	application, err := newApp(ctx, app.Options{
		ConfigPath: opts.configPath,
		ProjectDir: opts.projectDir,
		YOLO:       opts.yolo,
		Verbose:    opts.verbose,
		Offline:    opts.offline,
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
