package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
	"github.com/spf13/cobra"
)

// configEditLockTimeout bounds how long "config edit" waits to acquire
// the advisory lock before reporting that another edit is in progress.
// It applies to lock acquisition only, never to the editor session.
const configEditLockTimeout = 10 * time.Second

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

			// Hold an advisory lock across the whole read-modify-write
			// (default-create plus the editor session) so two concurrent
			// "config edit" runs can't open the same file, edit in
			// parallel, and clobber each other's changes on save. The
			// timeout bounds acquisition only; the editor itself runs on
			// the unbounded command context below.
			acquireCtx, cancel := context.WithTimeout(cmd.Context(), configEditLockTimeout)
			release, err := acquireConfigLock(acquireCtx, path)
			cancel()
			if err != nil {
				return fmt.Errorf("locking config for edit: %w", err)
			}
			defer func() { _ = release() }()

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
