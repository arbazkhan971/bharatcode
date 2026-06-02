package fsext

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExistsIsDirIsFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup a file and a directory.
	filePath := filepath.Join(tmpDir, "testfile.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	dirPath := filepath.Join(tmpDir, "testdir")
	err = os.Mkdir(dirPath, 0o755)
	require.NoError(t, err)

	nonExistentPath := filepath.Join(tmpDir, "nonexistent")

	// Test Exists.
	require.True(t, Exists(filePath))
	require.True(t, Exists(dirPath))
	require.False(t, Exists(nonExistentPath))

	// Test IsDir.
	require.False(t, IsDir(filePath))
	require.True(t, IsDir(dirPath))
	require.False(t, IsDir(nonExistentPath))

	// Test IsFile.
	require.True(t, IsFile(filePath))
	require.False(t, IsFile(dirPath))
	require.False(t, IsFile(nonExistentPath))
}

func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create a new directory.
	newDir := filepath.Join(tmpDir, "newdir", "nested")
	err := EnsureDir(newDir, 0o755)
	require.NoError(t, err)
	require.True(t, IsDir(newDir))

	// 2. Ensure existing directory returns nil.
	err = EnsureDir(newDir, 0o755)
	require.NoError(t, err)

	// 3. Ensure path that is a file returns error.
	filePath := filepath.Join(tmpDir, "file.txt")
	err = os.WriteFile(filePath, []byte("test"), 0o644)
	require.NoError(t, err)

	err = EnsureDir(filePath, 0o755)
	require.Error(t, err)
	require.Contains(t, err.Error(), "path exists and is not a directory")

	// 4. Test MkdirAll failure (by trying to create a directory under a file).
	failDir := filepath.Join(filePath, "subdir")
	err = EnsureDir(failDir, 0o755)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating directory hierarchy")
}

func TestAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Write a new file.
	filePath := filepath.Join(tmpDir, "atomic.txt")
	data := []byte("atomic data")
	err := AtomicWrite(filePath, data, 0o644)
	require.NoError(t, err)
	require.True(t, IsFile(filePath))

	readData, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, data, readData)

	// Check file mode (on Windows, permission bits are limited, so we skip strict check or mask it).
	fi, err := os.Stat(filePath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, fs.FileMode(0o644), fi.Mode().Perm())
	}

	// 2. Overwrite the existing file.
	newData := []byte("new atomic data")
	err = AtomicWrite(filePath, newData, 0o600)
	require.NoError(t, err)

	readData, err = os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, newData, readData)

	fi, err = os.Stat(filePath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, fs.FileMode(0o600), fi.Mode().Perm())
	}

	// 3. Test failure: parent directory does not exist.
	badPath := filepath.Join(tmpDir, "nonexistent-dir", "file.txt")
	err = AtomicWrite(badPath, []byte("data"), 0o644)
	require.Error(t, err)

	// 4. Test failure during rename (target is a directory).
	dirPath := filepath.Join(tmpDir, "target-dir")
	err = os.Mkdir(dirPath, 0o755)
	require.NoError(t, err)

	// Trying to atomic write to a directory path.
	err = AtomicWrite(dirPath, []byte("data"), 0o644)
	require.Error(t, err)

	// Verify target directory is still a directory.
	require.True(t, IsDir(dirPath))

	// Verify no temp files are left in tmpDir.
	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, f := range files {
		require.False(t, len(f.Name()) >= 4 && f.Name()[:4] == ".tmp", "temp file left over: %s", f.Name())
	}
}

func TestAtomicWriteFailures(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fail.txt")

	// 1. Mock Chmod failure
	oldChmod := chmod
	chmod = func(f *os.File, mode fs.FileMode) error {
		return errors.New("mocked chmod error")
	}
	err := AtomicWrite(filePath, []byte("data"), 0o644)
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting temp file permissions")
	chmod = oldChmod

	// 2. Mock Write failure
	oldWriteFile := writeFile
	writeFile = func(f *os.File, b []byte) (int, error) {
		return 0, errors.New("mocked write error")
	}
	err = AtomicWrite(filePath, []byte("data"), 0o644)
	require.Error(t, err)
	require.Contains(t, err.Error(), "writing data to temp file")
	writeFile = oldWriteFile

	// 3. Mock Sync failure
	oldSyncFile := syncFile
	syncFile = func(f *os.File) error {
		return errors.New("mocked sync error")
	}
	err = AtomicWrite(filePath, []byte("data"), 0o644)
	require.Error(t, err)
	require.Contains(t, err.Error(), "syncing temp file")
	syncFile = oldSyncFile

	// 4. Mock Close failure
	oldCloseFile := closeFile
	closeFile = func(f *os.File) error {
		_ = f.Close()
		return errors.New("mocked close error")
	}
	err = AtomicWrite(filePath, []byte("data"), 0o644)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closing temp file")
	closeFile = oldCloseFile
}
