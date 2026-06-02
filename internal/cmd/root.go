// Package cmd is the Cobra command tree for the bharatcode binary.
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/tui"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	configPath string
	verbose    bool
	yolo       bool
	projectDir string
}

var (
	newApp = app.New
	runTUI = func(ctx context.Context, application *app.App) error {
		loop, err := application.Agent.Agent("coder")
		if err != nil {
			return fmt.Errorf("resolving default agent: %w", err)
		}
		return tui.Run(ctx, tui.Dependencies{
			Agent:       loop,
			Sessions:    application.Sessions,
			Cfg:         application.Cfg,
			Bus:         application.Bus.Agent,
			Permission:  application.Permission,
			Ledger:      application.Ledger,
			FileTracker: application.FileTracker,
			Logger:      application.Logger,
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
			if err := runTUI(ctx, application); err != nil {
				return fmt.Errorf("running tui: %w", err)
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "path to user config")
	root.PersistentFlags().BoolVar(&opts.verbose, "verbose", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&opts.yolo, "yolo", false, "approve tool calls without prompting")
	root.PersistentFlags().StringVar(&opts.projectDir, "project-dir", "", "project directory")
	root.SetContext(withRootOptions(context.Background(), opts))

	root.AddCommand(
		newRunCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newModelsCmd(),
		newSessionsCmd(),
		newStatsCmd(),
		newBudgetCmd(),
		newUpdateProvidersCmd(),
		newConfigCmd(),
		newVersionCmd(),
		NewDoctorCmd(),
		newSkillsCmd(),
		newCompletionCmd(),
	)
	return root
}

func buildApp(ctx context.Context, opts *rootOptions) (*app.App, error) {
	application, err := newApp(ctx, app.Options{
		ConfigPath: opts.configPath,
		ProjectDir: opts.projectDir,
		YOLO:       opts.yolo,
		Verbose:    opts.verbose,
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
