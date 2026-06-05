package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/audit"
	"github.com/spf13/cobra"
)

// newAuditCmd builds the "audit" command group for inspecting the append-only
// audit log. The log records one immutable, hash-chained entry per significant
// event (LLM call, tool run, file write, permission decision), letting a user
// prove exactly what the agent did and that the record was not altered.
func newAuditCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the append-only audit log",
		Long: "Inspect BharatCode's tamper-evident audit log. Each event is " +
			"stored as one immutable, hash-chained record, so the trail can be " +
			"exported for archival and verified for tampering.",
	}
	cmd.PersistentFlags().StringVar(&path, "path", "", "audit log path (defaults to the standard data directory)")

	cmd.AddCommand(newAuditExportCmd(&path), newAuditVerifyCmd(&path))
	return cmd
}

func auditPath(flag string) string {
	if flag != "" {
		return flag
	}
	return audit.DefaultPath()
}

func newAuditExportCmd(path *string) *cobra.Command {
	return &cobra.Command{
		Use:     "export",
		Short:   "Export the audit log as JSON Lines",
		Example: "  bharatcode audit export > audit.jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			store, err := audit.Open(cmd.Context(), auditPath(*path))
			if err != nil {
				return fmt.Errorf("opening audit log: %w", err)
			}
			defer store.Close()
			if err := store.Export(cmd.Context(), cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("exporting audit log: %w", err)
			}
			return nil
		},
	}
}

func newAuditVerifyCmd(path *string) *cobra.Command {
	return &cobra.Command{
		Use:     "verify",
		Short:   "Verify the audit log hash chain for tampering",
		Example: "  bharatcode audit verify",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			store, err := audit.Open(cmd.Context(), auditPath(*path))
			if err != nil {
				return fmt.Errorf("opening audit log: %w", err)
			}
			defer store.Close()
			n, err := store.Verify(cmd.Context())
			if err != nil {
				return fmt.Errorf("audit log verification failed: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "audit log OK: %d records verified\n", n)
			return nil
		},
	}
}
