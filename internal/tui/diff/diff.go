// Package diff renders unified diffs for tool results and edit previews.
package diff

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
)

// Viewer renders unified diff text.
type Viewer struct {
	theme styles.Theme
}

// New constructs a diff viewer.
func New(theme styles.Theme) *Viewer {
	return &Viewer{theme: theme}
}

// hunkHeaderPattern captures the starting line numbers of a unified-diff hunk
// header, "@@ -<old>[,<count>] +<new>[,<count>] @@". Only the two start numbers
// are needed to number the lines that follow.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// RenderUnified returns a width-clamped unified diff view.
func (v *Viewer) RenderUnified(patch string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	for i, line := range lines {
		lines[i] = v.styleLine(clampWidth(expandTabs(line), width))
	}
	return strings.Join(lines, "\n")
}

// RenderUnifiedNumbered renders a unified diff like RenderUnified but prefixes
// each line with a two-column gutter showing the old- and new-file line
// numbers, matching the diff-review display of Claude Code and opencode. Hunk
// and file-boundary headers get a blank gutter; added lines show only the new
// number, removed lines only the old number, and context lines show both. The
// gutter width is reserved from width so the total stays within it.
func (v *Viewer) RenderUnifiedNumbered(patch string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")

	// Size the gutter once up front from the widest line number any row will
	// show, so every column aligns regardless of where the hunks start.
	digits := len(strconv.Itoa(lineNumberCeiling(lines)))
	gutterWidth := digits*2 + 2
	blankGutter := strings.Repeat(" ", gutterWidth)
	contentWidth := width - gutterWidth
	if contentWidth < 1 {
		contentWidth = 1
	}

	// Pair each removed line with the added line that replaced it so modified
	// lines can have just their changed runs emphasized.
	pairs := pairChanges(lines)

	var oldLn, newLn int
	inHunk := false
	out := make([]string, len(lines))
	for i, line := range lines {
		clamped := clampWidth(expandTabs(line), contentWidth)
		styled := v.styleLine(clamped)
		if j := pairs[i]; j >= 0 {
			styled = v.styleWordLine(clamped, clampWidth(expandTabs(lines[j]), contentWidth))
		}

		if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			inHunk = true
			out[i] = blankGutter + styled
			continue
		}

		switch {
		case isDiffHeader(line) || strings.HasPrefix(line, "@@") || !inHunk:
			out[i] = blankGutter + styled
		case strings.HasPrefix(line, "+"):
			out[i] = v.gutter(oldLn, newLn, digits, false, true) + styled
			newLn++
		case strings.HasPrefix(line, "-"):
			out[i] = v.gutter(oldLn, newLn, digits, true, false) + styled
			oldLn++
		default:
			out[i] = v.gutter(oldLn, newLn, digits, true, true) + styled
			oldLn++
			newLn++
		}
	}
	return strings.Join(out, "\n")
}

// Stat summarizes a unified diff as a one-line "N file(s) changed, +A -R"
// header, matching the diffstat line Claude Code and opencode show above a
// reviewed diff. Files are counted from "+++" path headers; a bare single-file
// hunk with no headers still counts as one changed file once it has content.
// Added and removed counts are the "+"/"-" content lines, excluding the
// "+++"/"---" file-boundary metadata. The "+A" segment is rendered with the
// add style and "-R" with the remove style; the rest is muted. An empty or
// content-free patch returns "".
func (v *Viewer) Stat(patch string) string {
	files, added, removed := diffStat(patch)
	if files == 0 {
		return ""
	}

	noun := "files"
	if files == 1 {
		noun = "file"
	}
	out := v.theme.Muted.Render(fmt.Sprintf("%d %s changed, ", files, noun))
	out += v.theme.DiffAdd.Render(fmt.Sprintf("+%d", added))
	out += v.theme.Muted.Render(" ")
	out += v.theme.DiffRemove.Render(fmt.Sprintf("-%d", removed))
	return out
}

// statBarWidth caps the number of "+"/"-" cells in a per-file histogram bar.
// Files whose total change count fits within it get one cell per changed line
// (so a small review shows its exact shape); larger files are scaled down to
// this width, matching how git's "--stat" bar stays bounded on wide diffs.
const statBarWidth = 32

