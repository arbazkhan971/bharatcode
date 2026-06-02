package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstructionBudget caps the total size in bytes of concatenated
// project instructions. Content beyond this budget is truncated and
// flagged with InstructionTruncatedMarker.
const InstructionBudget = 32 * 1024

// InstructionTruncatedMarker is appended when the concatenated
// instructions exceed InstructionBudget and are truncated.
const InstructionTruncatedMarker = "\n\n[... instructions truncated: byte budget exceeded ...]"

// instructionFilenames lists the candidate filenames, in priority
// order, that hold project instructions in a directory. AGENTS.md is
// preferred; CLAUDE.md is accepted as a fallback when AGENTS.md is
// absent in the same directory.
var instructionFilenames = []string{"AGENTS.md", "CLAUDE.md"}

// LoadInstructions loads project instructions for the current working
// directory. It reads the global instructions file (if present) under
// the global config directory, then every per-directory instructions
// file from the repository root down to the working directory,
// concatenated root-first so that deeper (more specific) files appear
// last and thus override shallower ones. The total size is capped at
// InstructionBudget. It returns an empty string when no instructions
// are found.
func LoadInstructions(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	globalAgents := filepath.Join(filepath.Dir(GlobalPath()), "AGENTS.md")
	root := repoRoot(cwd)
	return loadInstructionsFrom(ctx, globalAgents, root, cwd, InstructionBudget)
}

// repoRoot walks up from dir looking for a directory that contains a
// .git entry and returns it. When no such directory exists, it
// returns dir unchanged so that only the working directory's own
// instructions file is considered.
func repoRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	current := abs
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return abs
}

// loadInstructionsFrom is the testable core of LoadInstructions. It
// reads globalAgentsPath (if non-empty and present), then each
// instructions file in the chain of directories from root down to
// cwd, concatenating them root-first. The result is truncated to
// budget bytes when it would otherwise exceed it.
func loadInstructionsFrom(ctx context.Context, globalAgentsPath, root, cwd string, budget int) (string, error) {
	_ = ctx

	var sections []string

	if globalAgentsPath != "" {
		if content, ok, err := readInstructionFile(globalAgentsPath); err != nil {
			return "", err
		} else if ok {
			sections = append(sections, content)
		}
	}

	dirs, err := instructionDirChain(root, cwd)
	if err != nil {
		return "", err
	}
	for _, dir := range dirs {
		content, ok, err := readInstructionDir(dir)
		if err != nil {
			return "", err
		}
		if ok {
			sections = append(sections, content)
		}
	}

	joined := strings.Join(sections, "\n\n")
	if len(joined) > budget {
		// Truncate so the total (content prefix plus marker) stays
		// within budget bytes.
		keep := budget - len(InstructionTruncatedMarker)
		if keep < 0 {
			keep = 0
		}
		joined = joined[:keep] + InstructionTruncatedMarker
	}
	return joined, nil
}

// instructionDirChain returns the directories from root down to cwd
// inclusive, ordered root-first. When cwd is not within root, it
// returns only cwd so that the caller still picks up the working
// directory's own instructions.
func instructionDirChain(root, cwd string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root directory %s: %w", root, err)
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolving working directory %s: %w", cwd, err)
	}

	rel, err := filepath.Rel(absRoot, absCwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{absCwd}, nil
	}

	chain := []string{absCwd}
	current := absCwd
	for current != absRoot {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
		chain = append(chain, current)
	}

	// Reverse to root-first order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// readInstructionDir reads the first present instructions file in dir,
// trying instructionFilenames in priority order. It reports whether a
// file was found.
func readInstructionDir(dir string) (string, bool, error) {
	for _, name := range instructionFilenames {
		content, ok, err := readInstructionFile(filepath.Join(dir, name))
		if err != nil {
			return "", false, err
		}
		if ok {
			return content, true, nil
		}
	}
	return "", false, nil
}

// readInstructionFile reads path, returning its trimmed content and
// true when the file exists and is non-empty. A missing file is not an
// error: ok is false and err is nil.
func readInstructionFile(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading instructions file %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", false, nil
	}
	return trimmed, true, nil
}
