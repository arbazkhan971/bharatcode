package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/offline"
	"github.com/arbazkhan971/bharatcode/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCmd builds the "update" subcommand. With no flags it checks GitHub
// for a newer BharatCode commit on the default branch and reports whether the
// running binary is behind, along with the command to update a source install;
// the check is a single best-effort HTTP call that never mutates the install.
//
// With --apply it goes further: when an update is available it downloads the
// latest release binary for this platform, verifies it against the release's
// published SHA-256 checksums, and replaces the running executable in place
// (see selfupdate.Apply). When already up to date it says so and changes
// nothing.
//
// Offline/sovereignty mode disables both paths: probing GitHub or downloading a
// release is network egress, which offline mode promises will not happen. In
// that case the command says so and exits cleanly.
func newUpdateCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer BharatCode, or install it with --apply",
		Long: "Check the upstream repository for a newer commit than the one this " +
			"binary was built from. By default this only reports availability and " +
			"the update command; it does not modify the installation. With --apply " +
			"it downloads the latest release binary, verifies its checksum, and " +
			"replaces the running executable in place. Disabled in offline mode.",
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

			if !apply {
				if status.UpdateAvailable {
					_, _ = fmt.Fprintln(out, status.Advice())
				} else {
					_, _ = fmt.Fprintf(out, "BharatCode is up to date (%s).\n", status.Current)
				}
				return nil
			}

			// --apply: actually install the newer release when behind.
			if !status.UpdateAvailable {
				_, _ = fmt.Fprintf(out, "BharatCode is already up to date (%s); nothing to install.\n", status.Current)
				return nil
			}
			if err := selfupdate.Apply(cmd.Context(), selfupdate.ApplyOptions{Progress: out}); err != nil {
				return fmt.Errorf("self-update failed: %w", err)
			}
			_, _ = fmt.Fprintln(out, "Update installed. Restart bharatcode to run the new version.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "download and install the latest release binary, replacing this executable")
	return cmd
}
