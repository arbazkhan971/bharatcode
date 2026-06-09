package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// resetStaticBodyCache clears the process-wide static-body cache and restores it
// on test cleanup so a test starts from a cold cache and does not leak entries
// into sibling tests that share the package-global.
func resetStaticBodyCache(t *testing.T) {
	t.Helper()
	staticBodyCache.Clear()
	t.Cleanup(func() { staticBodyCache.Clear() })
}

// TestStaticBodyCacheReusesBodyAcrossTurns proves that a second render under an
// unchanged config reuses the cached static body instead of re-scanning the
// recipe directory. The proof is observable: a recipe fixture is rewritten
// between the two renders, yet the second render must keep the original text —
// only a cache hit (serving the already-assembled body) can produce that.
func TestStaticBodyCacheReusesBodyAcrossTurns(t *testing.T) {
	resetStaticBodyCache(t)
	workdir := t.TempDir()
	recipesRoot := filepath.Join(workdir, ".bharatcode", "recipes")
	writeRecipeFixture(t, recipesRoot, "ship", `{"title":"First Title","description":"first","prompt":"do it"}`)

	// Hermetic discovery: no skills, only our recipe fixture dir.
	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{recipesRoot} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	// Freeze the clock so the trailing environment block is byte-stable across
	// the two renders; otherwise a second-boundary crossing would mask the
	// static-body comparison this test cares about.
	restoreNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = restoreNow })

	first := renderInWorkdir(t, workdir)
	require.Contains(t, first, "<title>First Title</title>")

	// Rewrite the recipe on disk. A cache miss would re-scan and pick this up;
	// a hit serves the body assembled on the first render and never re-reads it.
	writeRecipeFixture(t, recipesRoot, "ship", `{"title":"Second Title","description":"second","prompt":"do it"}`)

	second := renderInWorkdir(t, workdir)

	// The cache served the first body verbatim, so the stale title persists and
	// the new on-disk title is absent — the directory scan was skipped.
	require.Contains(t, second, "<title>First Title</title>")
	require.NotContains(t, second, "<title>Second Title</title>")
	// With the clock frozen, the whole prompt is identical: the cached static
	// body plus an identical environment block.
	require.Equal(t, first, second)
}

// TestStaticBodyCacheInvalidatesOnConfigChange proves the cache is keyed on the
// inputs that determine the body: changing the active tool set produces a
// different key, misses the cache, and re-assembles the body — so a profile or
// config change is never served a stale prompt.
func TestStaticBodyCacheInvalidatesOnConfigChange(t *testing.T) {
	resetStaticBodyCache(t)
	workdir := t.TempDir()

	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	restoreNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = restoreNow })

	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// First config: a single tool advertised in the prompt.
	regA := newFakeRegistry()
	regA.Register(&recordingTool{name: "write", desc: "Write a file to disk."})
	bodyA, err := renderPrompt(context.Background(), "coder", "", regA, nil)
	require.NoError(t, err)
	require.Contains(t, bodyA, "write")
	require.NotContains(t, bodyA, "deploy_service")

	// Second config: a different tool set. Because the tool signature is part
	// of the cache key, this misses the cache and re-renders with the new tool.
	regB := newFakeRegistry()
	regB.Register(&recordingTool{name: "deploy_service", desc: "Deploy the service to prod."})
	bodyB, err := renderPrompt(context.Background(), "coder", "", regB, nil)
	require.NoError(t, err)
	require.Contains(t, bodyB, "deploy_service")
	require.NotEqual(t, bodyA, bodyB)
}

