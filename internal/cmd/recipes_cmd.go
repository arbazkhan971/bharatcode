package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/spf13/cobra"
)

// newRecipesCmd builds the "recipes" subcommand, which discovers recipes
// in the standard recipe directories and prints each recipe's name, title,
// and description as a table. When no recipes are found it reports the
// directories that were searched so the user knows where to add them.
func newRecipesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recipes",
		Short: "List discovered recipes",
		Long: "Discover recipes in the standard recipe directories (the " +
			"global config directory and the project directory) and " +
			"print each recipe's name, title, and description. When no recipes are " +
			"found, the searched directories are reported.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			dirs := recipeDirs(opts)
			return runRecipes(cmd.Context(), cmd.OutOrStdout(), dirs)
		},
	}
}

// recipeDirs resolves the standard recipe directories in precedence
// order: the global config directory's recipes/ subdirectory, then the
// project's .bharatcode/recipes directory. The project directory is the
// --project-dir value when set, falling back to the current working
// directory so `bharatcode recipes` discovers project recipes from a repo
// root without the flag (matching init and import-history). Paths are
// expanded so ~ and environment references resolve the same way the rest
// of the CLI resolves them.
func recipeDirs(opts *rootOptions) []string {
	project := ""
	if opts != nil {
		project = opts.projectDir
	}
	if project == "" {
		if cwd, err := os.Getwd(); err == nil {
			project = cwd
		}
	}
	dirs := recipe.DefaultDirs(config.GlobalPath(), project)
	expanded := make([]string, len(dirs))
	for i, d := range dirs {
		expanded[i] = util.ExpandPath(d)
	}
	return expanded
}

// runRecipes loads recipes from dirs, creates a registry, and writes the result
// to w. It is split from newRecipesCmd so tests can inject directories and drive
// rendering deterministically. A registry load error is returned wrapped; an
// empty registry prints the "no recipes" message with the searched directories.
func runRecipes(ctx context.Context, w io.Writer, dirs []string) error {
	_ = ctx // recipes discovery is synchronous; ctx is reserved for future use
	reg, err := recipe.NewRegistry(dirs...)
	if err != nil {
		return fmt.Errorf("loading recipes: %w", err)
	}
	entries := reg.List()
	if len(entries) == 0 {
		_, _ = fmt.Fprintf(w, "No recipes found (searched: %s)\n", recipeDirsLabel(dirs))
		return nil
	}

	rows := make([][]string, 0, len(entries)+1)
	rows = append(rows, []string{"NAME", "TITLE", "DESCRIPTION"})
	for _, e := range entries {
		rows = append(rows, []string{e.Name, e.Title, e.Description})
	}
	_, _ = io.WriteString(w, renderTable(rows))
	return nil
}

// recipeDirsLabel joins the searched directories for the "no recipes"
// message, falling back to a stable placeholder when none resolved.
func recipeDirsLabel(dirs []string) string {
	if len(dirs) == 0 {
		return "no recipe directories resolved"
	}
	return strings.Join(dirs, ", ")
}
