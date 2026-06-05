package tui

import (
	"path/filepath"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
)

// loadRecipeRegistry builds the recipe registry from the standard global and
// project recipe directories. It mirrors loadPromptRegistry: a load failure
// yields an empty registry so a missing or malformed recipes directory cannot
// block TUI startup.
func loadRecipeRegistry(cfg *config.Config) *recipe.Registry {
	dirs := recipeDirs(cfg)
	reg, err := recipe.NewRegistry(dirs...)
	if err != nil || reg == nil {
		empty, _ := recipe.NewRegistry()
		return empty
	}
	return reg
}

// recipeDirs returns the directories scanned for recipe files. The set is
// derived from the configured data directory (global) plus the process working
// directory's .bharatcode/recipes (project). An empty slice is acceptable and
// yields an empty registry.
func recipeDirs(cfg *config.Config) []string {
	var dirs []string
	if cfg != nil && cfg.Options.DataDir != "" {
		dirs = append(dirs, filepath.Join(cfg.Options.DataDir, "recipes"))
	}
	// Project-local recipes come second so they override global ones on a
	// name collision, matching the recipe.DefaultDirs precedence convention.
	if wd := workingDir(); wd != "" {
		dirs = append(dirs, filepath.Join(wd, ".bharatcode", "recipes"))
	}
	return dirs
}
