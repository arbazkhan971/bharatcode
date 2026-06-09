package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/identity"
	"github.com/spf13/cobra"
)

func newAboutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: "Print what BharatCode is",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			_, err := fmt.Fprintln(cmd.OutOrStdout(), identity.ShortAnswer)
			return err
		},
	}
}
