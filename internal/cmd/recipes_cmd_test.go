package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRecipesDirsResolution verifies that recipeDirs correctly assembles the
// standard recipe directories with proper precedence and expansion.
func TestRecipesDirsResolution(t *testing.T) {
	tests := []struct {
		name     string
		opts     *rootOptions
		checkDir func(string) bool
	}{
		{
			name: "with project dir",
			opts: &rootOptions{projectDir: "/test/project"},
			checkDir: func(d string) bool {
				return strings.Contains(d, "recipes")
			},
		},
		{
			name: "nil options",
			opts: nil,
			checkDir: func(d string) bool {
				return strings.Contains(d, "recipes")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := recipeDirs(tt.opts)
			require.NotEmpty(t, dirs, "recipe dirs should not be empty")
			for _, d := range dirs {
				require.True(t, tt.checkDir(d), "dir should contain expected path: %s", d)
			}
		})
	}
}

// TestRunRecipesEmptyDirectory verifies that runRecipes reports when no recipes
// are found, including the searched directories.
func TestRunRecipesEmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	var buf bytes.Buffer

	err := runRecipes(context.Background(), &buf, []string{tempDir})
	require.NoError(t, err)

	output := buf.String()
	require.Contains(t, output, "No recipes found")
	require.Contains(t, output, tempDir)
}

// TestRunRecipesWithRecipes verifies that runRecipes discovers and lists recipes
// correctly, displaying NAME, TITLE, and DESCRIPTION columns.
func TestRunRecipesWithRecipes(t *testing.T) {
	tempDir := t.TempDir()

	// Write a minimal test recipe file.
	recipeContent := `{
  "title": "Test Recipe",
  "description": "A test recipe for listing.",
  "prompt": "Test prompt with {{param}}.",
  "parameters": [
    {
      "name": "param",
      "type": "string",
      "requirement": "required",
      "description": "A test parameter."
    }
  ]
}`
	recipeFile := filepath.Join(tempDir, "test.recipe.json")
	require.NoError(t, os.WriteFile(recipeFile, []byte(recipeContent), 0o644))

	var buf bytes.Buffer
	err := runRecipes(context.Background(), &buf, []string{tempDir})
	require.NoError(t, err)

	output := buf.String()
	require.Contains(t, output, "NAME")
	require.Contains(t, output, "TITLE")
	require.Contains(t, output, "DESCRIPTION")
	require.Contains(t, output, "test")
	require.Contains(t, output, "Test Recipe")
	require.Contains(t, output, "A test recipe for listing.")
}

// TestRecipesDirsFallsBackToCwd verifies that, with no --project-dir set,
// recipeDirs uses the current working directory as the project root so a
// project recipe under ./.bharatcode/recipes is discoverable without the flag.
func TestRecipesDirsFallsBackToCwd(t *testing.T) {
	tempDir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// macOS reports the temp dir through a /private symlink; resolve both
	// sides so the substring assertion is stable.
	resolved, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	dirs := recipeDirs(&rootOptions{projectDir: ""})
	found := false
	for _, d := range dirs {
		rd, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(d)))
		if err == nil && rd == resolved {
			found = true
		}
	}
	require.True(t, found, "recipeDirs should include a project dir rooted at cwd; got %v", dirs)
}

// TestRecipesDirsLabel verifies the label formatting for empty and non-empty
// directory lists.
func TestRecipesDirsLabel(t *testing.T) {
	tests := []struct {
		name     string
		dirs     []string
		expected string
	}{
		{
			name:     "empty dirs",
			dirs:     []string{},
			expected: "no recipe directories resolved",
		},
		{
			name:     "single dir",
			dirs:     []string{"/tmp/recipes"},
			expected: "/tmp/recipes",
		},
		{
			name:     "multiple dirs",
			dirs:     []string{"/tmp/recipes", "/home/recipes"},
			expected: "/tmp/recipes, /home/recipes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := recipeDirsLabel(tt.dirs)
			require.Equal(t, tt.expected, result)
		})
	}
}
