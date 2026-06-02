package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeSkill creates root/<name>/SKILL.md with the given content and
// returns the skill directory path.
func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, skillFilename), []byte(content), 0o644))
	return dir
}

func TestLoadSkillsTwoSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "pdf", "---\nname: pdf\ndescription: Fill and read PDF forms\n---\n\nUse pdftk to fill forms.\nThen verify output.\n")
	writeSkill(t, root, "git-flow", "---\nname: git-flow\ndescription: Manage branches and releases\n---\nRun git flow init first.\n")

	set, err := LoadSkills(root)
	require.NoError(t, err)
	require.Equal(t, 2, set.Len())

	// List is sorted by name, deterministically.
	list := set.List()
	require.Len(t, list, 2)
	require.Equal(t, "git-flow", list[0].Name)
	require.Equal(t, "Manage branches and releases", list[0].Description)
	require.Equal(t, "pdf", list[1].Name)
	require.Equal(t, "Fill and read PDF forms", list[1].Description)

	// Get returns the parsed body (frontmatter and closing delimiter
	// stripped, surrounding whitespace trimmed).
	pdf, ok := set.Get("pdf")
	require.True(t, ok)
	require.Equal(t, "Use pdftk to fill forms.\nThen verify output.", pdf.Body)
	require.Equal(t, filepath.Join(root, "pdf"), pdf.Dir)

	gitFlow, ok := set.Get("git-flow")
	require.True(t, ok)
	require.Equal(t, "Run git flow init first.", gitFlow.Body)

	_, ok = set.Get("missing")
	require.False(t, ok)

	// Summaries lists both skills, one per line, in name order.
	summaries := set.Summaries()
	require.Contains(t, summaries, "Fill and read PDF forms")
	require.Contains(t, summaries, "Manage branches and releases")
	require.Equal(
		t,
		"git-flow — Manage branches and releases\npdf — Fill and read PDF forms",
		summaries,
	)
}

func TestLoadSkillsSkipsMalformed(t *testing.T) {
	root := t.TempDir()
	// A valid skill.
	writeSkill(t, root, "good", "---\nname: good\ndescription: A working skill\n---\nbody\n")
	// Missing description.
	writeSkill(t, root, "no-desc", "---\nname: no-desc\n---\nbody\n")
	// No frontmatter delimiter at all.
	writeSkill(t, root, "no-fm", "just some prose without frontmatter\n")
	// Unterminated frontmatter.
	writeSkill(t, root, "unterminated", "---\nname: x\ndescription: y\nbody continues forever")
	// A directory with no SKILL.md is silently skipped (not a skill).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755))

	set, err := LoadSkills(root)
	// Malformed skills do not fail the whole load.
	require.NoError(t, err)
	require.Equal(t, 1, set.Len())

	_, ok := set.Get("good")
	require.True(t, ok)
	for _, bad := range []string{"no-desc", "no-fm", "unterminated", "empty-dir"} {
		_, ok := set.Get(bad)
		require.Falsef(t, ok, "malformed skill %q should be skipped", bad)
	}
}

func TestLoadSkillsMissingRootIsSkipped(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "ok", "---\nname: ok\ndescription: present\n---\nbody\n")
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	// A non-existent root is skipped, not an error.
	set, err := LoadSkills(missing, root)
	require.NoError(t, err)
	require.Equal(t, 1, set.Len())
	_, ok := set.Get("ok")
	require.True(t, ok)
}

func TestLoadSkillsLaterRootOverrides(t *testing.T) {
	globalRoot := t.TempDir()
	projectRoot := t.TempDir()
	writeSkill(t, globalRoot, "deploy", "---\nname: deploy\ndescription: global deploy\n---\nglobal body\n")
	writeSkill(t, projectRoot, "deploy", "---\nname: deploy\ndescription: project deploy\n---\nproject body\n")

	// Project root is passed last, so it wins.
	set, err := LoadSkills(globalRoot, projectRoot)
	require.NoError(t, err)
	require.Equal(t, 1, set.Len())

	deploy, ok := set.Get("deploy")
	require.True(t, ok)
	require.Equal(t, "project deploy", deploy.Description)
	require.Equal(t, "project body", deploy.Body)
}

func TestLoadSkillsNoneEmptySet(t *testing.T) {
	set, err := LoadSkills()
	require.NoError(t, err)
	require.Equal(t, 0, set.Len())
	require.Empty(t, set.List())
	require.Empty(t, set.Summaries())
}

func TestParseSkillDescriptionWithColon(t *testing.T) {
	// SplitN-style parsing keeps colons inside the value.
	name, desc, body, err := parseSkill("---\nname: api\ndescription: Calls api: GET, POST, etc.\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "api", name)
	require.Equal(t, "Calls api: GET, POST, etc.", desc)
	require.Equal(t, "body", body)
}

func TestParseSkillToleratesBOMAndQuotes(t *testing.T) {
	// A leading UTF-8 BOM and quoted values are tolerated.
	name, desc, _, err := parseSkill("\ufeff---\nname: \"quoted\"\ndescription: 'single quoted'\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "quoted", name)
	require.Equal(t, "single quoted", desc)
}

func TestNilSkillSetSafe(t *testing.T) {
	var set *SkillSet
	require.Equal(t, 0, set.Len())
	require.Nil(t, set.List())
	require.Empty(t, set.Summaries())
	_, ok := set.Get("x")
	require.False(t, ok)
}