// StatLines renders a multi-line diffstat: the aggregate Stat header followed,
// when more than one file changed, by one indented row per file showing its path,
// its own "+A -B" counts, and a git-style "+++---" histogram bar visualizing the
// relative size and add/remove ratio of the change, matching the per-file
// breakdown Claude Code and opencode show above a multi-file review. File paths
// and counts are left-aligned in shared columns so the bars start at the same
// place. A single-file or content-free patch returns just the Stat header (or ""
// when empty), so the common case is unchanged. Unnamed bare hunks are labelled
// with a muted placeholder.
func (v *Viewer) StatLines(patch string) string {
	header := v.Stat(patch)
	if header == "" {
		return ""
	}
	files := fileStats(patch)
	if len(files) <= 1 {
		return header
	}

	// Size the path column to the widest name and the count column to the widest
	// "+A -B" string so every histogram bar begins at the same offset.
	nameWidth, countWidth, maxTotal := 0, 0, 0
	for _, f := range files {
		if n := len([]rune(displayPath(f.Path))); n > nameWidth {
			nameWidth = n
		}
		if n := len(fmt.Sprintf("+%d -%d", f.Added, f.Removed)); n > countWidth {
			countWidth = n
		}
		if t := f.Added + f.Removed; t > maxTotal {
			maxTotal = t
		}
	}

	out := header
	for _, f := range files {
		name := displayPath(f.Path)
		namePad := strings.Repeat(" ", nameWidth-len([]rune(name)))
		count := fmt.Sprintf("+%d -%d", f.Added, f.Removed)
		countPad := strings.Repeat(" ", countWidth-len(count))

		row := "  " + v.theme.Muted.Render(name+namePad+"  ")
		row += v.theme.DiffAdd.Render(fmt.Sprintf("+%d", f.Added))
		row += v.theme.Muted.Render(" ")
		row += v.theme.DiffRemove.Render(fmt.Sprintf("-%d", f.Removed))

		plus, minus := scaledBar(f.Added, f.Removed, maxTotal, statBarWidth)
		if plus+minus > 0 {
			row += v.theme.Muted.Render(countPad + "  ")
			row += v.theme.DiffAdd.Render(strings.Repeat("+", plus))
			row += v.theme.DiffRemove.Render(strings.Repeat("-", minus))
		}
		out += "\n" + row
	}
	return out
}

// scaledBar splits a per-file histogram bar into its "+" (plus) and "-" (minus)
// cell counts. When the busiest file (maxTotal) already fits within width, every
// file shows one cell per changed line, so a small review reproduces its exact
// add/remove shape. On a larger diff each file's total is scaled to width in
// proportion to maxTotal, and the cells are split between adds and removes by
// their ratio; a side with any change keeps at least one cell so it never
// vanishes from the bar.
func scaledBar(added, removed, maxTotal, width int) (plus, minus int) {
	total := added + removed
	if total == 0 || maxTotal == 0 {
		return 0, 0
	}
	cells := total
	if maxTotal > width {
		cells = int(math.Round(float64(total) / float64(maxTotal) * float64(width)))
		if cells < 1 {
			cells = 1
		}
	}
	plus = int(math.Round(float64(added) / float64(total) * float64(cells)))
	if added > 0 && plus == 0 {
		plus = 1
	}
	if plus > cells {
		plus = cells
	}
	minus = cells - plus
	if removed > 0 && minus == 0 && plus > 1 {
		plus--
		minus = 1
	}
	return plus, minus
}

// displayPath returns the file name to show in a per-file diffstat row, falling
// back to a placeholder for an unnamed bare hunk.
func displayPath(p string) string {
	if p == "" {
		return "(unnamed)"
	}
	return p
}

// FileStat is the per-file portion of a diffstat: the new-file path and the
// added/removed content-line counts attributed to it.
type FileStat struct {
	Path    string
	Added   int
	Removed int
}

// diffStat counts the changed files and the added/removed content lines in a
// unified diff. It mirrors the line classification used by the renderers so the
// summary always agrees with what is shown. A patch carrying added or removed
// content but no "+++" header (a bare hunk) is reported as one changed file.
func diffStat(patch string) (files, added, removed int) {
	for _, f := range fileStats(patch) {
		files++
		added += f.Added
		removed += f.Removed
	}
	return files, added, removed
}

// fileStats breaks a unified diff into one FileStat per changed file, attributing
// added/removed content lines to the file named by the most recent "+++" header.
// The "a/"/"b/" prefix and any trailing tab metadata on the path are stripped so
// the reported name matches the working-tree path. Content that appears before
// any "+++" header (a bare hunk) is attributed to a single unnamed file so it is
// still counted once, mirroring diffStat's bare-hunk handling.
func fileStats(patch string) []FileStat {
	var files []FileStat
	cur := -1
	ensureBare := func() {
		if cur < 0 {
			files = append(files, FileStat{})
			cur = 0
		}
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
			files = append(files, FileStat{Path: cleanDiffPath(line[len("+++"):])})
			cur = len(files) - 1
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "@@"):
			// File-boundary or hunk metadata: not counted as content.
		case strings.HasPrefix(line, "+"):
			ensureBare()
			files[cur].Added++
		case strings.HasPrefix(line, "-"):
			ensureBare()
			files[cur].Removed++
		}
	}
	return files
}

