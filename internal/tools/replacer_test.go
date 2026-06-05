package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEditFlexibleMatchesIndentationDriftedBlock verifies that a multi-line
// old_string whose indentation differs from the file (tabs on disk, spaces in
// the request) is still applied via the line-trimmed fallback, and that the
// on-disk indentation is preserved by replacing the actual file span.
func TestEditFlexibleMatchesIndentationDriftedBlock(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "code.go")
	// On disk uses a tab + the real body; the request uses 4 spaces.
	onDisk := "func add(a, b int) int {\n\treturn a + b\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(onDisk), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "flex-1"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "code.go",
		"old_string": "func add(a, b int) int {\n    return a + b\n}", // spaces, not tab
		"new_string": "func add(a, b int) int {\n\treturn a + b + 0\n}",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "expected flexible match to succeed: %s", result.Content)
	require.Contains(t, result.Content, "flexible line-trimmed matching")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "func add(a, b int) int {\n\treturn a + b + 0\n}\n", string(got))
}

// TestEditSingleLineWhitespaceStaysStrict verifies that the flexible fallback is
// gated to multi-line blocks: a single-line whitespace mismatch must still fail
// with the not-found hint, preserving BharatCode's strict single-line policy.
func TestEditSingleLineWhitespaceStaysStrict(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "one.txt")
	require.NoError(t, os.WriteFile(path, []byte("    func hello() {}\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "flex-2"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "one.txt",
		"old_string": "\tfunc hello() {}",
		"new_string": "\tfunc namaste() {}",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "old_string was not found")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "    func hello() {}\n", string(got))
}

// TestEditFlexibleAmbiguousIsRejected verifies that when a whitespace-tolerant
// block matches in more than one place the edit is rejected (not silently
// applied to the wrong location) unless replace_all is set.
func TestEditFlexibleAmbiguousIsRejected(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "dup.txt")
	// Two identical two-line blocks differing from the request only by indent.
	onDisk := "\tx = 1\n\ty = 2\nmid\n\tx = 1\n\ty = 2\n"
	require.NoError(t, os.WriteFile(path, []byte(onDisk), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "flex-3"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "dup.txt",
		"old_string": "x = 1\ny = 2", // no indentation
		"new_string": "x = 9\ny = 9",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "Found 2 occurrences")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, onDisk, string(got), "file must be untouched on ambiguous match")
}

// TestEditFlexibleBlockAnchorMatchesDriftedInterior verifies the block-anchor
// strategy: a >=3-line block whose interior line differs is matched on its
// unique first and last trimmed lines.
func TestEditFlexibleBlockAnchorMatchesDriftedInterior(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "block.txt")
	onDisk := "begin marker\n  middle ACTUAL\nend marker\n"
	require.NoError(t, os.WriteFile(path, []byte(onDisk), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "flex-4"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "block.txt",
		"old_string": "begin marker\nmiddle GUESS\nend marker", // interior differs
		"new_string": "begin marker\nreplaced\nend marker",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "expected block-anchor match: %s", result.Content)
	require.Contains(t, result.Content, "flexible block-anchor matching")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "begin marker\nreplaced\nend marker\n", string(got))
}

// TestMultiEditFlexibleMatching verifies the multiedit tool shares the flexible
// fallback and reports how many edits used it.
func TestMultiEditFlexibleMatching(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "multi.txt")
	onDisk := "alpha\n\tindented one\n\tindented two\nomega\n"
	require.NoError(t, os.WriteFile(path, []byte(onDisk), 0o644))

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "flex-5"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "multi.txt",
		"edits": []map[string]any{
			// The request matches on trimmed text; the replacement is applied
			// verbatim, so it carries the file's tab indentation to preserve it.
			{"old": "indented one\nindented two", "new": "\tindented one\n\tindented two and a half"},
		},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "expected flexible multiedit success: %s", result.Content)
	require.Contains(t, result.Content, "flexible whitespace-tolerant matching")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "alpha\n\tindented one\n\tindented two and a half\nomega\n", string(got))
}

// TestFindFlexibleSpansWhitespaceNormalized exercises the internal-spacing
// strategy directly: lines differing only by internal whitespace runs match.
func TestFindFlexibleSpansWhitespaceNormalized(t *testing.T) {
	source := "foo  =   1\nbar = 2\n"
	spans, strategy := findFlexibleSpans(source, "foo = 1\nbar = 2")
	require.Equal(t, "whitespace-normalized", strategy)
	require.Len(t, spans, 1)
	require.Equal(t, "foo  =   1\nbar = 2", source[spans[0].start:spans[0].end])
}
