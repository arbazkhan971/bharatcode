package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect saved sessions",
	}
	cmd.AddCommand(newSessionsListCmd(), newSessionsShowCmd(), newSessionsDeleteCmd())
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sessions for this project",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			application, err := buildApp(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)
			projectPath := opts.projectDir
			if projectPath == "" {
				if cwd, err := os.Getwd(); err == nil {
					projectPath = cwd
				}
			}
			sessions, err := application.Sessions.List(cmd.Context(), session.ListFilter{ProjectPath: projectPath})
			if err != nil {
				return fmt.Errorf("listing sessions: %w", err)
			}
			if len(sessions) == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "no sessions")
				return nil
			}
			rows := [][]string{{"ID", "TITLE", "UPDATED", "MESSAGES", "COST(₹)"}}
			for _, s := range sessions {
				sum, _ := application.Ledger.Summary(cmd.Context(), s.ID, "session")
				rows = append(rows, []string{
					s.ID,
					s.Title,
					s.UpdatedAt.Format("2006-01-02 15:04"),
					fmt.Sprintf("%d", s.MessageCount),
					formatRupees(sum.CostINR),
				})
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), renderTable(rows))
			return nil
		},
	}
}

func newSessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a session transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := buildApp(cmd.Context(), getRootOptions(cmd))
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)
			id := args[0]
			if _, err := application.Sessions.Get(cmd.Context(), id); err != nil {
				if errors.Is(err, session.ErrNotFound) {
					return fmt.Errorf("session %s not found", id)
				}
				return fmt.Errorf("getting session: %w", err)
			}
			messages, err := application.Sessions.Messages(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("loading transcript: %w", err)
			}
			for _, msg := range messages {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", msg.Role, messageText(msg))
			}
			return nil
		},
	}
}

func newSessionsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := buildApp(cmd.Context(), getRootOptions(cmd))
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)
			id := args[0]
			if _, err := application.Sessions.Get(cmd.Context(), id); err != nil {
				if errors.Is(err, session.ErrNotFound) {
					return fmt.Errorf("session %s not found", id)
				}
				return fmt.Errorf("getting session: %w", err)
			}
			if err := application.Sessions.Delete(cmd.Context(), id); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted session %s\n", id)
			return nil
		},
	}
}

func messageText(msg message.Message) string {
	var parts []string
	for _, block := range msg.Content {
		switch b := block.(type) {
		case message.TextBlock:
			parts = append(parts, b.Text)
		case *message.TextBlock:
			parts = append(parts, b.Text)
		case message.ToolResultBlock:
			parts = append(parts, b.Content)
		case *message.ToolResultBlock:
			parts = append(parts, b.Content)
		}
	}
	return strings.Join(parts, "")
}
