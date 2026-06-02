package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "logout <provider>",
		Short:   "Remove a provider token",
		Example: "  bharatcode logout openai",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := args[0]
			if err := keyring.Delete(keyringService, provider); err != nil {
				return fmt.Errorf("keyring unavailable: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s\n", provider)
			return nil
		},
	}
}
