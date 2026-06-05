package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadInstructionsExpandsImport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "AGENTS.md"),
		[]byte("Project rules.\nSee @docs/style.md for details."),
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "docs", "style.md"),
		[]byte("STYLE: tabs not spaces."),
		0o644,
	))

	out, err := loadInstructionsFrom(ctx, "", root, root, InstructionBudget)
	require.NoError(t, err)

	require.Contains(t, out, "Project rules.")
	require.Contains(t, out, "STYLE: tabs not spaces.")
	// The literal @path reference must be replaced, not left behind.
	require.NotContains(t, out, "@docs/style.md")
}

func TestImportResolvesRelativeToImportingFile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// The reference is relative to pkg/, not the workspace root.
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "AGENTS.md"),
		[]byte("@notes.md"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "notes.md"),
		[]byte("LOCAL NOTE"),
		0o644,
	))

	content, ok, err := readInstructionFile(filepath.Join(sub, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "LOCAL NOTE", content)
}

func TestImportIsRecursive(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("@a.md"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("A then @b.md"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.md"), []byte("B-LEAF"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, content, "A then")
	require.Contains(t, content, "B-LEAF")
	require.NotContains(t, content, "@b.md")
}

func TestImportCycleLeavesReferenceLiteral(t *testing.T) {
	root := t.TempDir()
	// a -> b -> a forms a cycle; expansion must terminate.
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("start @a.md"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("A @b.md"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.md"), []byte("B @a.md"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, content, "A ")
	require.Contains(t, content, "B ")
	// The back-reference into the cycle is left as a literal, not expanded
	// again, so the result is finite.
	require.Contains(t, content, "@a.md")
}

func TestImportSelfReferenceLeavesLiteral(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("hello @AGENTS.md"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello @AGENTS.md", content)
}

func TestImportMissingFileLeavesLiteral(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("see @does/not/exist.md"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "see @does/not/exist.md", content)
}

func TestImportIgnoredInsideCodeFenceAndSpan(t *testing.T) {
	root := t.TempDir()
	body := strings.Join([]string{
		"Real: @real.md",
		"`@span.md` should be ignored.",
		"```",
		"@fenced.md should be ignored.",
		"```",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(body), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.md"), []byte("REAL-IMPORT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "span.md"), []byte("SPAN-IMPORT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "fenced.md"), []byte("FENCED-IMPORT"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, content, "REAL-IMPORT")
	require.NotContains(t, content, "SPAN-IMPORT")
	require.NotContains(t, content, "FENCED-IMPORT")
	// The code-span and fenced references survive verbatim.
	require.Contains(t, content, "`@span.md`")
	require.Contains(t, content, "@fenced.md should be ignored.")
}

func TestImportNotTriggeredByEmailAddress(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "AGENTS.md"),
		[]byte("Contact dev@example.com for access."),
		0o644,
	))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Contact dev@example.com for access.", content)
}

func TestImportTrailingPunctuationPreserved(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "AGENTS.md"),
		[]byte("Read @guide.md."),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(root, "guide.md"), []byte("GUIDE"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Read GUIDE.", content)
}

func TestImportDepthLimit(t *testing.T) {
	root := t.TempDir()
	// Build a chain a0 -> a1 -> ... -> a7 longer than the depth limit.
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("@a0.md"), 0o644))
	for i := 0; i < 7; i++ {
		body := "L" + string(rune('0'+i)) + " @a" + string(rune('0'+i+1)) + ".md"
		require.NoError(t, os.WriteFile(filepath.Join(root, "a"+string(rune('0'+i))+".md"), []byte(body), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "a7.md"), []byte("DEEP-LEAF"), 0o644))

	content, ok, err := readInstructionFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, ok)
	// Early levels expand; the chain is cut off before the deepest leaf,
	// leaving an unexpanded @ reference rather than recursing without bound.
	require.Contains(t, content, "L0")
	require.NotContains(t, content, "DEEP-LEAF")
	require.Contains(t, content, ".md")
}
