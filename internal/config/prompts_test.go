package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRenderSlashPositionalArgs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "review.md"),
		[]byte("Review $1 for $2; full request: $@"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.RenderSlash("review", "main.go races")
	require.NoError(t, err)
	require.Equal(t, "Review main.go for races; full request: main.go races", out)
}

func TestRenderSlashArgumentsAlias(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "explain.md"),
		[]byte("Explain: $ARGUMENTS"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.RenderSlash("explain", "the failover logic")
	require.NoError(t, err)
	require.Equal(t, "Explain: the failover logic", out)
}

func TestRenderSlashQuotedFieldStaysWhole(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "open.md"),
		[]byte("Open [$1] then [$2]"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	// The double-quoted run with a space must survive as a single field.
	out, err := reg.RenderSlash("open", `"my file.go" tail`)
	require.NoError(t, err)
	require.Equal(t, "Open [my file.go] then [tail]", out)
}

func TestRenderSlashOutOfRangeIndexIsEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("First=$1 Second=$2 Third=$3"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.RenderSlash("triage", "only")
	require.NoError(t, err)
	require.Equal(t, "First=only Second= Third=", out)
}

func TestRenderSlashEscapedDollarIsLiteral(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "cost.md"),
		[]byte("Budget is $$5 for $@"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.RenderSlash("cost", "the task")
	require.NoError(t, err)
	require.Equal(t, "Budget is $5 for the task", out)
}

func TestRenderSlashStillSupportsInputPlaceholder(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("Triage {{input}} now"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	out, err := reg.RenderSlash("triage", "bug 42")
	require.NoError(t, err)
	require.Equal(t, "Triage bug 42 now", out)
}

func TestRenderSlashUnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("$1"), 0o644))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	_, err = reg.RenderSlash("nope", "x")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPromptNotFound)
}

func TestSplitFieldsHonorsQuotes(t *testing.T) {
	require.Equal(t, []string{"a", "b", "c"}, splitFields("a b c"))
	require.Equal(t, []string{"a b", "c"}, splitFields(`"a b" c`))
	require.Equal(t, []string{"a b", "c d"}, splitFields(`'a b' "c d"`))
	require.Equal(t, []string{"abc"}, splitFields(`a"b"c`))
	require.Equal(t, []string{"unterminated run"}, splitFields(`"unterminated run`))
	require.Empty(t, splitFields("   "))
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

func TestLoadPromptRegistryParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("---\ndescription: Triage a flaky test\nargument-hint: <test-name>\n---\nTriage {{input}} with care"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	p, ok := reg.Get("triage")
	require.True(t, ok)
	require.Equal(t, "Triage a flaky test", p.Description)
	require.Equal(t, "<test-name>", p.ArgumentHint)
	// The frontmatter is stripped, so the template is just the body and renders
	// without seeing the metadata lines.
	require.Equal(t, "Triage {{input}} with care", p.Template)

	out, err := reg.Render("triage", map[string]string{"input": "bug 42"})
	require.NoError(t, err)
	require.Equal(t, "Triage bug 42 with care", out)
}

func TestLoadPromptRegistryFrontmatterAcceptsArgumentHintSynonymAndQuotes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "review.md"),
		[]byte("---\nDescription: \"Review a file\"\nargument_hint: '<file>'\n---\nReview {{input}}"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	p, ok := reg.Get("review")
	require.True(t, ok)
	// Keys are case-insensitive and surrounding quotes are stripped.
	require.Equal(t, "Review a file", p.Description)
	// argument_hint is accepted as a synonym for argument-hint.
	require.Equal(t, "<file>", p.ArgumentHint)
}

func TestLoadPromptRegistryNoFrontmatterLeavesBodyIntact(t *testing.T) {
	dir := t.TempDir()
	// A document whose first content is a horizontal rule mid-text, plus a
	// plain prompt, must not be mistaken for frontmatter.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "plain.md"),
		[]byte("Summarise {{input}}\n\n---\n\nThanks"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	p, ok := reg.Get("plain")
	require.True(t, ok)
	require.Empty(t, p.Description)
	require.Empty(t, p.ArgumentHint)
	require.Equal(t, "Summarise {{input}}\n\n---\n\nThanks", p.Template)
}

func TestLoadPromptRegistryUnclosedFenceIsNotFrontmatter(t *testing.T) {
	dir := t.TempDir()
	// An opening fence with no closing fence must not swallow the body.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "weird.md"),
		[]byte("---\ndescription: never closed\nbody text {{input}}"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	p, ok := reg.Get("weird")
	require.True(t, ok)
	require.Empty(t, p.Description)
	require.Contains(t, p.Template, "body text {{input}}")
	require.Contains(t, p.Template, "description: never closed")
}

func TestPromptRegistryListSortsAndCarriesMetadata(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "beta.md"),
		[]byte("---\ndescription: B\n---\nbody"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "alpha.md"),
		[]byte("just a body"),
		0o644,
	))

	reg, err := LoadPromptRegistry(dir)
	require.NoError(t, err)

	list := reg.List()
	require.Len(t, list, 2)
	require.Equal(t, "alpha", list[0].Name)
	require.Equal(t, "beta", list[1].Name)
	require.Equal(t, "B", list[1].Description)
}

func TestParseFrontmatterEmptyOpeningFenceVariants(t *testing.T) {
	// CRLF line endings around the fences must still be recognised.
	meta, body := parseFrontmatter("---\r\ndescription: hi\r\n---\r\nbody")
	require.Equal(t, "hi", meta["description"])
	require.Equal(t, "body", strings.TrimSpace(body))
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
