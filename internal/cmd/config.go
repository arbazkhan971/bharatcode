package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}
	cmd.AddCommand(newConfigEditCmd())
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "edit",
		Short:   "Open user config in an editor",
		Example: "  EDITOR=nvim bharatcode config edit",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			path := opts.configPath
			if path == "" {
				path = config.GlobalPath()
			}
			path = util.ExpandPath(path)
			if err := fsext.EnsureDir(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("ensuring config directory: %w", err)
			}
			if _, err := os.Stat(path); os.IsNotExist(err) {
				cfg := config.Default()
				if err := saveConfigPath(cmd.Context(), path, cfg); err != nil {
					return err
				}
			} else if err != nil {
				return fmt.Errorf("checking config file: %w", err)
			}
			editor := exec.CommandContext(cmd.Context(), defaultEditor(), path)
			editor.Stdin = cmd.InOrStdin()
			editor.Stdout = cmd.OutOrStdout()
			editor.Stderr = cmd.ErrOrStderr()
			if err := editor.Run(); err != nil {
				return fmt.Errorf("running editor: %w", err)
			}
			return nil
		},
	}
}
