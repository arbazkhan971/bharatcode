package recipe

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// recipeExt is the canonical file extension for recipe files.
const recipeExt = ".recipe.json"

// Entry is the minimal projection of a loaded recipe used by the Registry's
// listing API. It avoids exposing the full Recipe struct on the hot path where
// callers only need the name and description.
type Entry struct {
	// Name is the stem of the recipe file (filename minus the .recipe.json
	// extension). It is the key callers pass to Registry.Get.
	Name string
	// Title is the recipe's declared title.
	Title string
	// Description is the recipe's declared one-line description.
	Description string
	// Path is the absolute filesystem path of the recipe file.
	Path string
}

// Registry discovers and caches recipe files from one or more directories.
// It mirrors the project-over-global precedence used by the skills loader:
// directories supplied later in the constructor override earlier ones for
// recipes with the same stem name, so a project-local recipe shadows a
// global one.
//
// Callers construct a Registry with NewRegistry, optionally passing the global
// recipes directory followed by the project recipes directory so project
// recipes win:
//
//	reg, err := NewRegistry(globalRecipesDir, projectRecipesDir)
type Registry struct {
	byName map[string]Entry
	order  []string
}

// NewRegistry builds a Registry by scanning each directory in dirs for
// *.recipe.json files. Directories are scanned in order; a later directory
// overrides an earlier one when both contain a recipe with the same stem name.
// A missing or unreadable directory is skipped with a warning rather than
// failing the whole load. The returned error is reserved for unrecoverable
// internal failures (none currently); scan errors are logged and skipped.
func NewRegistry(dirs ...string) (*Registry, error) {
	reg := &Registry{byName: make(map[string]Entry)}
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			slog.Warn("Skipping recipe directory: cannot read", "dir", dir, "error", err)
			continue
		}
		for _, de := range entries {
			if de.IsDir() {
				continue
			}
			if !strings.HasSuffix(de.Name(), recipeExt) {
				continue
			}
			path := filepath.Join(dir, de.Name())
			stem := strings.TrimSuffix(de.Name(), recipeExt)
			r, err := Load(path)
			if err != nil {
				slog.Warn("Skipping malformed recipe", "path", path, "error", err)
				continue
			}
			if verr := Validate(r); verr != nil {
				slog.Warn("Skipping invalid recipe", "path", path, "error", verr)
				continue
			}
			reg.byName[stem] = Entry{
				Name:        stem,
				Title:       r.Title,
				Description: r.Description,
				Path:        path,
			}
		}
	}
	reg.finalize()
	return reg, nil
}

// finalize rebuilds the deterministic name order from the current contents so
// List is stable across runs.
func (reg *Registry) finalize() {
	reg.order = make([]string, 0, len(reg.byName))
	for name := range reg.byName {
		reg.order = append(reg.order, name)
	}
	sort.Strings(reg.order)
}

// List returns all discovered recipes in deterministic name order.
func (reg *Registry) List() []Entry {
	if reg == nil {
		return nil
	}
	out := make([]Entry, 0, len(reg.order))
	for _, name := range reg.order {
		out = append(out, reg.byName[name])
	}
	return out
}

// Get returns the Entry for the recipe with the given name (stem) and whether
// it was found.
func (reg *Registry) Get(name string) (Entry, bool) {
	if reg == nil {
		return Entry{}, false
	}
	e, ok := reg.byName[name]
	return e, ok
}

// Load returns the parsed Recipe for the entry. It re-reads the file from
// disk so callers always get the current on-disk state.
func (e Entry) Load() (*Recipe, error) {
	r, err := Load(e.Path)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Len reports how many recipes the Registry holds.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.byName)
}

// GlobalRecipesDir returns the canonical global recipes directory:
// the sibling recipes/ directory next to the global BharatCode config file.
// It mirrors the convention used by skillDirs in the cmd package.
// Returns an empty string when the global config path cannot be resolved.
func GlobalRecipesDir(globalConfigPath string) string {
	if globalConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(globalConfigPath), "recipes")
}

// ProjectRecipesDir returns the canonical project-local recipes directory:
// <projectDir>/.bharatcode/recipes. Returns an empty string when projectDir is
// empty.
func ProjectRecipesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ".bharatcode", "recipes")
}

// DefaultDirs returns the standard recipe directory pair in precedence order
// (global first, project second so project wins on collision), suitable for
// passing directly to NewRegistry.
//
//	reg, err := recipe.NewRegistry(recipe.DefaultDirs(config.GlobalPath(), projectDir)...)
func DefaultDirs(globalConfigPath, projectDir string) []string {
	var dirs []string
	if g := GlobalRecipesDir(globalConfigPath); g != "" {
		dirs = append(dirs, g)
	}
	if p := ProjectRecipesDir(projectDir); p != "" {
		dirs = append(dirs, p)
	}
	return dirs
}

// Summaries renders the registry's contents as a human-readable block for
// injection into a system prompt or CLI listing. Each recipe appears on its own
// line as "  name — description". Returns an empty string when the registry is
// empty.
func (reg *Registry) Summaries() string {
	if reg == nil || len(reg.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_recipes>")
	for _, name := range reg.order {
		e := reg.byName[name]
		b.WriteString(fmt.Sprintf("\n  <recipe><name>%s</name><title>%s</title><description>%s</description></recipe>",
			xmlEscape(e.Name), xmlEscape(e.Title), xmlEscape(e.Description)))
	}
	b.WriteString("\n</available_recipes>")
	return b.String()
}

// xmlEscape escapes the five XML special characters so recipe metadata that
// contains markup characters renders as well-formed XML text.
func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}
