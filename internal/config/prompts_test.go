package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPromptRegistryRendersInput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}} with care"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	require.Contains(t, reg.Names(), "triage")

	out, err := reg.Render("triage", map[string]string{"input": "bug 42"})
	require.NoError(t, err)
	require.Equal(t, "Triage bug 42 with care", out)
}

func TestLoadPromptRegistryTrimsTrailingWhitespace(t *testing.T) {
	dir := t.TempDir()
	// File written with a trailing newline, as editors commonly do.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}} with care\n"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.Render("triage", map[string]string{"input": "bug 42"})
	require.NoError(t, err)
	require.Equal(t, "Triage bug 42 with care", out)
}

func TestRenderMultipleNamedVars(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "review.md"),
		[]byte("Review {{file}} for {{concern}}: {{input}}"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.Render("review", map[string]string{
		"file":    "main.go",
		"concern": "races",
		"input":   "be thorough",
	})
	require.NoError(t, err)
	require.Equal(t, "Review main.go for races: be thorough", out)
}

func TestRenderMissingNameErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}} with care"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	_, err = reg.Render("does-not-exist", map[string]string{"input": "x"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPromptNotFound)
	require.Contains(t, err.Error(), "does-not-exist")
}

func TestRenderMissingVarErrorsNamingVariable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}} on {{date}}"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	// "date" is referenced by the template but not supplied.
	_, err = reg.Render("triage", map[string]string{"input": "bug 42"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "date")
	require.NotContains(t, err.Error(), "input", "the supplied variable should not be reported missing")
}

func TestRenderIgnoresExtraArgs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}}"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	// Unused "extra" key must be silently ignored.
	out, err := reg.Render("triage", map[string]string{
		"input": "bug 42",
		"extra": "ignored",
	})
	require.NoError(t, err)
	require.Equal(t, "Triage bug 42", out)
}

func TestLoadPromptRegistryIgnoresNonMarkdownAndDirs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("ok"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("nope"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("nope"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir.md"), 0o755))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"triage"}, reg.Names())
}

func TestLoadPromptRegistryMissingDirIsNotError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	reg, err := LoadPromptRegistry(missing)
	require.NoError(t, err)
	require.Empty(t, reg.Names())
}

func TestLoadPromptRegistryLaterDirOverridesEarlier(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(globalDir, "triage.md"),
		[]byte("GLOBAL triage {{input}}"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "triage.md"),
		[]byte("PROJECT triage {{input}}"),
		0o644,
	))

	// Project dir is listed last, so it must win.
	reg, err := LoadPromptRegistry(globalDir, projectDir)
	require.NoError(t, err)

	require.Equal(t, []string{"triage"}, reg.Names())

	out, err := reg.Render("triage", map[string]string{"input": "x"})
	require.NoError(t, err)
	require.Equal(t, "PROJECT triage x", out)
}

func TestLoadPromptRegistryMergesAcrossDirs(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "alpha.md"), []byte("A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "beta.md"), []byte("B"), 0o644))

	reg, err := LoadPromptRegistry(globalDir, projectDir)
	require.NoError(t, err)

	require.Equal(t, []string{"alpha", "beta"}, reg.Names())
}

func TestLoadPromptRegistryNamesIsSortedAndCopy(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"zeta.md", "alpha.md", "mid.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644))
	}

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	names := reg.Names()
	require.Equal(t, []string{"alpha", "mid", "zeta"}, names)

	// Mutating the returned slice must not affect a later call.
	names[0] = "MUTATED"
	require.Equal(t, []string{"alpha", "mid", "zeta"}, reg.Names())
}

func TestGetReportsPresence(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("body"), 0o644))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	p, ok := reg.Get("triage")
	require.True(t, ok)
	require.Equal(t, "triage", p.Name)
	require.Equal(t, "body", p.Template)
	require.Equal(t, filepath.Join(dir, "triage.md"), p.Source)

	_, ok = reg.Get("nope")
	require.False(t, ok)
}

func TestRenderErrorsWrapSentinelOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("hi"), 0o644))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	_, err = reg.Render("missing", nil)
	require.True(t, errors.Is(err, ErrPromptNotFound))
}
