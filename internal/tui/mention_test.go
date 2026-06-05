package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile (defined in filetree_test.go) creates a file under root with the
// given relative path and content; mention tests reuse it.

func TestExpandFileMentions_InlinesResolvedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")

	out, refs := expandFileMentions("look at @main.go please", root)

	require.Equal(t, []string{"main.go"}, refs)
	require.Contains(t, out, "look at @main.go please")
	require.Contains(t, out, "[Attached files]")
	require.Contains(t, out, "@main.go:")
	require.Contains(t, out, "```go")
	require.Contains(t, out, "func main() {}")
}

func TestExpandFileMentions_NoMentionLeavesTextUnchanged(t *testing.T) {
	root := t.TempDir()
	out, refs := expandFileMentions("just a plain message", root)
	require.Equal(t, "just a plain message", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_IgnoresEmailAddresses(t *testing.T) {
	root := t.TempDir()
	// An email exists as a file would-be name, but the "@" is mid-token so it is
	// never treated as a mention.
	out, refs := expandFileMentions("ping arbaz@lineupx.com about it", root)
	require.Equal(t, "ping arbaz@lineupx.com about it", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_UnresolvedMentionLeftIntact(t *testing.T) {
	root := t.TempDir()
	out, refs := expandFileMentions("see @does/not/exist.go", root)
	require.Equal(t, "see @does/not/exist.go", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_StripsTrailingPunctuation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/util.go", "package pkg\n")

	out, refs := expandFileMentions("check (@pkg/util.go).", root)
	require.Equal(t, []string{"pkg/util.go"}, refs)
	require.Contains(t, out, "@pkg/util.go:")
}

func TestExpandFileMentions_DeduplicatesRepeatedMention(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	out, refs := expandFileMentions("@a.go and again @a.go", root)
	require.Equal(t, []string{"a.go"}, refs)
	require.Equal(t, 1, strings.Count(out, "@a.go:"))
}

func TestExpandFileMentions_RejectsPathEscapingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(root, 0o755))
	// A sensitive file outside the workspace must never be inlined.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("top secret"), 0o644))

	out, refs := expandFileMentions("read @../secret.txt", root)
	require.Nil(t, refs)
	require.NotContains(t, out, "top secret")
}

func TestExpandFileMentions_RejectsDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal"), 0o755))

	out, refs := expandFileMentions("look in @internal", root)
	require.Nil(t, refs)
	require.Equal(t, "look in @internal", out)
}

func TestExpandFileMentions_TruncatesOversizedFile(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", maxMentionFileBytes+500)
	writeFile(t, root, "big.txt", big)

	out, refs := expandFileMentions("@big.txt", root)
	require.Equal(t, []string{"big.txt"}, refs)
	require.Contains(t, out, "… [truncated]")
	// The inlined body is capped, not the full file.
	require.Less(t, len(out), maxMentionFileBytes+400)
}

func TestExpandFileMentions_MultipleFilesInOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "first.go", "package first\n")
	writeFile(t, root, "second.go", "package second\n")

	out, refs := expandFileMentions("@second.go then @first.go", root)
	require.Equal(t, []string{"second.go", "first.go"}, refs)
	// Order of appended blocks follows first-mention order.
	require.Less(t, strings.Index(out, "@second.go:"), strings.Index(out, "@first.go:"))
}

func TestExpandFileMentions_EmptyRootNoOp(t *testing.T) {
	out, refs := expandFileMentions("@main.go", "")
	require.Equal(t, "@main.go", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_MentionAtStart(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "start.go", "package start\n")

	out, refs := expandFileMentions("@start.go is the entrypoint", root)
	require.Equal(t, []string{"start.go"}, refs)
	require.Contains(t, out, "package start")
}
