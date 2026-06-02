// Package fsext provides filesystem helpers.
// It depends only on the Go standard library.
package fsext

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Internal helpers to allow mocking in tests.
var (
	createTemp = os.CreateTemp
	chmod      = func(f *os.File, mode fs.FileMode) error { return f.Chmod(mode) }
	writeFile  = func(f *os.File, b []byte) (int, error) { return f.Write(b) }
	syncFile   = func(f *os.File) error { return f.Sync() }
	closeFile  = func(f *os.File) error { return f.Close() }
)

// Exists reports whether path resolves to an existing filesystem
// entry. It returns false on any stat error, including permission
// denied; callers needing finer error handling should use os.Stat
// directly.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether path resolves to a directory. Returns false
// for any stat error, including non-existence.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

// IsFile reports whether path resolves to a regular file. Returns
// false for any stat error, including non-existence.
func IsFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
}

// EnsureDir creates path and any required parent directories with
// the given mode. It is a thin wrapper around os.MkdirAll that
// returns nil when path already exists as a directory and an error
// when path exists as a non-directory.
func EnsureDir(path string, perm fs.FileMode) error {
	fi, err := os.Stat(path)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		// Return an error when it exists as a non-directory.
		return fmt.Errorf("path exists and is not a directory: %s", path)
	}

	// Call os.MkdirAll, which will fail if a parent is not a directory.
	if err = os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("creating directory hierarchy: %w", err)
	}
	return nil
}

// AtomicWrite writes data to path by first creating a temporary
// file in the same directory, syncing it, and then renaming it
// over the target. The temp file is removed on any failure. perm
// is applied to the temp file before rename so the final file has
// the requested mode atomically. The parent directory must already
// exist; callers should pair AtomicWrite with EnsureDir as needed.
func AtomicWrite(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmpFile, err := createTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err = chmod(tmpFile, perm); err != nil {
		return fmt.Errorf("setting temp file permissions: %w", err)
	}

	if _, err = writeFile(tmpFile, data); err != nil {
		return fmt.Errorf("writing data to temp file: %w", err)
	}

	if err = syncFile(tmpFile); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err = closeFile(tmpFile); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err = renameFile(tmpName, path); err != nil {
		return fmt.Errorf("renaming temp file to target: %w", err)
	}

	success = true
	return nil
}
