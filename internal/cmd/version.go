package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "v0.2.0"
	commit  = "0000000"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "bharatcode %s (%s)\n", version, commit)
			return nil
		},
	}
}
