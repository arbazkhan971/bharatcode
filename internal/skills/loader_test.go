package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeManifest creates root/<rel> with the given content, making any
// parent directories, and returns the manifest's absolute path.
func writeManifest(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestLoadSkillTreeValidAndMalformed(t *testing.T) {
	root := t.TempDir()

	// A well-formed canonical skill whose name matches its directory.
	pdfPath := writeManifest(t, root, "pdf/SKILL.md",
		"---\nname: pdf\ndescription: Fill and read PDF forms\n---\n\nUse pdftk.\n")
	// A nested skill, discovered by the recursive walk.
	writeManifest(t, root, "ops/deploy/SKILL.md",
		"---\nname: deploy\ndescription: Ship a release\n---\nbody\n")
	// disable-model-invocation is parsed into the flag.
	writeManifest(t, root, "internal-notes/SKILL.md",
		"---\nname: internal-notes\ndescription: Humans only\ndisable-model-invocation: true\n---\nbody\n")
	// A non-canonical *.md manifest is loaded and is not bound to its
	// directory name.
	writeManifest(t, root, "pack/extra.md",
		"---\nname: free-named\ndescription: Auxiliary skill\n---\nbody\n")

	// Malformed entries: each becomes a Diagnostic, none aborts the load.
	writeManifest(t, root, "no-desc/SKILL.md", "---\nname: no-desc\n---\nbody\n")
	writeManifest(t, root, "no-fm/SKILL.md", "just prose, no frontmatter\n")
	writeManifest(t, root, "unterminated/SKILL.md", "---\nname: x\ndescription: y\nbody forever")
	// Name violates [a-z0-9-]+.
	writeManifest(t, root, "bad-name/SKILL.md", "---\nname: Bad_Name\ndescription: nope\n---\nbody\n")
	// Canonical name disagrees with its directory.
	writeManifest(t, root, "wrongdir/SKILL.md", "---\nname: mismatch\ndescription: nope\n---\nbody\n")
	// A skipped VCS directory must not contribute a skill.
	writeManifest(t, root, ".git/SKILL.md", "---\nname: ghost\ndescription: hidden\n---\nbody\n")

	skills, diags, err := LoadSkillTree(root)
	require.NoError(t, err)

	// Exactly the four valid skills, in sorted name order.
	got := make([]string, len(skills))
	for i, s := range skills {
		got[i] = s.Name
	}
	require.Equal(t, []string{"deploy", "free-named", "internal-notes", "pdf"}, got)

	byName := make(map[string]LoadedSkill, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}

	pdf := byName["pdf"]
	require.Equal(t, "Fill and read PDF forms", pdf.Description)
	require.Equal(t, "Use pdftk.", pdf.Body)
	require.Equal(t, pdfPath, pdf.Source)
	require.Equal(t, filepath.Join(root, "pdf"), pdf.Dir)
	require.False(t, pdf.DisableModelInvocation)

	require.True(t, byName["internal-notes"].DisableModelInvocation)
	require.Equal(t, filepath.Join(root, "pack", "extra.md"), byName["free-named"].Source)

	// The .git ghost was pruned and never loaded.
	_, ghost := byName["ghost"]
	require.False(t, ghost)

	// Every malformed manifest produced a Diagnostic. There are five.
	require.Len(t, diags, 5)
	msgByPath := make(map[string]Diagnostic, len(diags))
	for _, d := range diags {
		msgByPath[d.Path] = d
		require.NotEmpty(t, d.Message)
		require.NotEmpty(t, d.String())
	}

	require.Contains(t, msgByPath[filepath.Join(root, "no-desc", "SKILL.md")].Message, "description")
	require.Contains(t, msgByPath[filepath.Join(root, "no-fm", "SKILL.md")].Message, "frontmatter")
	require.Contains(t, msgByPath[filepath.Join(root, "unterminated", "SKILL.md")].Message, "unterminated")

	// A malformed name and a directory mismatch are flagged as errors.
	require.Equal(t, DiagError, msgByPath[filepath.Join(root, "bad-name", "SKILL.md")].Level)
	require.Contains(t, msgByPath[filepath.Join(root, "bad-name", "SKILL.md")].Message, "[a-z0-9-]+")
	require.Equal(t, DiagError, msgByPath[filepath.Join(root, "wrongdir", "SKILL.md")].Level)
	require.Contains(t, msgByPath[filepath.Join(root, "wrongdir", "SKILL.md")].Message, "does not match directory")
}

func TestLoadSkillTreeMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	skills, diags, err := LoadSkillTree(missing)
	require.NoError(t, err)
	require.Empty(t, skills)
	require.Empty(t, diags)
}

func TestLoadSkillTreeEmptyRoot(t *testing.T) {
	skills, diags, err := LoadSkillTree("   ")
	require.NoError(t, err)
	require.Nil(t, skills)
	require.Nil(t, diags)
}

func TestLoadSkillTreeRootIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "SKILL.md")
	require.NoError(t, os.WriteFile(file, []byte("---\nname: x\ndescription: y\n---\n"), 0o644))
	_, _, err := LoadSkillTree(file)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestLoadSkillTreeReusesSummaries(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "review/SKILL.md",
		"---\nname: review\ndescription: Diff < and > & report\n---\nbody\n")

	skills, _, err := LoadSkillTree(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)

	// The embedded Skill works with the existing SkillSet renderers.
	set := &SkillSet{byName: map[string]Skill{}}
	for _, s := range skills {
		set.byName[s.Name] = s.Skill
	}
	set.finalize()
	out := set.Summaries()
	require.Contains(t, out, "Diff &lt; and &gt; &amp; report")
	require.Contains(t, out, "<location>"+filepath.Join(root, "review")+"</location>")
}

func TestParseFrontmatterTOMLishSeparator(t *testing.T) {
	// '=' separators (TOML-ish) parse alongside ':' (YAML-ish).
	meta, body, err := parseFrontmatter("---\nname = toml-skill\ndescription = uses equals\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "toml-skill", meta["name"])
	require.Equal(t, "uses equals", meta["description"])
	require.Equal(t, "body\n", body)
}

func TestParseFrontmatterColonInValue(t *testing.T) {
	// A ':' inside the value is preserved when it is the separator.
	meta, _, err := parseFrontmatter("---\nname: api\ndescription: Calls api: GET, POST\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "api", meta["name"])
	require.Equal(t, "Calls api: GET, POST", meta["description"])
}

func TestParseFrontmatterBOMQuotesAndComments(t *testing.T) {
	meta, _, err := parseFrontmatter("\ufeff---\n# a comment\nname: \"quoted\"\ndescription: 'single'\n---\nbody\n")
	require.NoError(t, err)
	require.Equal(t, "quoted", meta["name"])
	require.Equal(t, "single", meta["description"])
}

func TestParseBool(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
		err  bool
	}{
		{"", false, false},
		{"true", true, false},
		{"YES", true, false},
		{"1", true, false},
		{"on", true, false},
		{"false", false, false},
		{"no", false, false},
		{"off", false, false},
		{"maybe", false, true},
	} {
		got, err := parseBool(tc.in)
		if tc.err {
			require.Errorf(t, err, "input %q", tc.in)
			continue
		}
		require.NoErrorf(t, err, "input %q", tc.in)
		require.Equalf(t, tc.want, got, "input %q", tc.in)
	}
}

func TestDiagnosticLevelString(t *testing.T) {
	require.Equal(t, "warn", DiagWarn.String())
	require.Equal(t, "error", DiagError.String())
	require.Equal(t, "unknown", DiagnosticLevel(99).String())
}
