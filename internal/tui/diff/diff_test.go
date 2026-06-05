package diff

import (
	"fmt"
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

// TestUnifiedNumbered_WordHighlight checks that a modified line has only its
// changed run rendered with the emphasized add/remove style while the shared
// head and tail keep the plain add/remove style, matching the intra-line
// word-diff highlighting of Claude Code and opencode.
func TestUnifiedNumbered_WordHighlight(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,1 +1,1 @@\n-func old() {}\n+func new() {}\n"
	got := New(theme).RenderUnifiedNumbered(patch, 120)

	// The shared prefix and suffix keep the plain colors; only the differing
	// word is emphasized on each side.
	require.Contains(t, got, theme.DiffRemove.Render("-func "))
	require.Contains(t, got, theme.DiffRemoveEmph.Render("old"))
	require.Contains(t, got, theme.DiffAddEmph.Render("new"))
	require.Contains(t, got, theme.DiffAdd.Render("() {}"))
}

// TestUnifiedNumbered_WordHighlight_NoSharedAffix checks that when the removed
// and added lines share no common prefix or suffix there is nothing to single
// out, so the whole line keeps its plain add/remove style rather than being
// rendered as one big emphasized block.
func TestUnifiedNumbered_WordHighlight_NoSharedAffix(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,1 +1,1 @@\n-xyz\n+abc\n"
	got := New(theme).RenderUnifiedNumbered(patch, 120)

	require.Contains(t, got, theme.DiffRemove.Render("-xyz"))
	require.Contains(t, got, theme.DiffAdd.Render("+abc"))
	require.NotContains(t, got, theme.DiffRemoveEmph.Render("xyz"))
	require.NotContains(t, got, theme.DiffAddEmph.Render("abc"))
}

// TestUnifiedNumbered_WordHighlight_UnpairedLines checks that added or removed
// lines without a counterpart on the other side (an unbalanced change block)
// are left with their plain style, since there is no line to diff them against.
func TestUnifiedNumbered_WordHighlight_UnpairedLines(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	// One removal replaced by two additions: the first add pairs with the
	// removal, the second add is surplus and must stay plain.
	patch := "@@ -1,1 +1,2 @@\n-alpha one\n+alpha two\n+brand new line\n"
	got := New(theme).RenderUnifiedNumbered(patch, 120)

	require.Contains(t, got, theme.DiffAddEmph.Render("two"))
	require.Contains(t, got, theme.DiffAdd.Render("+brand new line"))
}

// TestChangedSpan_SplitsSharedAffixes checks the rune-wise prefix/middle/suffix
// split that drives word highlighting, including a multi-byte case so a rune is
// never cut in half.
func TestChangedSpan_SplitsSharedAffixes(t *testing.T) {
	t.Parallel()

	prefix, mid, suffix := changedSpan("func old() {}", "func new() {}")
	require.Equal(t, "func ", prefix)
	require.Equal(t, "old", mid)
	require.Equal(t, "() {}", suffix)

	// No shared affix: the whole string is the middle.
	prefix, mid, suffix = changedSpan("xyz", "abc")
	require.Equal(t, "", prefix)
	require.Equal(t, "xyz", mid)
	require.Equal(t, "", suffix)

	// Multi-byte runes stay intact around the change.
	prefix, mid, suffix = changedSpan("café au lait", "café con leche")
	require.Equal(t, "café ", prefix)
	require.Equal(t, "au lait", mid)
	require.Equal(t, "", suffix)
}

// TestPairChanges_MatchesWithinBlocks checks that removed lines are paired with
// the added lines that replaced them, index-for-index within a contiguous
// change block, and that surplus or stand-alone lines stay unpaired (-1).
func TestPairChanges_MatchesWithinBlocks(t *testing.T) {
	t.Parallel()

	lines := []string{
		"@@ -1,2 +1,2 @@",
		" context",
		"-old a",
		"-old b",
		"+new a",
		"+new b",
		"+extra add", // surplus: no removed counterpart
	}
	pairs := pairChanges(lines)
	require.Equal(t, []int{-1, -1, 4, 5, 2, 3, -1}, pairs)
}

// TestUnifiedHunkHeader_SectionHeadingMuted checks that git's trailing section
// heading on a hunk header ("@@ … @@ func foo()") is rendered in the muted style
// while the "@@ … @@" range marker keeps the hunk style, so the context label
// reads as a quiet annotation rather than competing with the marker.
func TestUnifiedHunkHeader_SectionHeadingMuted(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,3 +1,3 @@ func foo() {\n context\n-old\n+new\n"
	got := New(theme).RenderUnified(patch, 120)
	header := strings.Split(got, "\n")[0]

	require.Equal(t, theme.DiffHunk.Render("@@ -1,3 +1,3 @@")+theme.Muted.Render(" func foo() {"), header)
}

// TestUnifiedHunkHeader_NoSection checks that a header with no trailing section
// keeps the whole line in the hunk style, so the common case is unchanged.
func TestUnifiedHunkHeader_NoSection(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,3 +1,3 @@\n context\n-old\n+new\n"
	got := New(theme).RenderUnified(patch, 120)
	header := strings.Split(got, "\n")[0]

	require.Equal(t, theme.DiffHunk.Render("@@ -1,3 +1,3 @@"), header)
}

// TestStat_CountsFilesAndLines checks that Stat reports the changed-file count
// and the added/removed content totals, excluding the +++/--- file-boundary
// headers from the line counts.
func TestStat_CountsFilesAndLines(t *testing.T) {
	t.Parallel()

	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old() {}\n+func new() {}\n" +
		"--- a/util.go\n+++ b/util.go\n@@ -1,1 +1,2 @@\n+// added\n+func helper() {}\n"
	got := New(styles.Theme{}).Stat(patch)
	require.Equal(t, "2 files changed, +3 -1", got)
}

// TestStat_SingularFile checks the noun is singular for a one-file diff and that
// a bare hunk with no +++ header still counts as one changed file.
func TestStat_SingularFile(t *testing.T) {
	t.Parallel()

	patch := "@@ -1,2 +1,2 @@\n context\n-old\n+new\n"
	got := New(styles.Theme{}).Stat(patch)
	require.Equal(t, "1 file changed, +1 -1", got)
}

// TestStat_EmptyPatch checks that a patch with no content yields no summary, so
// the diff view does not lead with an empty header.
func TestStat_EmptyPatch(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", New(styles.Theme{}).Stat(""))
}

// TestStat_StylesSegments checks the +A and -R segments are rendered with the
// add and remove styles so they stand out in the summary.
func TestStat_StylesSegments(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	got := New(theme).Stat(patch)
	require.Contains(t, got, theme.DiffAdd.Render("+1"))
	require.Contains(t, got, theme.DiffRemove.Render("-1"))
}

// TestStatLines_PerFileBreakdown checks that a multi-file patch yields the
// aggregate header plus one indented row per file with that file's own +A -B
// counts and its working-tree path (the a/ b/ prefix stripped).
func TestStatLines_PerFileBreakdown(t *testing.T) {
	t.Parallel()

	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old() {}\n+func new() {}\n" +
		"--- a/util.go\n+++ b/util.go\n@@ -1,1 +1,2 @@\n+// added\n+func helper() {}\n"
	got := New(styles.Theme{}).StatLines(patch, 0)
	lines := strings.Split(got, "\n")

	require.Equal(t, "2 files changed, +3 -1", lines[0])
	require.Equal(t, "  main.go  +1 -1  +-", lines[1])
	require.Equal(t, "  util.go  +2 -0  ++", lines[2])
	require.Len(t, lines, 3)
}

// TestStatLines_SingleFileIsHeaderOnly checks that a one-file patch returns just
// the aggregate header, with no per-file row, so the common case is unchanged.
func TestStatLines_SingleFileIsHeaderOnly(t *testing.T) {
	t.Parallel()

	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	got := New(styles.Theme{}).StatLines(patch, 0)
	require.Equal(t, "1 file changed, +1 -1", got)
}

// TestStatLines_AlignsPaths checks that per-file rows pad the path column to the
// widest name so the +A -B counts line up.
func TestStatLines_AlignsPaths(t *testing.T) {
	t.Parallel()

	patch := "--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-x\n+y\n" +
		"--- a/longer.go\n+++ b/longer.go\n@@ -0,0 +1,1 @@\n+z\n"
	got := New(styles.Theme{}).StatLines(patch, 0)
	lines := strings.Split(got, "\n")

	require.Equal(t, "  a.go       +1 -1  +-", lines[1])
	require.Equal(t, "  longer.go  +1 -0  +", lines[2])
}

// TestStatLines_BarScalesAndBoundsWidth checks that a file far larger than
// statBarWidth has its histogram bar scaled down to that width while a tiny file
// in the same patch keeps at least one cell, so the bar stays bounded yet never
// drops a changed side.
func TestStatLines_BarScalesAndBoundsWidth(t *testing.T) {
	t.Parallel()

	var big strings.Builder
	big.WriteString("--- a/big.go\n+++ b/big.go\n@@ -0,0 +1,40 @@\n")
	for i := 0; i < 40; i++ {
		big.WriteString("+line\n")
	}
	patch := big.String() + "--- a/tiny.go\n+++ b/tiny.go\n@@ -0,0 +1,1 @@\n+x\n"

	lines := strings.Split(New(styles.Theme{}).StatLines(patch, 0), "\n")
	require.Len(t, lines, 3)

	bigBar := barOf(t, lines[1])
	tinyBar := barOf(t, lines[2])
	require.Equal(t, statBarWidth, len(bigBar), "large file's bar should be clamped to statBarWidth")
	require.Equal(t, strings.Repeat("+", statBarWidth), bigBar)
	require.Equal(t, "+", tinyBar, "tiny file should keep one cell")
}

// TestStatLines_BarsAlignAcrossCountWidths checks that files whose "+A -B" count
// strings differ in width still have their histogram bars begin at the same
// column, because the count column is padded to the widest entry.
func TestStatLines_BarsAlignAcrossCountWidths(t *testing.T) {
	t.Parallel()

	var wide strings.Builder
	wide.WriteString("--- a/wide.go\n+++ b/wide.go\n@@ -0,0 +1,10 @@\n")
	for i := 0; i < 10; i++ {
		wide.WriteString("+line\n")
	}
	patch := wide.String() + "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,1 @@\n-a\n+b\n"

	lines := strings.Split(New(styles.Theme{}).StatLines(patch, 0), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, barStart(lines[1]), barStart(lines[2]), "bars should start at the same column")
}

// TestStatLines_ElidesLongPathToWidth checks that when a path is too long to fit
// the given width, the row is shortened from the left with a leading "…" so the
// base name survives and the rendered row stays within width — keeping the
// per-file summary from wrapping past the diff dialog.
func TestStatLines_ElidesLongPathToWidth(t *testing.T) {
	t.Parallel()

	long := "internal/very/deeply/nested/package/handler.go"
	patch := "--- a/" + long + "\n+++ b/" + long + "\n@@ -1,1 +1,1 @@\n-x\n+y\n" +
		"--- a/short.go\n+++ b/short.go\n@@ -0,0 +1,1 @@\n+z\n"

	const width = 40
	lines := strings.Split(New(styles.Theme{}).StatLines(patch, width), "\n")
	require.Len(t, lines, 3)

	// The long path is elided from the left but keeps its base name.
	require.Contains(t, lines[1], "…")
	require.Contains(t, lines[1], "handler.go")
	require.NotContains(t, lines[1], "internal/very")

	// Every row fits within the requested width.
	for _, row := range lines[1:] {
		require.LessOrEqual(t, len([]rune(row)), width, "row should fit width: %q", row)
	}
}

// TestStatLines_NoElisionWhenUnbounded checks that a non-positive width leaves
// long paths intact, so the unbounded call path is byte-for-byte unchanged.
func TestStatLines_NoElisionWhenUnbounded(t *testing.T) {
	t.Parallel()

	long := "internal/very/deeply/nested/package/handler.go"
	patch := "--- a/" + long + "\n+++ b/" + long + "\n@@ -1,1 +1,1 @@\n-x\n+y\n" +
		"--- a/short.go\n+++ b/short.go\n@@ -0,0 +1,1 @@\n+z\n"

	lines := strings.Split(New(styles.Theme{}).StatLines(patch, 0), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[1], long)
	require.NotContains(t, lines[1], "…")
}

// TestElidePath_KeepsTail checks the path-shortening helper keeps the trailing
// runes behind a leading ellipsis and collapses to a lone "…" when there is no
// room for even one tail rune.
func TestElidePath_KeepsTail(t *testing.T) {
	t.Parallel()

	require.Equal(t, "abc.go", elidePath("abc.go", 10), "short path is unchanged")
	require.Equal(t, "…e.go", elidePath("longname.go", 5), "long path keeps tail behind ellipsis")
	require.Equal(t, "…", elidePath("longname.go", 1), "no room for a tail collapses to ellipsis")
}

// barOf extracts the trailing run of "+"/"-" histogram cells from a per-file
// diffstat row (everything after the last run of spaces).
func barOf(t *testing.T, row string) string {
	t.Helper()
	fields := strings.Fields(row)
	return fields[len(fields)-1]
}

// barStart returns the rune index where the histogram bar begins: the start of
// the final "+/-" run in the row.
func barStart(row string) int {
	runes := []rune(row)
	i := len(runes)
	for i > 0 && (runes[i-1] == '+' || runes[i-1] == '-') {
		i--
	}
	return i
}

// TestStatLines_CapsFileRows checks that a review with more files than
// maxStatFiles lists only that many per-file rows and collapses the rest into a
// single "… and N more files" summary, while the aggregate header still counts
// every file.
func TestStatLines_CapsFileRows(t *testing.T) {
	t.Parallel()

	const extra = 5
	var b strings.Builder
	for i := 0; i < maxStatFiles+extra; i++ {
		fmt.Fprintf(&b, "--- a/f%02d.go\n+++ b/f%02d.go\n@@ -1,1 +1,1 @@\n-old\n+new\n", i, i)
	}

	lines := strings.Split(New(styles.Theme{}).StatLines(b.String(), 0), "\n")

	// 1 header + maxStatFiles rows + 1 overflow summary.
	require.Len(t, lines, 1+maxStatFiles+1)
	require.Equal(t, fmt.Sprintf("%d files changed, +%d -%d", maxStatFiles+extra, maxStatFiles+extra, maxStatFiles+extra), lines[0])
	require.Equal(t, fmt.Sprintf("  … and %d more files", extra), lines[len(lines)-1])
}

// TestStatLines_OverflowSingularNoun checks that an overflow of exactly one file
// uses the singular noun.
func TestStatLines_OverflowSingularNoun(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 0; i < maxStatFiles+1; i++ {
		fmt.Fprintf(&b, "--- a/f%02d.go\n+++ b/f%02d.go\n@@ -1,1 +1,1 @@\n-old\n+new\n", i, i)
	}

	lines := strings.Split(New(styles.Theme{}).StatLines(b.String(), 0), "\n")
	require.Equal(t, "  … and 1 more file", lines[len(lines)-1])
}

// TestStatLines_NoOverflowAtCap checks that a review with exactly maxStatFiles
// files lists them all with no summary row.
func TestStatLines_NoOverflowAtCap(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 0; i < maxStatFiles; i++ {
		fmt.Fprintf(&b, "--- a/f%02d.go\n+++ b/f%02d.go\n@@ -1,1 +1,1 @@\n-old\n+new\n", i, i)
	}

	lines := strings.Split(New(styles.Theme{}).StatLines(b.String(), 0), "\n")
	require.Len(t, lines, 1+maxStatFiles)
	require.NotContains(t, lines[len(lines)-1], "more file")
}

// TestStatLines_EmptyPatch checks that a content-free patch yields no summary.
func TestStatLines_EmptyPatch(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", New(styles.Theme{}).StatLines("", 0))
}

// TestStat_CountsBinaryAndRename proves a binary blob and a pure rename — neither
// of which carries a "+++" content header — are still counted as changed files in
// the aggregate header, so a review that touches only binary assets or renames no
// longer reports "0 files changed".
func TestStat_CountsBinaryAndRename(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/logo.png b/logo.png\nindex e69de29..d95f3ad 100644\nBinary files a/logo.png and b/logo.png differ\n" +
		"diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n" +
		"diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	got := New(styles.Theme{}).Stat(patch)
	require.Equal(t, "3 files changed, +1 -1", got)
}

// TestStatLines_BinaryShowsBinMarker proves a binary file row shows git's "Bin"
// marker (with no histogram bar) instead of a "+0 -0" that would read as an empty
// change, a pure rename keeps its accurate zero counts, and a text file in the
// same patch keeps its counts and bar.
func TestStatLines_BinaryShowsBinMarker(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/logo.png b/logo.png\nBinary files a/logo.png and b/logo.png differ\n" +
		"diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n" +
		"diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	got := New(styles.Theme{}).StatLines(patch, 0)
	lines := strings.Split(got, "\n")

	require.Equal(t, "3 files changed, +1 -1", lines[0])
	require.Equal(t, "  logo.png  Bin", lines[1])
	require.Equal(t, "  new.go    +0 -0", lines[2])
	require.Equal(t, "  main.go   +1 -1  +-", lines[3])
	require.Len(t, lines, 4)
}

// TestStat_RenameWithContentCountedOnce proves a rename that also edits the file
// (so the block carries both "rename to" metadata and a "+++" content header) is
// counted once, by its content header, rather than twice.
func TestStat_RenameWithContentCountedOnce(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/old.go b/new.go\nsimilarity index 80%\nrename from old.go\nrename to new.go\n" +
		"--- a/old.go\n+++ b/new.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	got := New(styles.Theme{}).Stat(patch)
	require.Equal(t, "1 file changed, +1 -1", got)
}

// TestBinaryPath_FallsBackToASideForDeletedBlob proves the binary file path is
// read from the post-change (b) side, falling back to the a side when the b side
// is /dev/null (a deleted binary), so the row never displays "/dev/null".
func TestBinaryPath_FallsBackToASideForDeletedBlob(t *testing.T) {
	t.Parallel()

	require.Equal(t, "logo.png", binaryPath("Binary files a/logo.png and /dev/null differ"))
	require.Equal(t, "logo.png", binaryPath("Binary files a/logo.png and b/logo.png differ"))
	require.Equal(t, "", binaryPath("not a binary line"))
}

// TestStatLines_DeletedFileNamedByPreImage proves a deleted file — whose "+++"
// header is "/dev/null" — is listed under its real pre-image ("---") name rather
// than as "/dev/null", the way git's "--stat" names a removed file. Its removed
// lines are still counted, and a co-changed file in the same patch is unaffected.
func TestStatLines_DeletedFileNamedByPreImage(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/gone.go b/gone.go\ndeleted file mode 100644\nindex abc1234..0000000\n--- a/gone.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n" +
		"diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"

	files := fileStats(patch)
	require.Len(t, files, 2)
	require.Equal(t, "gone.go", files[0].Path)
	require.Equal(t, 2, files[0].Removed)
	require.Equal(t, 0, files[0].Added)
	require.Equal(t, "main.go", files[1].Path)

	// The rendered row carries the real name, never "/dev/null".
	got := New(styles.Theme{}).StatLines(patch, 0)
	require.Contains(t, got, "gone.go")
	require.NotContains(t, got, "/dev/null")
}

// TestUnified_TruncatesWithEllipsis checks that a line wider than the render
// width is clipped with a trailing ellipsis (not silently cut) and that the
// result still fits within the width, so a reviewer can tell content was
// dropped.
func TestUnified_TruncatesWithEllipsis(t *testing.T) {
	t.Parallel()

	patch := "+abcdefghij\n"
	got := New(styles.Theme{}).RenderUnified(patch, 5)
	require.Equal(t, "+abc…", got)
	require.Equal(t, 5, len([]rune(got)))
}

// TestUnified_NoTruncationWhenFits checks that a line at or under the width is
// left untouched, so the ellipsis only appears when content is actually lost.
func TestUnified_NoTruncationWhenFits(t *testing.T) {
	t.Parallel()

	patch := "+abc\n"
	got := New(styles.Theme{}).RenderUnified(patch, 5)
	require.Equal(t, "+abc", got)
}

// TestUnified_TruncateWidthOne checks the degenerate width-1 case yields a lone
// ellipsis rather than panicking or dropping the marker.
func TestUnified_TruncateWidthOne(t *testing.T) {
	t.Parallel()

	patch := "+abc\n"
	got := New(styles.Theme{}).RenderUnified(patch, 1)
	require.Equal(t, "…", got)
}

// TestExpandTabs_AdvancesToStops checks that tabs expand to the next
// diffTabStop boundary measured from column 0, so the rune count matches the
// rendered column width, and that a tab-free string is returned unchanged.
func TestExpandTabs_AdvancesToStops(t *testing.T) {
	t.Parallel()

	// A leading tab on a "+" line: the marker is column 0, so the tab advances
	// from column 1 to the next stop (column 4) => 3 spaces.
	require.Equal(t, "+   code", expandTabs("+\tcode"))
	// A tab at a stop boundary advances a full stop's worth.
	require.Equal(t, "    x", expandTabs("\tx"))
	// Two leading tabs => two indent levels.
	require.Equal(t, "+       x", expandTabs("+\t\tx"))
	// No tab: returned verbatim.
	require.Equal(t, "+plain", expandTabs("+plain"))
}

// TestUnified_ExpandsTabs checks that tab-indented diff content is rendered with
// spaces so its measured width matches what the terminal shows, keeping the
// width clamp accurate for tab-indented code.
func TestUnified_ExpandsTabs(t *testing.T) {
	t.Parallel()

	patch := "+\tx\n"
	got := New(styles.Theme{}).RenderUnified(patch, 120)
	require.Equal(t, "+   x", got)
}

// TestUnifiedNumbered_ExpandsTabsKeepsGutterAligned checks that tab expansion in
// the numbered renderer leaves the line-number gutter intact and produces a
// content width equal to its displayed column width.
func TestUnifiedNumbered_ExpandsTabsKeepsGutterAligned(t *testing.T) {
	t.Parallel()

	patch := "@@ -1,1 +1,2 @@\n context\n+\tindented\n"
	got := New(styles.Theme{}).RenderUnifiedNumbered(patch, 120)
	lines := strings.Split(got, "\n")

	// Gutter unchanged; the leading tab on the added line became spaces.
	require.Equal(t, "  2 +   indented", lines[2])
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

// TestUnifiedNumbered_NoNewlineMarkerNotNumbered checks that git's "\ No newline
// at end of file" annotation gets a blank gutter and does not advance the
// line-number counters, so the lines following it keep their correct numbers
// instead of being shifted by one.
func TestUnifiedNumbered_NoNewlineMarkerNotNumbered(t *testing.T) {
	t.Parallel()

	// The removed line ends the old file without a trailing newline; the marker
	// trails it. A context line follows so we can check its number is intact.
	patch := "@@ -1,3 +1,3 @@\n-old\n\\ No newline at end of file\n+new\n context\n more\n"
	got := New(styles.Theme{}).RenderUnifiedNumbered(patch, 120)
	lines := strings.Split(got, "\n")

	want := []string{
		"    @@ -1,3 +1,3 @@",
		"1   -old",
		"    \\ No newline at end of file",
		"  1 +new",
		"2 2  context",
		"3 3  more",
	}
	require.Equal(t, want, lines)
}

// TestUnified_NoNewlineMarkerMuted checks that the "\ No newline at end of file"
// annotation is dimmed rather than styled as added or removed content, so it
// reads as a quiet note in the diff.
func TestUnified_NoNewlineMarkerMuted(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,1 +1,1 @@\n-old\n\\ No newline at end of file\n+new\n"
	out := New(theme).RenderUnified(patch, 120)
	lines := strings.Split(out, "\n")

	marker := "\\ No newline at end of file"
	require.Contains(t, lines, theme.Muted.Render(marker))
	require.NotContains(t, lines, theme.DiffRemove.Render(marker))
	require.NotContains(t, lines, theme.DiffAdd.Render(marker))
}

// TestUnified_FlagsAddedTrailingWhitespace checks that trailing whitespace an
// added line introduces is rendered with the DiffWhitespace style rather than
// folded into the plain add color, so a reviewer sees the introduced blank run
// the way git's "diff --check" flags it.
func TestUnified_FlagsAddedTrailingWhitespace(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -0,0 +1,1 @@\n+hello   \n"
	out := New(theme).RenderUnified(patch, 120)

	// The content keeps the add color; only the trailing blanks get the
	// whitespace-error style, and the whole line is never rendered plain-add.
	require.Contains(t, out, theme.DiffAdd.Render("+hello")+theme.DiffWhitespace.Render("   "))
	require.NotContains(t, out, theme.DiffAdd.Render("+hello   "))
}

// TestUnifiedNumbered_FlagsAddedTrailingWhitespace checks the numbered renderer
// flags an introduced trailing-whitespace run the same way the plain renderer
// does, so the diff-review display marks it too.
func TestUnifiedNumbered_FlagsAddedTrailingWhitespace(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -0,0 +1,1 @@\n+hello   \n"
	out := New(theme).RenderUnifiedNumbered(patch, 120)

	require.Contains(t, out, theme.DiffAdd.Render("+hello")+theme.DiffWhitespace.Render("   "))
}

// TestUnified_TrailingWhitespaceOnlyOnAddedLines checks that trailing
// whitespace on context and removed lines is left untouched — git reports a
// whitespace error only for introduced content, so an unchanged or removed
// line's blanks are not the reviewer's to fix.
func TestUnified_TrailingWhitespaceOnlyOnAddedLines(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -1,2 +1,1 @@\n context  \n-removed  \n"
	out := New(theme).RenderUnified(patch, 120)

	require.NotContains(t, out, theme.DiffWhitespace.Render("  "))
	require.Contains(t, out, theme.DiffRemove.Render("-removed  "))
}

// TestStyleLine_NoTrailingWhitespaceUnchanged checks that an added line without
// trailing whitespace renders byte-for-byte as the plain add style, so the
// common case is untouched by the whitespace flagging.
func TestStyleLine_NoTrailingWhitespaceUnchanged(t *testing.T) {
	t.Parallel()

	theme := styles.Default()
	patch := "@@ -0,0 +1,1 @@\n+clean line\n"
	out := New(theme).RenderUnified(patch, 120)

	require.Contains(t, out, theme.DiffAdd.Render("+clean line"))
}

// TestSplitTrailingWhitespace checks the helper isolates the trailing run of
// spaces and tabs while preserving the marker and content.
func TestSplitTrailingWhitespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, body, trail string
	}{
		{"+code", "+code", ""},
		{"+code  ", "+code", "  "},
		{"+code\t", "+code", "\t"},
		{"+code \t ", "+code", " \t "},
		{"+", "+", ""},
		{"+   ", "+", "   "},
	}
	for _, c := range cases {
		body, trail := splitTrailingWhitespace(c.in)
		require.Equal(t, c.body, body, "body for %q", c.in)
		require.Equal(t, c.trail, trail, "trail for %q", c.in)
	}
}
