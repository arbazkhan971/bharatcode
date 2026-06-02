package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// configLockPollInterval is how long acquireConfigLock waits between
// attempts to create the lockfile while another holder owns it.
const configLockPollInterval = 50 * time.Millisecond

// acquireConfigLock takes an advisory lock for the config file at path
// using a sibling lockfile (path + ".lock"). The lockfile is created
// with O_CREATE|O_EXCL so that exactly one acquirer succeeds at a time;
// this excludes both other goroutines in this process and other
// processes, preventing two concurrent "config edit" sessions from
// interleaving and clobbering each other's changes.
//
// It blocks until the lock is acquired, ctx is cancelled, or ctx's
// deadline passes, retrying every configLockPollInterval. The returned
// release function removes the lockfile and must be called exactly once
// when the caller is done; releasing more than once is a no-op error.
//
// ctx should carry an acquisition timeout only. Callers must not derive
// long-running work (such as running an editor) from the same deadline,
// or that work would be cut short when the acquisition window elapses.
func acquireConfigLock(ctx context.Context, path string) (release func() error, err error) {
	lockPath := path + ".lock"
	for {
		f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			// We own the lock. Record the pid for debuggability; the
			// contents are advisory only and never parsed back.
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return newConfigLockRelease(lockPath), nil
		}
		if !errors.Is(openErr, fs.ErrExist) {
			return nil, fmt.Errorf("acquiring config lock %s: %w", lockPath, openErr)
		}

		// Another holder owns the lock; wait and retry until our
		// acquisition window closes.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquiring config lock %s: %w", lockPath, ctx.Err())
		case <-time.After(configLockPollInterval):
		}
	}
}

// newConfigLockRelease returns a release function that removes lockPath
// exactly once. Subsequent calls return an error so double-release is
// detectable rather than silently masking a logic bug.
func newConfigLockRelease(lockPath string) func() error {
	released := false
	return func() error {
		if released {
			return fmt.Errorf("config lock %s already released", lockPath)
		}
		released = true
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("releasing config lock %s: %w", lockPath, err)
		}
		return nil
	}
}