// cleanDiffPath normalizes the path captured from a "---"/"+++" header: it trims
// surrounding spaces, drops any trailing tab-delimited timestamp, and removes a
// leading "a/" or "b/" prefix so the name matches the working-tree path.
func cleanDiffPath(raw string) string {
	p := strings.TrimSpace(raw)
	if tab := strings.IndexByte(p, '\t'); tab >= 0 {
		p = strings.TrimSpace(p[:tab])
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	return p
}

// gutter renders the muted two-column "old new " line-number prefix. A column
// is left blank (spaces) when its side does not apply to the line, so an added
// line shows no old number and a removed line shows no new number.
func (v *Viewer) gutter(oldN, newN, digits int, showOld, showNew bool) string {
	return v.theme.Muted.Render(numberCell(oldN, digits, showOld) + " " + numberCell(newN, digits, showNew) + " ")
}

// numberCell right-aligns n in a digits-wide field, or returns that many spaces
// when the number does not apply to the line.
func numberCell(n, digits int, show bool) string {
	if !show {
		return strings.Repeat(" ", digits)
	}
	return fmt.Sprintf("%*d", digits, n)
}

// lineNumberCeiling returns the largest old- or new-file line number that
// numbering the given unified-diff lines will produce, so the gutter can be
// sized once before rendering. It mirrors the counting in RenderUnifiedNumbered
// and returns at least 1 so the gutter is never zero-width.
func lineNumberCeiling(lines []string) int {
	var oldLn, newLn, max int
	inHunk := false
	for _, line := range lines {
		if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			inHunk = true
			continue
		}
		if isDiffHeader(line) || strings.HasPrefix(line, "@@") || !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			max = maxInt(max, newLn)
			newLn++
		case strings.HasPrefix(line, "-"):
			max = maxInt(max, oldLn)
			oldLn++
		default:
			max = maxInt(max, oldLn)
			max = maxInt(max, newLn)
			oldLn++
			newLn++
		}
	}
	if max < 1 {
		return 1
	}
	return max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// styleLine applies the per-line diff styling shared by the plain and numbered
// renderers. File-boundary metadata is matched before the +/- content cases so
// a "+++"/"---" path line is styled as a header rather than as added/removed
// content.
func (v *Viewer) styleLine(line string) string {
	switch {
	case strings.HasPrefix(line, "@@"):
		return v.styleHunkHeader(line)
	case isDiffHeader(line):
		return v.theme.DiffHeader.Render(line)
	case strings.HasPrefix(line, "+"):
		return v.theme.DiffAdd.Render(line)
	case strings.HasPrefix(line, "-"):
		return v.theme.DiffRemove.Render(line)
	default:
		return line
	}
}

// styleHunkHeader renders a "@@ -a,b +c,d @@ <section>" hunk header, coloring
// the "@@ … @@" range marker with the hunk style while rendering git's optional
// trailing section heading — the enclosing function or block git appends to
// orient the reader — in the muted style. This separates the line-range marker
// from the context label the way Claude Code and opencode do, so the reviewer's
// eye lands on the marker first and the context reads as a quiet annotation. A
// header with no trailing section (or a malformed one) keeps the whole line in
// the hunk style.
func (v *Viewer) styleHunkHeader(line string) string {
	// The range marker ends at the closing "@@"; anything past it is the
	// section heading git adds. Search after the opening "@@" so the two never
	// collide on a bare "@@@@".
	if close := strings.Index(line[2:], "@@"); close >= 0 {
		end := close + 2 + 2 // offset of the first rune past the closing "@@"
		if end < len(line) {
			return v.theme.DiffHunk.Render(line[:end]) + v.theme.Muted.Render(line[end:])
		}
	}
	return v.theme.DiffHunk.Render(line)
}

