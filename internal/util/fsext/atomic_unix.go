//go:build !windows

package fsext

import "os"

// renameFile renames oldPath to newPath. On non-Windows platforms,
// this is a simple wrapper around os.Rename.
func renameFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
