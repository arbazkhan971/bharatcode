package diff

import (
	"os"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

func TestUnifiedHunk_Renders(t *testing.T) {
	t.Parallel()

	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old() {}\n+func new() {}\n"
	got := New(styles.Theme{}).RenderUnified(patch, 120)
	want, err := os.ReadFile("testdata/diff_unified.txt")
	require.NoError(t, err)
	require.Equal(t, strings.TrimRight(string(want), "\n"), got)
}

// TestUnifiedNumbered_Gutter checks that the numbered renderer prefixes each
// line with the right old/new line-number gutter: blank for headers, the new
// number only for added lines, the old number only for removed lines, and both
// for context lines.
func TestUnifiedNumbered_Gutter(t *testing.T) {
	t.Parallel()

	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old() {}\n+func new() {}\n"
	got := New(styles.Theme{}).RenderUnifiedNumbered(patch, 120)
	lines := strings.Split(got, "\n")

	// Single-digit line numbers => 1-wide cells, gutter "o n " is 4 chars.
	want := []string{
		"    --- a/main.go",
		"    +++ b/main.go",
		"    @@ -1,3 +1,3 @@",
		"1 1  package main",
		"2   -func old() {}",
		"  2 +func new() {}",
	}
	require.Equal(t, want, lines)
}

// TestUnifiedNumbered_GutterWidthScales checks that the gutter widens to fit the
// largest line number so columns stay aligned when a hunk starts deep in a file.
func TestUnifiedNumbered_GutterWidthScales(t *testing.T) {
	t.Parallel()

	patch := "@@ -100,2 +100,2 @@\n context\n-old\n+new\n"
	got := New(styles.Theme{}).RenderUnifiedNumbered(patch, 120)
	lines := strings.Split(got, "\n")

	// Line numbers reach 101 => 3-wide cells, gutter "ooo nnn " is 8 chars.
	require.Equal(t, "        @@ -100,2 +100,2 @@", lines[0])
	require.Equal(t, "100 100  context", lines[1])
	require.Equal(t, "101     -old", lines[2])
	require.Equal(t, "    101 +new", lines[3])
}

// TestUnifiedHeader_StyledDistinctly checks that file-boundary metadata lines
// (---, +++, diff --git, index) are rendered with the header style and not
// mistaken for added/removed content, so file boundaries stand out in a
// multi-file diff.
func TestUnifiedHeader_StyledDistinctly(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/main.go b/main.go\n" +
		"index 111..222 100644\n" +
		"--- a/main.go\n+++ b/main.go\n" +
		"@@ -1,2 +1,2 @@\n-old\n+new\n"
	theme := styles.Default()
	out := New(theme).RenderUnified(patch, 120)
	lines := strings.Split(out, "\n")

	// Map raw prefixes to their expected per-line styled rendering.
	cases := map[string]string{
		"diff --git a/main.go b/main.go": theme.DiffHeader.Render("diff --git a/main.go b/main.go"),
		"index 111..222 100644":          theme.DiffHeader.Render("index 111..222 100644"),
		"--- a/main.go":                  theme.DiffHeader.Render("--- a/main.go"),
		"+++ b/main.go":                  theme.DiffHeader.Render("+++ b/main.go"),
	}
	for raw, wantStyled := range cases {
		var found bool
		for _, ln := range lines {
			if ln == wantStyled {
				found = true
				break
			}
		}
		require.Truef(t, found, "header line %q not rendered with DiffHeader style", raw)
	}

	// The "+++"/"---" headers must not be styled as add/remove content.
	require.NotContains(t, lines, theme.DiffAdd.Render("+++ b/main.go"))
	require.NotContains(t, lines, theme.DiffRemove.Render("--- a/main.go"))

	// Actual content lines keep their add/remove styling.
	require.Contains(t, lines, theme.DiffAdd.Render("+new"))
	require.Contains(t, lines, theme.DiffRemove.Render("-old"))
}
