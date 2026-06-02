//go:build windows

package fsext

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// renameFile renames oldPath to newPath. On Windows, we handle the case
// where the target exists by removing it first (if it is not a directory).
func renameFile(oldPath, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if err == nil {
		return nil
	}

	if isExistError(err) {
		// Check if target is a directory. If so, fail.
		if fi, statErr := os.Stat(newPath); statErr == nil && fi.IsDir() {
			return fmt.Errorf("target path is a directory: %w", err)
		}

		// Race window: between the Remove and the Rename, there
		// is a brief window where the target file does not exist, and
		// another process might create it, causing the rename to fail again.
		if remErr := os.Remove(newPath); remErr != nil && !os.IsNotExist(remErr) {
			return fmt.Errorf("removing existing target file: %w", remErr)
		}
		if renErr := os.Rename(oldPath, newPath); renErr != nil {
			return fmt.Errorf("renaming temp file to target: %w", renErr)
		}
		return nil
	}
	return err
}

// isExistError reports whether the error indicates a file already exists.
func isExistError(err error) bool {
	if errors.Is(err, fs.ErrExist) {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "file exists") || strings.Contains(errStr, "already exists")
}