// TestStaticBodyKeyFieldsAreDistinguishing asserts that each field of the cache
// key actually changes the key, so no two materially different configs collapse
// onto the same entry. A struct key compares by value, so this is a direct
// inequality check on representative variations.
func TestStaticBodyKeyFieldsAreDistinguishing(t *testing.T) {
	base := staticBodyKey{
		agent:      "coder",
		override:   "",
		workdir:    "/repo",
		toolsSig:   "sig-a",
		skillDirs:  "/skills",
		recipeDirs: "/recipes",
	}
	variants := []staticBodyKey{
		func() staticBodyKey { k := base; k.agent = "task"; return k }(),
		func() staticBodyKey { k := base; k.override = "CUSTOM PROMPT"; return k }(),
		func() staticBodyKey { k := base; k.workdir = "/other"; return k }(),
		func() staticBodyKey { k := base; k.toolsSig = "sig-b"; return k }(),
		func() staticBodyKey { k := base; k.skillDirs = "/skills2"; return k }(),
		func() staticBodyKey { k := base; k.recipeDirs = "/recipes2"; return k }(),
	}
	for i, v := range variants {
		require.NotEqual(t, base, v, "variant %d should differ from base", i)
	}
}

// TestToolsSignatureSensitivity checks the tool-set hash: an identical tool list
// yields the same signature (so an unchanged config hits the cache), while any
// change to a tool's name, description, or schema — or the set of tools —
// yields a different signature (so the change invalidates the cache).
func TestToolsSignatureSensitivity(t *testing.T) {
	a := []ToolInfo{
		{Name: "write", Description: "Write a file.", Schema: `{"type":"object"}`},
		{Name: "bash", Description: "Run a command.", Schema: `{"type":"object"}`},
	}
	// Same content reproduces the same signature.
	require.Equal(t, toolsSignature(a), toolsSignature(append([]ToolInfo(nil), a...)))

	// A changed description flips the signature.
	desc := append([]ToolInfo(nil), a...)
	desc[0].Description = "Write a file to disk."
	require.NotEqual(t, toolsSignature(a), toolsSignature(desc))

	// A changed schema flips the signature.
	schema := append([]ToolInfo(nil), a...)
	schema[1].Schema = `{"type":"object","required":["cmd"]}`
	require.NotEqual(t, toolsSignature(a), toolsSignature(schema))

	// A renamed tool flips the signature.
	rename := append([]ToolInfo(nil), a...)
	rename[0].Name = "edit"
	require.NotEqual(t, toolsSignature(a), toolsSignature(rename))

	// Adding a tool flips the signature.
	added := append(append([]ToolInfo(nil), a...), ToolInfo{Name: "grep", Description: "Search.", Schema: "{}"})
	require.NotEqual(t, toolsSignature(a), toolsSignature(added))

	// The empty set has a stable, well-defined signature.
	require.Equal(t, toolsSignature(nil), toolsSignature([]ToolInfo{}))
}

// TestStaticBodyCacheExcludesEnvironmentBlock asserts the cache covers only the
// stable body: two renders under the same config but a moved clock keep an
// identical body prefix while the trailing environment block reflects the new
// time. This is what lets the cached prefix land on a provider's prompt cache
// while the volatile tail still updates each turn.
func TestStaticBodyCacheExcludesEnvironmentBlock(t *testing.T) {
	resetStaticBodyCache(t)
	workdir := t.TempDir()

	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	restoreNow := nowFunc
	t.Cleanup(func() { nowFunc = restoreNow })

	nowFunc = func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }
	first := renderInWorkdir(t, workdir)

	nowFunc = func() time.Time { return time.Date(2026, 6, 9, 13, 30, 0, 0, time.UTC) }
	second := renderInWorkdir(t, workdir)

	// The environment block carries the new timestamp on the second render...
	require.Contains(t, second, "2026-06-09T13:30:00Z")
	require.NotContains(t, second, "2026-06-09T12:00:00Z")

	// ...but everything above the environment header is byte-identical, because
	// the static body was served from the cache rather than re-assembled.
	firstBody := first[:strings.Index(first, environmentHeader)]
	secondBody := second[:strings.Index(second, environmentHeader)]
	require.Equal(t, firstBody, secondBody)
}
