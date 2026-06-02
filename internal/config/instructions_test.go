package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadInstructionsNestedMergeRootFirst(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	rootContent := "ROOT-RULE: always use gofumpt."
	nestedContent := "NESTED-RULE: this package forbids panics."
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rootContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte(nestedContent), 0o644))

	// No global file: pass empty global path.
	out, err := loadInstructionsFrom(ctx, "", root, sub, InstructionBudget)
	require.NoError(t, err)

	// Both files' content must be present.
	require.Contains(t, out, rootContent)
	require.Contains(t, out, nestedContent)

	// Root content must appear BEFORE nested content (override order).
	rootIdx := strings.Index(out, rootContent)
	nestedIdx := strings.Index(out, nestedContent)
	require.GreaterOrEqual(t, rootIdx, 0)
	require.GreaterOrEqual(t, nestedIdx, 0)
	require.Less(t, rootIdx, nestedIdx, "root instructions must precede nested instructions")
}

func TestLoadInstructionsGlobalFirst(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	globalContent := "GLOBAL-RULE: prefer Indian Rupee."
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(globalPath, []byte(globalContent), 0o644))

	rootContent := "ROOT-RULE: repo specific."
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rootContent), 0o644))

	out, err := loadInstructionsFrom(ctx, globalPath, root, root, InstructionBudget)
	require.NoError(t, err)

	require.Contains(t, out, globalContent)
	require.Contains(t, out, rootContent)
	// Global precedes repo-root content.
	require.Less(t, strings.Index(out, globalContent), strings.Index(out, rootContent))
}

func TestLoadInstructionsByteCapTruncates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// Write a file larger than the budget.
	budget := 1024
	big := strings.Repeat("A", budget*2)
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(big), 0o644))

	out, err := loadInstructionsFrom(ctx, "", root, root, budget)
	require.NoError(t, err)

	require.Contains(t, out, InstructionTruncatedMarker)
	// Total output (content prefix plus marker) must not exceed budget.
	require.LessOrEqual(t, len(out), budget)
	// The retained prefix is the leading bytes of the original content,
	// sized so that prefix plus marker fits the budget exactly.
	keep := budget - len(InstructionTruncatedMarker)
	require.Equal(t, strings.Repeat("A", keep), strings.TrimSuffix(out, InstructionTruncatedMarker))
}

func TestLoadInstructionsClaudeMdFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "service")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// Root has AGENTS.md; subdir has only CLAUDE.md (fallback).
	rootContent := "ROOT via AGENTS.md."
	claudeContent := "CLAUDE-FALLBACK: service rules here."
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rootContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte(claudeContent), 0o644))

	out, err := loadInstructionsFrom(ctx, "", root, sub, InstructionBudget)
	require.NoError(t, err)

	require.Contains(t, out, rootContent)
	require.Contains(t, out, claudeContent)
	require.Less(t, strings.Index(out, rootContent), strings.Index(out, claudeContent))
}

func TestLoadInstructionsAgentsMdPreferredOverClaudeMd(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	agents := "AGENTS-WINS: this is the canonical file."
	claude := "CLAUDE-IGNORED: should not appear."
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(agents), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(claude), 0o644))

	out, err := loadInstructionsFrom(ctx, "", root, root, InstructionBudget)
	require.NoError(t, err)

	require.Contains(t, out, agents)
	require.NotContains(t, out, claude)
}

func TestLoadInstructionsNoneFoundReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	out, err := loadInstructionsFrom(ctx, "", root, root, InstructionBudget)
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func TestLoadInstructionsCwdOutsideRootUsesCwdOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	other := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("ROOT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(other, "AGENTS.md"), []byte("OTHER"), 0o644))

	// cwd (other) is not under root; only cwd's file should be used.
	out, err := loadInstructionsFrom(ctx, "", root, other, InstructionBudget)
	require.NoError(t, err)
	require.Equal(t, "OTHER", out)
	require.NotContains(t, out, "ROOT")
}
