package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile creates parents and writes a tiny source file at root/rel.
func writeDiagFile(t *testing.T, root, rel string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("package main\n"), 0o644))
	return full
}

// TestDiagnosticFilesSkipsIgnoredDirs asserts the workspace scan descends into
// real source directories but skips the dependency/build directories grep and
// glob already ignore, so the language servers are never asked to analyse
// vendored or generated code.
func TestDiagnosticFilesSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()

	want := writeDiagFile(t, root, "main.go")
	nested := writeDiagFile(t, root, "pkg/util.go")
	// Each of these lives under a directory grep's ignoredDirs already skips.
	writeDiagFile(t, root, "node_modules/dep/index.go")
	writeDiagFile(t, root, "vendor/lib/lib.go")
	writeDiagFile(t, root, "dist/bundle.go")
	writeDiagFile(t, root, ".git/hooks/hook.go")

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{want, nested}, got)
}

// TestDiagnosticFilesHonorsRootGitignore asserts that a directory named in the
// root .gitignore (here Rust's target/, which is not in the built-in set) is
// also skipped, matching grep's loadRootGitignore behaviour.
func TestDiagnosticFilesHonorsRootGitignore(t *testing.T) {
	root := t.TempDir()

	want := writeDiagFile(t, root, "main.go")
	writeDiagFile(t, root, "target/debug/build.go")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("target/\n"), 0o644))

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}

// TestDiagnosticFilesScansRootNamedLikeIgnored guards the path != root exception:
// when the workspace root itself is named like an ignored directory, its files
// are still scanned rather than the whole tree being skipped.
func TestDiagnosticFilesScansRootNamedLikeIgnored(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "node_modules")
	want := writeDiagFile(t, root, "main.go")

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}
