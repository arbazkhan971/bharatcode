package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

// newRevertCmd builds the "revert" command, which rolls every file a
// session edited back to its pre-session state using the content
// snapshots the file tracker records on each write.
func newRevertCmd() *cobra.Command {
	var (
		force  bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "revert [session-id]",
		Short: "Undo a session's file changes, restoring files to their pre-session state",
		Long: "Restore every file a session created or edited back to the state it had " +
			"before the session began. Files created during the session are deleted. " +
			"With no session-id, the most recent session for this project is used.\n\n" +
			"Files modified outside the session since it last wrote them are skipped " +
			"to avoid clobbering later changes; pass --force to revert them anyway.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := getRootOptions(cmd)
			application, err := buildApp(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)

			id, err := resolveRevertSession(cmd, application, args)
			if err != nil {
				return err
			}

			outcomes, err := application.FileTracker.RevertSession(cmd.Context(), id, filetracker.RevertOptions{
				Force:  force,
				DryRun: dryRun,
			})
			if err != nil {
				return fmt.Errorf("reverting session %s: %w", id, err)
			}
			return printRevertOutcomes(cmd, id, outcomes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "revert files even if they changed since the session wrote them")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be reverted without changing any files")
	return cmd
}

// resolveRevertSession returns the session id to revert: the explicit
// argument when given, otherwise the most recent session for the
// project. It verifies the session exists.
func resolveRevertSession(cmd *cobra.Command, application *app.App, args []string) (string, error) {
	if len(args) == 1 {
		id := args[0]
		if _, err := application.Sessions.Get(cmd.Context(), id); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				return "", fmt.Errorf("session %s not found", id)
			}
			return "", fmt.Errorf("getting session: %w", err)
		}
		return id, nil
	}

	projectPath := getRootOptions(cmd).projectDir
	if projectPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectPath = cwd
		}
	}
	sessions, err := application.Sessions.List(cmd.Context(), session.ListFilter{ProjectPath: projectPath})
	if err != nil {
		return "", fmt.Errorf("listing sessions: %w", err)
	}
	if len(sessions) == 0 {
		return "", errors.New("no sessions found for this project; pass a session id explicitly")
	}
	return sessions[0].ID, nil // List returns newest first
}

// printRevertOutcomes renders the per-file result of a revert.
func printRevertOutcomes(cmd *cobra.Command, id string, outcomes []filetracker.RevertOutcome, dryRun bool) error {
	out := cmd.OutOrStdout()
	if len(outcomes) == 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "session %s changed no files\n", id)
		return nil
	}
	verb := "Reverted"
	if dryRun {
		verb = "Would revert"
	}
	rows := [][]string{{"ACTION", "PATH", "DETAIL"}}
	var reverted, skipped int
	for _, o := range outcomes {
		switch o.Action {
		case filetracker.RevertSkipped:
			skipped++
		default:
			reverted++
		}
		rows = append(rows, []string{string(o.Action), o.Path, o.Reason})
	}
	_, _ = fmt.Fprint(out, renderTable(rows))
	_, _ = fmt.Fprintf(out, "%s %d file(s) for session %s (%d skipped)\n", verb, reverted, id, skipped)
	return nil
}
