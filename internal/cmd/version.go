package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version and commit identify the build. They are deliberately set to dev
// placeholders here and overridden at link time via -ldflags -X (see the
// Makefile and .goreleaser.yaml). An unstamped local build (`go build`/`go
// install` without the ldflags) therefore reports the dev sentinel "v0.0.0"
// rather than a misleading real release number; selfupdate.CompareVersions
// recognizes "v0.0.0" as the dev placeholder and never nags such a build.
var (
	version = "v0.0.0"
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
