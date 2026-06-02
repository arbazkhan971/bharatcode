package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/spf13/cobra"
)

// loadedSkill is the minimal projection of a discovered skill that the
// skills command needs to render: a name and a one-line description.
// It mirrors the fields the internal/skills package exposes on each
// entry returned by Set.List(), so the rendering and test code does not
// depend on that package's concrete type. The integration seam in
// skillsLoader adapts skills.Skill values into this shape.
type loadedSkill struct {
	Name        string
	Description string
}

// skillsLoader discovers skills under dirs and returns them as the
// flat projection the command renders. It is a package-level variable
// so tests can inject a deterministic, offline loader and so the real
// implementation can be wired in once the internal/skills package
// lands this wave.
//
// INTEGRATION SEAM: when internal/skills is importable, replace the
// default body below with a call into it, e.g.:
//
//	set, err := skills.LoadSkills(dirs...)
//	if err != nil {
//	    return nil, err
//	}
//	out := make([]loadedSkill, 0)
//	for _, s := range set.List() {
//	    out = append(out, loadedSkill{Name: s.Name, Description: s.Description})
//	}
//	return out, nil
//
// Until then the default returns no skills so the command and the rest
// of the cmd package compile and run.
var skillsLoader = func(_ context.Context, _ ...string) ([]loadedSkill, error) {
	return nil, nil
}

// newSkillsCmd builds the "skills" subcommand, which discovers skills
// in the standard skill directories and prints each skill's name and
// description as a table. When no skills are found it reports the
// directories that were searched so the user knows where to add them.
func newSkillsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "List discovered skills",
		Long: "Discover skills in the standard skill directories (the " +
			"global config directory and the project directory) and " +
			"print each skill's name and description. When no skills are " +
			"found, the searched directories are reported.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			dirs := skillDirs(opts)
			return runSkills(cmd.Context(), cmd.OutOrStdout(), dirs, skillsLoader)
		},
	}
}

// skillDirs resolves the standard skill directories in precedence
// order: the global config directory's skills/ subdirectory, then the
// project's .bharatcode/skills directory when a project directory is
// set. Paths are expanded so ~ and environment references are resolved
// the same way the rest of the CLI resolves them.
func skillDirs(opts *rootOptions) []string {
	var dirs []string
	if global := config.GlobalPath(); global != "" {
		dirs = append(dirs, filepath.Join(filepath.Dir(global), "skills"))
	}
	if opts != nil && opts.projectDir != "" {
		dirs = append(dirs, filepath.Join(opts.projectDir, ".bharatcode", "skills"))
	}
	for i, d := range dirs {
		dirs[i] = util.ExpandPath(d)
	}
	return dirs
}

// runSkills loads skills from dirs using load and writes the result to
// w. It is split from newSkillsCmd so tests can inject a loader and
// drive rendering deterministically. A load error is returned wrapped;
// an empty result prints the "no skills" message with the searched
// directories.
func runSkills(ctx context.Context, w io.Writer, dirs []string, load func(context.Context, ...string) ([]loadedSkill, error)) error {
	found, err := load(ctx, dirs...)
	if err != nil {
		return fmt.Errorf("loading skills: %w", err)
	}
	if len(found) == 0 {
		_, _ = fmt.Fprintf(w, "No skills found (searched: %s)\n", skillDirsLabel(dirs))
		return nil
	}

	rows := make([][]string, 0, len(found)+1)
	rows = append(rows, []string{"NAME", "DESCRIPTION"})
	for _, s := range found {
		rows = append(rows, []string{s.Name, s.Description})
	}
	_, _ = io.WriteString(w, renderTable(rows))
	return nil
}

// skillDirsLabel joins the searched directories for the "no skills"
// message, falling back to a stable placeholder when none resolved.
func skillDirsLabel(dirs []string) string {
	if len(dirs) == 0 {
		return "no skill directories resolved"
	}
	return strings.Join(dirs, ", ")
}
