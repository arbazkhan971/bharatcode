package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/offline"
	"github.com/arbazkhan971/bharatcode/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCmd builds the "update" subcommand. It checks GitHub for a newer
// BharatCode commit on the default branch and reports whether the running
// binary is behind, along with the command to update a source install. The
// check is a single best-effort HTTP call; it never mutates the install.
//
// Offline/sovereignty mode disables the check entirely: probing GitHub is
// network egress, which offline mode promises will not happen. In that case
// the command says so and exits cleanly.
func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Check whether a newer BharatCode is available",
		Long: "Check the upstream repository for a newer commit than the one this " +
			"binary was built from. This only reports availability and the update " +
			"command; it does not modify the installation. Disabled in offline mode.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			out := cmd.OutOrStdout()

			if (opts != nil && opts.offline) || offline.EnabledFromEnv() {
				_, _ = fmt.Fprintln(out, "Update check skipped: "+offline.Banner)
				return nil
			}

			status, err := selfupdate.CheckWithTimeout(cmd.Context(), selfupdate.DefaultAPIURL, commit)
			if err != nil {
				return fmt.Errorf("update check failed: %w", err)
			}
			if status.UpdateAvailable {
				_, _ = fmt.Fprintln(out, status.Advice())
			} else {
				_, _ = fmt.Fprintf(out, "BharatCode is up to date (%s).\n", status.Current)
			}
			return nil
		},
	}
}
