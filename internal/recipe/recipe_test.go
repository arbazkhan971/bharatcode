package recipe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeRecipeFile serialises r to a *.recipe.json file in dir named stem and
// returns the absolute path. It is a test helper shared by multiple tests.
func writeRecipeFile(t *testing.T, dir, stem string, r *Recipe) string {
	t.Helper()
	data, err := json.Marshal(r)
	require.NoError(t, err, "marshaling recipe for test fixture")
	path := filepath.Join(dir, stem+recipeExt)
	require.NoError(t, os.WriteFile(path, data, 0o644), "writing recipe fixture")
	return path
}

// minimalRecipe returns a valid Recipe with all required fields set.
func minimalRecipe() *Recipe {
	return &Recipe{
		Title:       "Greet user",
		Description: "Emit a personalised greeting.",
		Prompt:      "Say hello to {{name}}.",
		Parameters: []Parameter{
			{
				Name:        "name",
				Type:        ParamTypeString,
				Requirement: RequirementRequired,
				Description: "The person to greet.",
			},
		},
	}
}

// --- Load ---

func TestLoad_WellFormed(t *testing.T) {
	dir := t.TempDir()
	r := minimalRecipe()
	path := writeRecipeFile(t, dir, "greet", r)

	got, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, r.Title, got.Title)
	require.Equal(t, r.Prompt, got.Prompt)
	require.Len(t, got.Parameters, 1)
	require.Equal(t, "name", got.Parameters[0].Name)
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/no.recipe.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading recipe")
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad"+recipeExt)
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o644))

	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing recipe")
}

// --- Validate ---

func TestValidate_WellFormed(t *testing.T) {
	require.NoError(t, Validate(minimalRecipe()))
}

func TestValidate_MissingTitle(t *testing.T) {
	r := minimalRecipe()
	r.Title = ""
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "title is required")
}

func TestValidate_MissingPrompt(t *testing.T) {
	r := minimalRecipe()
	r.Prompt = ""
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "prompt is required")
}