// pairChanges matches each removed line in a unified diff with the added line
// that replaced it, returning a slice the length of lines whose entry is the
// index of a line's counterpart or -1 when it has none. Within a contiguous
// change block — a run of "-" lines immediately followed by a run of "+" lines
// — the k-th removed line is paired with the k-th added line, mirroring how
// git, delta, and opencode align replaced lines for intra-line word diffing.
// Surplus lines on either side (a block that adds or removes more than it
// replaces) stay unpaired and render with their plain add/remove style.
func pairChanges(lines []string) []int {
	pairs := make([]int, len(lines))
	for i := range pairs {
		pairs[i] = -1
	}
	i := 0
	for i < len(lines) {
		if !isRemoved(lines[i]) {
			i++
			continue
		}
		rStart := i
		for i < len(lines) && isRemoved(lines[i]) {
			i++
		}
		aStart := i
		for i < len(lines) && isAdded(lines[i]) {
			i++
		}
		n := minInt(aStart-rStart, i-aStart)
		for k := 0; k < n; k++ {
			pairs[rStart+k] = aStart + k
			pairs[aStart+k] = rStart + k
		}
	}
	return pairs
}

// isRemoved reports whether line is removed diff content ("-…") rather than the
// "---" file-boundary header.
func isRemoved(line string) bool {
	return strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")
}

// isAdded reports whether line is added diff content ("+…") rather than the
// "+++" file-boundary header.
func isAdded(line string) bool {
	return strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")
}

// styleWordLine styles a modified diff line (line) against its counterpart on
// the other side of the change, emphasizing only the runs that differ. The
// leading marker and the shared head/tail keep the line's add/remove color; the
// changed middle is rendered with the emphasized variant. When the two lines
// share no common prefix or suffix the whole line changed, so there is nothing
// to single out and it falls back to the plain per-line style.
func (v *Viewer) styleWordLine(line, other string) string {
	if len(line) == 0 || len(other) == 0 {
		return v.styleLine(line)
	}
	marker := line[:1]
	if marker != other[:1] && !(isAdded(line) && isRemoved(other)) && !(isRemoved(line) && isAdded(other)) {
		return v.styleLine(line)
	}

	base, emph := v.theme.DiffAdd, v.theme.DiffAddEmph
	if isRemoved(line) {
		base, emph = v.theme.DiffRemove, v.theme.DiffRemoveEmph
	}

	prefix, mid, suffix := changedSpan(line[1:], other[1:])
	if prefix == "" && suffix == "" {
		return v.styleLine(line)
	}
	return base.Render(marker+prefix) + emph.Render(mid) + base.Render(suffix)
}

// changedSpan splits a against its counterpart b into the shared leading prefix,
// the differing middle, and the shared trailing suffix, all from a's point of
// view. Comparison is rune-wise so multi-byte content is never split mid-rune,
// and the prefix and suffix never overlap.
func changedSpan(a, b string) (prefix, mid, suffix string) {
	ar, br := []rune(a), []rune(b)
	p := 0
	for p < len(ar) && p < len(br) && ar[p] == br[p] {
		p++
	}
	sa, sb := len(ar), len(br)
	for sa > p && sb > p && ar[sa-1] == br[sb-1] {
		sa--
		sb--
	}
	return string(ar[:p]), string(ar[p:sa]), string(ar[sa:])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// clampWidth truncates line to at most width runes. When a line is cut short, an
// ellipsis replaces the final visible rune so a reviewer can tell the row was
// truncated rather than mistaking the clipped text for the whole line, matching
// how Claude Code and opencode mark wrapped-off diff content. The result still
// occupies exactly width runes so the gutter and column alignment are unchanged.
// At width 1 there is no room for both content and a marker, so the lone cell
// becomes the ellipsis.
func clampWidth(line string, width int) string {
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// diffTabStop is the column width a tab advances to when rendering a diff line.
// Diff content — Go source especially — is commonly tab-indented. A terminal
// would expand those tabs to its own tab stops, but the width clamp and the
// line-number gutter measure runes, so an unexpanded tab counts as a single
// column while occupying several, misaligning every column that follows it.
// Expanding tabs to spaces up front keeps the measured width equal to the
// displayed width, matching how Claude Code and opencode show tab-indented code.
const diffTabStop = 4

// expandTabs replaces each tab in line with enough spaces to advance to the next
// diffTabStop boundary, so the rune count of the result equals its rendered
// column width. Stops are measured from the start of the diff line — the
// "+"/"-"/" " marker is column 0 — so a reviewer reads the same indentation the
// editor shows. A line with no tab is returned unchanged so the common case
// allocates nothing.
func expandTabs(line string) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := diffTabStop - col%diffTabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// isDiffHeader reports whether line is unified-diff file-boundary metadata
// rather than content: the old/new path lines (---/+++), the git "diff --git"
// banner, or the "index" blob line. These delimit one file from the next in a
// multi-file patch and are styled distinctly from added/removed content.
func isDiffHeader(line string) bool {
	return strings.HasPrefix(line, "+++") ||
		strings.HasPrefix(line, "---") ||
		strings.HasPrefix(line, "diff --git") ||
		strings.HasPrefix(line, "index ")
}