func TestValidate_ParameterMissingName(t *testing.T) {
	r := minimalRecipe()
	r.Parameters = []Parameter{
		{Name: "", Type: ParamTypeString, Requirement: RequirementOptional},
	}
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestValidate_BadParamType(t *testing.T) {
	r := minimalRecipe()
	r.Parameters = []Parameter{
		{Name: "x", Type: "invalid", Requirement: RequirementOptional},
	}
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown type")
}

func TestValidate_BadRequirement(t *testing.T) {
	r := minimalRecipe()
	r.Parameters = []Parameter{
		{Name: "x", Type: ParamTypeString, Requirement: "maybe"},
	}
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown requirement")
}

func TestValidate_SelectWithNoOptions(t *testing.T) {
	r := minimalRecipe()
	r.Parameters = []Parameter{
		{Name: "style", Type: ParamTypeSelect, Requirement: RequirementOptional, Options: nil},
	}
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one option")
}

func TestValidate_DuplicateParameterName(t *testing.T) {
	r := minimalRecipe()
	r.Parameters = []Parameter{
		{Name: "x", Type: ParamTypeString, Requirement: RequirementOptional},
		{Name: "x", Type: ParamTypeString, Requirement: RequirementOptional},
	}
	err := Validate(r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate parameter name")
}

func TestValidate_AllParamTypes(t *testing.T) {
	for _, tt := range []struct {
		ptype   ParamType
		options []string
	}{
		{ParamTypeString, nil},
		{ParamTypeNumber, nil},
		{ParamTypeBool, nil},
		{ParamTypeSelect, []string{"a", "b"}},
	} {
		r := &Recipe{
			Title:  "T",
			Prompt: "p",
			Parameters: []Parameter{
				{Name: "x", Type: tt.ptype, Requirement: RequirementOptional, Options: tt.options},
			},
		}
		require.NoError(t, Validate(r), "type %s should be valid", tt.ptype)
	}
}

// --- Render ---

func TestRender_SubstitutesProvidedParams(t *testing.T) {
	r := &Recipe{
		Title:  "Say hi",
		Prompt: "Hello {{first}} {{last}}!",
		Parameters: []Parameter{
			{Name: "first", Type: ParamTypeString, Requirement: RequirementRequired},
			{Name: "last", Type: ParamTypeString, Requirement: RequirementRequired},
		},
	}
	got, err := Render(r, map[string]string{"first": "Fatima", "last": "Sheikh"})
	require.NoError(t, err)
	require.Equal(t, "Hello Fatima Sheikh!", got)
}

func TestRender_UsesDefaultWhenNotProvided(t *testing.T) {
	r := &Recipe{
		Title:  "Coverage",
		Prompt: "Target coverage: {{pct}}.",
		Parameters: []Parameter{
			{Name: "pct", Type: ParamTypeString, Requirement: RequirementOptional, Default: "80%"},
		},
	}
	got, err := Render(r, nil)
	require.NoError(t, err)
	require.Equal(t, "Target coverage: 80%.", got)
}

func TestRender_ProvidedParamOverridesDefault(t *testing.T) {
	r := &Recipe{
		Title:  "Coverage",
		Prompt: "Target: {{pct}}.",
		Parameters: []Parameter{
			{Name: "pct", Type: ParamTypeString, Requirement: RequirementOptional, Default: "80%"},
		},
	}
	got, err := Render(r, map[string]string{"pct": "95%"})
	require.NoError(t, err)
	require.Equal(t, "Target: 95%.", got)
}

func TestRender_MissingRequiredParam(t *testing.T) {
	r := minimalRecipe() // "name" is required, no default
	_, err := Render(r, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required parameter")
	require.Contains(t, err.Error(), "name")
}

func TestRender_MissingUserPromptParam(t *testing.T) {
	r := &Recipe{
		Title:  "T",
		Prompt: "{{x}}",
		Parameters: []Parameter{
			{Name: "x", Type: ParamTypeString, Requirement: RequirementUserPrompt},
		},
	}
	_, err := Render(r, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required parameter")
}

func TestRender_UnresolvedPlaceholder(t *testing.T) {
	r := &Recipe{
		Title:  "T",
		Prompt: "Hello {{unknown}}",
		// No parameters — unknown is not declared.
	}
	_, err := Render(r, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unresolved placeholder")
}

func TestRender_BoolParamValidation(t *testing.T) {
	r := &Recipe{
		Title:  "T",
		Prompt: "verbose: {{v}}",
		Parameters: []Parameter{
			{Name: "v", Type: ParamTypeBool, Requirement: RequirementRequired},
		},
	}
	// Valid values.
	for _, val := range []string{"true", "false", "True", "FALSE"} {
		_, err := Render(r, map[string]string{"v": val})
		require.NoError(t, err, "bool value %q should be accepted", val)
	}
	// Invalid value.
	_, err := Render(r, map[string]string{"v": "yes"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bool parameter")
}

func TestRender_SelectParamValidation(t *testing.T) {
	r := &Recipe{
		Title:  "T",
		Prompt: "style: {{style}}",
		Parameters: []Parameter{
			{Name: "style", Type: ParamTypeSelect, Requirement: RequirementRequired,
				Options: []string{"testify", "stdlib"}},
		},
	}
	got, err := Render(r, map[string]string{"style": "testify"})
	require.NoError(t, err)
	require.Equal(t, "style: testify", got)

	_, err = Render(r, map[string]string{"style": "other"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "select parameter")
}

func TestRender_WithInstructions(t *testing.T) {
	r := &Recipe{
		Title:        "T",
		Instructions: "Be concise.",
		Prompt:       "Summarise {{topic}}.",
		Parameters: []Parameter{
			{Name: "topic", Type: ParamTypeString, Requirement: RequirementRequired},
		},
	}
	got, err := Render(r, map[string]string{"topic": "Go modules"})
	require.NoError(t, err)
	require.Equal(t, "Be concise.\n\nSummarise Go modules.", got)
}

func TestRender_OptionalMissingNoDefaultOK(t *testing.T) {
	// An optional param with no default and no supplied value resolves to "".
	// The placeholder is replaced with empty string.
	r := &Recipe{
		Title:  "T",
		Prompt: "Prefix: {{opt}}suffix",
		Parameters: []Parameter{
			{Name: "opt", Type: ParamTypeString, Requirement: RequirementOptional},
		},
	}
	got, err := Render(r, nil)
	require.NoError(t, err)
	require.Equal(t, "Prefix: suffix", got)
}

// --- LoadAndRender round-trip ---

func TestLoadAndRender_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := &Recipe{
		Title:       "Generate tests",
		Description: "Scaffold table-driven Go tests for a package.",
		Prompt:      "Write tests for {{package}}. Coverage: {{coverage}}.",
		Parameters: []Parameter{
			{Name: "package", Type: ParamTypeString, Requirement: RequirementRequired, Description: "Go import path"},
			{Name: "coverage", Type: ParamTypeString, Requirement: RequirementOptional, Default: "80%"},
		},
	}
	path := writeRecipeFile(t, dir, "gen-tests", r)

	loaded, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, Validate(loaded))

	rendered, err := Render(loaded, map[string]string{"package": "internal/foo"})
	require.NoError(t, err)
	require.Equal(t, "Write tests for internal/foo. Coverage: 80%.", rendered)
}

// --- Registry ---

func TestRegistry_DiscoversSingleRecipe(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "greet", minimalRecipe())

	reg, err := NewRegistry(dir)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len())

	list := reg.List()
	require.Len(t, list, 1)
	require.Equal(t, "greet", list[0].Name)
	require.Equal(t, minimalRecipe().Title, list[0].Title)
}

func TestRegistry_ListIsSorted(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"zebra", "alpha", "middle"} {
		r := &Recipe{Title: name, Prompt: "p", Description: name + " desc"}
		writeRecipeFile(t, dir, name, r)
	}

	reg, err := NewRegistry(dir)
	require.NoError(t, err)
	require.Equal(t, 3, reg.Len())

	names := make([]string, 0, 3)
	for _, e := range reg.List() {
		names = append(names, e.Name)
	}
	require.Equal(t, []string{"alpha", "middle", "zebra"}, names)
}

func TestRegistry_ProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	// Same stem "greet" in both dirs.
	globalVersion := &Recipe{Title: "Global Greet", Prompt: "global {{x}}", Description: "global",
		Parameters: []Parameter{{Name: "x", Type: ParamTypeString, Requirement: RequirementOptional}}}
	projectVersion := &Recipe{Title: "Project Greet", Prompt: "project {{x}}", Description: "project",
		Parameters: []Parameter{{Name: "x", Type: ParamTypeString, Requirement: RequirementOptional}}}

	writeRecipeFile(t, globalDir, "greet", globalVersion)
	writeRecipeFile(t, projectDir, "greet", projectVersion)

	// global first, project second → project wins.
	reg, err := NewRegistry(globalDir, projectDir)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len())

	e, ok := reg.Get("greet")
	require.True(t, ok)
	require.Equal(t, "Project Greet", e.Title, "project-local recipe must shadow the global one")
}

func TestRegistry_MissingDirIsSkipped(t *testing.T) {
	reg, err := NewRegistry("/nonexistent/path/recipes")
	require.NoError(t, err) // missing dir is not an error
	require.Equal(t, 0, reg.Len())
}

func TestRegistry_NonRecipeFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	// Plain JSON — not *.recipe.json.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644))
	// Correct extension.
	writeRecipeFile(t, dir, "real", minimalRecipe())

	reg, err := NewRegistry(dir)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len())
	_, ok := reg.Get("real")
	require.True(t, ok)
}

func TestRegistry_MalformedRecipeSkipped(t *testing.T) {
	dir := t.TempDir()
	// Write a syntactically invalid file with the correct extension.
	path := filepath.Join(dir, "bad"+recipeExt)
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))
	// Also write a valid one.
	writeRecipeFile(t, dir, "good", minimalRecipe())

	reg, err := NewRegistry(dir)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len(), "only the valid recipe should be registered")
}

func TestRegistry_InvalidRecipeSkipped(t *testing.T) {
	dir := t.TempDir()
	// Valid JSON but semantically invalid (no title).
	invalid := &Recipe{Title: "", Prompt: "p", Description: "d"}
	writeRecipeFile(t, dir, "invalid", invalid)
	writeRecipeFile(t, dir, "valid", minimalRecipe())

	reg, err := NewRegistry(dir)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len())
}

func TestRegistry_GetMissing(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)
	_, ok := reg.Get("missing")
	require.False(t, ok)
}

func TestRegistry_EntryLoad(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "greet", minimalRecipe())

	reg, err := NewRegistry(dir)
	require.NoError(t, err)

	e, ok := reg.Get("greet")
	require.True(t, ok)

	r, err := e.Load()
	require.NoError(t, err)
	require.Equal(t, minimalRecipe().Title, r.Title)
}

func TestRegistry_Summaries(t *testing.T) {
	dir := t.TempDir()
	r := minimalRecipe()
	writeRecipeFile(t, dir, "greet", r)

	reg, err := NewRegistry(dir)
	require.NoError(t, err)

	s := reg.Summaries()
	require.Contains(t, s, "<available_recipes>")
	require.Contains(t, s, "greet")
	require.Contains(t, s, r.Title)
}

func TestGlobalRecipesDir(t *testing.T) {
	got := GlobalRecipesDir("/home/user/.config/bharatcode/config.json")
	require.Equal(t, "/home/user/.config/bharatcode/recipes", got)
	require.Equal(t, "", GlobalRecipesDir(""))
}

func TestProjectRecipesDir(t *testing.T) {
	got := ProjectRecipesDir("/workspace/myproject")
	require.Equal(t, "/workspace/myproject/.bharatcode/recipes", got)
	require.Equal(t, "", ProjectRecipesDir(""))
}

func TestDefaultDirs(t *testing.T) {
	dirs := DefaultDirs("/home/user/.config/bharatcode/config.json", "/workspace/proj")
	require.Len(t, dirs, 2)
	require.Equal(t, "/home/user/.config/bharatcode/recipes", dirs[0])
	require.Equal(t, "/workspace/proj/.bharatcode/recipes", dirs[1])
}
