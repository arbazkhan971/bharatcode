// Package diff renders unified diffs for tool results and edit previews.
package diff

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

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
//
// Long runs of unchanged context are collapsed to a "⋯ N unchanged lines" marker
// (see planFold), keeping foldContext lines next to each change so a small edit
// inside a large shared region is not buried — the way Claude Code and opencode
// fold context in a reviewed diff. The line numbers still account for the hidden
// lines, so the gutter on either side of a fold stays accurate.
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

	// Collapse long runs of unchanged context so a small edit buried in a large
	// shared region does not bury the changed lines under scrollback.
	hidden, markers := planFold(lines)

	var oldLn, newLn int
	inHunk := false
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if n, ok := markers[i]; ok {
			// At this point newLn holds the new-file number of the first hidden
			// line (the run begins at index i), so the marker can name the range
			// it collapses to orient the reviewer.
			out = append(out, blankGutter+v.foldMarker(n, newLn, contentWidth))
		}
		// A folded context line keeps its place in the line numbering — it is
		// still a real source line — but is not drawn; the marker above stands in
		// for the whole collapsed run. Context advances both counters.
		if hidden[i] {
			oldLn++
			newLn++
			continue
		}

		clamped := clampWidth(expandTabs(line), contentWidth)
		styled := v.styleLine(clamped)
		if j := pairs[i]; j >= 0 {
			styled = v.styleWordLine(clamped, clampWidth(expandTabs(lines[j]), contentWidth))
		}

		if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			inHunk = true
			out = append(out, blankGutter+styled)
			continue
		}

		switch {
		case isDiffHeader(line) || strings.HasPrefix(line, "@@") || isNoNewlineMarker(line) || !inHunk:
			out = append(out, blankGutter+styled)
		case strings.HasPrefix(line, "+"):
			out = append(out, v.gutter(oldLn, newLn, digits, false, true)+styled)
			newLn++
		case strings.HasPrefix(line, "-"):
			out = append(out, v.gutter(oldLn, newLn, digits, true, false)+styled)
			oldLn++
		default:
			out = append(out, v.gutter(oldLn, newLn, digits, true, true)+styled)
			oldLn++
			newLn++
		}
	}
	return strings.Join(out, "\n")
}

// foldContext is how many unchanged lines the numbered diff keeps adjacent to a
// change when collapsing a longer run of context, so the reviewer still sees the
// immediate surroundings of each edit. It matches git's default three-line
// context window.
const foldContext = 3

// foldMin is the fewest hidden lines worth collapsing. Replacing one unchanged
// line with a one-line marker saves nothing, so a context run is folded only when
// at least this many lines would be hidden — keeping the fold a net reduction.
const foldMin = 2

// planFold scans the numbered diff's lines and decides which interior context
// lines to collapse, so a small change inside a large unchanged region stays
// readable. It returns hidden, a per-line flag marking the context lines the
// renderer should skip (while still advancing the line-number counters, since a
// hidden context line is still a real source line), and markers, mapping the
// index where a collapsed run begins to the number of lines it hides so the
// renderer can draw a "⋯ N unchanged lines" placeholder there.
//
// Only maximal runs of genuine in-hunk context lines are considered; headers and
// changed (+/-) lines break a run and are never hidden. A run keeps foldContext
// lines next to any change it abuts — context after the change above it and
// before the change below it — and is folded only when at least foldMin lines
// remain to hide, mirroring how Claude Code and opencode fold long unchanged
// stretches in a reviewed diff while leaving each edit's surroundings visible.
func planFold(lines []string) (hidden []bool, markers map[int]int) {
	hidden = make([]bool, len(lines))
	markers = make(map[int]int)

	// Classify each line as unchanged in-hunk context once, so the run scan and
	// the change-neighbour checks agree on what counts as context.
	isCtx := make([]bool, len(lines))
	inHunk := false
	for i, line := range lines {
		if hunkHeaderPattern.MatchString(line) {
			inHunk = true
			continue
		}
		if isDiffHeader(line) || strings.HasPrefix(line, "@@") || isNoNewlineMarker(line) || !inHunk {
			continue
		}
		if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "-") {
			isCtx[i] = true
		}
	}

	for i := 0; i < len(lines); {
		if !isCtx[i] {
			i++
			continue
		}
		start := i
		for i < len(lines) && isCtx[i] {
			i++
		}
		end := i // exclusive

		keepLeft, keepRight := 0, 0
		if start > 0 && isChangeLine(lines[start-1]) {
			keepLeft = foldContext
		}
		if end < len(lines) && isChangeLine(lines[end]) {
			keepRight = foldContext
		}
		hideStart, hideEnd := start+keepLeft, end-keepRight
		if hideEnd-hideStart < foldMin {
			continue
		}
		for k := hideStart; k < hideEnd; k++ {
			hidden[k] = true
		}
		markers[hideStart] = hideEnd - hideStart
	}
	return hidden, markers
}

// isChangeLine reports whether line is added or removed diff content, as opposed
// to context, a hunk header, or file-boundary metadata. It is used to decide
// whether a context run abuts a change and so should keep its edge lines visible.
func isChangeLine(line string) bool {
	return isAdded(line) || isRemoved(line)
}

// foldMarker renders the placeholder shown in place of a collapsed run of
// unchanged context: a muted "⋯ N unchanged line(s) (a–b)" note telling the
// reviewer how many lines were hidden and which new-file line numbers they span,
// so a fold reads as a deliberate elision the reviewer can locate rather than
// missing content. startLine is the new-file number of the first hidden line; a
// non-positive value drops the range, so a caller without numbering still gets
// the count. The text is clamped to width so it never widens the diff body.
func (v *Viewer) foldMarker(n, startLine, width int) string {
	noun := "lines"
	if n == 1 {
		noun = "line"
	}
	label := fmt.Sprintf("⋯ %d unchanged %s", n, noun)
	if startLine > 0 {
		label += fmt.Sprintf(" (%d–%d)", startLine, startLine+n-1)
	}
	return v.theme.Muted.Render(clampWidth(label, width))
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

// CountSummary returns just the colored "+A -B" change counts for a patch,
// without the "N files changed" preamble Stat prepends. It suits a caller that
// already names the file it is summarizing — the file-tree panel's per-file diff
// header — where Stat's "1 file changed" count would only restate what the
// surrounding label already says. The "+A" segment uses the add style and "-B"
// the remove style, matching Stat; an empty or content-free patch returns "".
func (v *Viewer) CountSummary(patch string) string {
	files, added, removed := diffStat(patch)
	if files == 0 {
		return ""
	}
	return v.theme.DiffAdd.Render(fmt.Sprintf("+%d", added)) +
		v.theme.Muted.Render(" ") +
		v.theme.DiffRemove.Render(fmt.Sprintf("-%d", removed))
}

// statBarWidth caps the number of "+"/"-" cells in a per-file histogram bar.
// Files whose total change count fits within it get one cell per changed line
// (so a small review shows its exact shape); larger files are scaled down to
// this width, matching how git's "--stat" bar stays bounded on wide diffs.
const statBarWidth = 32

// maxStatFiles caps how many per-file rows StatLines lists before collapsing the
// remainder into a single "… and N more files" summary, so a sprawling
// many-file review stays scannable instead of pushing the diff itself off
// screen. It mirrors git's "--stat-count" truncation; the aggregate header above
// the list still reflects every file.
const maxStatFiles = 20

// minStatPathCol is the floor the per-file diffstat path column is shrunk to
// when width forces elision: a path narrower than this would lose its base name,
// so on a very narrow terminal the rows may overrun rather than collapse the
// name into nothing. It keeps enough room for "…" plus a short base name.
const minStatPathCol = 12

// StatLines renders a multi-line diffstat: the aggregate Stat header followed,
// when more than one file changed, by one indented row per file showing its path,
// its own "+A -B" counts, and a git-style "+++---" histogram bar visualizing the
// relative size and add/remove ratio of the change, matching the per-file
// breakdown Claude Code and opencode show above a multi-file review. File paths
// and counts are left-aligned in shared columns so the bars start at the same
// place. A single-file or content-free patch returns just the Stat header (or ""
// when empty), so the common case is unchanged. Unnamed bare hunks are labelled
// with a muted placeholder.
//
// width caps the row layout so a long path cannot overrun the diff dialog and
// wrap (the diff body below is clamped the same way). The path column is sized to
// the widest name but no wider than the room left after the count column and the
// histogram bar; paths past that are elided from the left with a leading "…" so
// the base name — the part a reviewer scans for — stays visible, the way git's
// "--stat" shortens long paths. A non-positive width means unbounded, leaving the
// rows their natural width.
func (v *Viewer) StatLines(patch string, width int) string {
	header := v.Stat(patch)
	if header == "" {
		return ""
	}
	files := fileStats(patch)
	if len(files) <= 1 {
		return header
	}

	// Collapse the tail of a sprawling review into a single summary row, keeping
	// the listing bounded. The aggregate header above still reflects every file.
	overflow := 0
	if len(files) > maxStatFiles {
		overflow = len(files) - maxStatFiles
		files = files[:maxStatFiles]
	}

	// Size the count column to the widest "+A -B" string so every histogram bar
	// begins at the same offset, and find the busiest file to scale the bars.
	countWidth, maxTotal := 0, 0
	for _, f := range files {
		if n := len(countLabel(f)); n > countWidth {
			countWidth = n
		}
		if t := f.Added + f.Removed; t > maxTotal {
			maxTotal = t
		}
	}

	// Size the path column to the widest name, but cap it so the indent, count
	// column, gaps, and a full-width bar still fit within width. The fixed cost is
	// the 2-space indent, the two 2-space gaps, the count column, and the bar.
	names := make([]string, len(files))
	nameWidth := 0
	for i, f := range files {
		names[i] = statName(f)
		if n := len([]rune(names[i])); n > nameWidth {
			nameWidth = n
		}
	}
	if width > 0 {
		budget := width - (2 + 2 + countWidth + 2 + statBarWidth)
		if budget < minStatPathCol {
			budget = minStatPathCol
		}
		if nameWidth > budget {
			nameWidth = budget
			for i := range names {
				names[i] = elidePath(names[i], nameWidth)
			}
		}
	}

	out := header
	for i, f := range files {
		name := names[i]
		namePad := strings.Repeat(" ", nameWidth-len([]rune(name)))
		count := countLabel(f)
		countPad := strings.Repeat(" ", countWidth-len(count))

		row := "  " + v.theme.Muted.Render(name+namePad+"  ")
		if f.Binary || f.renamed() {
			// A binary blob or pure rename has no countable lines; show git's
			// "Bin" marker (or a "rename" marker, the "old => new" name already
			// being in the path column) in the muted style rather than a "+0 -0"
			// that would read as an empty change, and draw no histogram bar.
			row += v.theme.Muted.Render(count)
			out += "\n" + row
			continue
		}
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
	if overflow > 0 {
		noun := "files"
		if overflow == 1 {
			noun = "file"
		}
		out += "\n" + v.theme.Muted.Render(fmt.Sprintf("  … and %d more %s", overflow, noun))
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

// countLabel formats the per-file count column of a diffstat row: git's "Bin"
// marker for a binary change that carries no text lines, a "rename" marker for a
// pure rename (whose "old => new" path already shows it moved, so a "+0 -0" would
// only read as an empty change), and the "+A -B" added/removed counts otherwise.
// It is shared by the column-width sizing pass and the row renderer so both agree
// on the label's width.
func countLabel(f FileStat) string {
	switch {
	case f.Binary:
		return "Bin"
	case f.renamed():
		return "rename"
	default:
		return fmt.Sprintf("+%d -%d", f.Added, f.Removed)
	}
}

// displayPath returns the file name to show in a per-file diffstat row, falling
// back to a placeholder for an unnamed bare hunk.
func displayPath(p string) string {
	if p == "" {
		return "(unnamed)"
	}
	return p
}

// statName returns the path label shown in a per-file diffstat row: "old => new"
// for a pure rename, so the source name a reviewer needs to locate the moved file
// stays visible the way git's "--stat" shows it, and the plain destination path
// otherwise. A whole-file creation or deletion is tagged with a muted "(new)" or
// "(gone)" marker so a reviewer can tell at a glance that the file appeared or was
// removed rather than edited in place — the way Claude Code and opencode flag file
// status in a reviewed diff. A rename takes precedence, since its "old => new"
// already conveys the move.
func statName(f FileStat) string {
	if f.renamed() {
		return displayPath(f.OldPath) + " => " + displayPath(f.Path)
	}
	name := displayPath(f.Path)
	switch {
	case f.Created:
		name += " (new)"
	case f.Deleted:
		name += " (gone)"
	}
	return name
}

// elidePath shortens p to at most width runes by dropping leading path segments
// and prefixing a "…", so the base name — the part a reviewer scans for — is
// what survives, matching how git's "--stat" trims a long path. A path already
// within width is returned unchanged; a width too small for even "…x" collapses
// to a lone ellipsis. Measured and cut by rune so multi-byte names are never
// split mid-rune.
func elidePath(p string, width int) string {
	runes := []rune(p)
	if len(runes) <= width {
		return p
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

// FileStat is the per-file portion of a diffstat: the new-file path and the
// added/removed content-line counts attributed to it. Binary marks a file whose
// change carries no countable text lines — a binary blob — so the diffstat can
// still list it (git shows such files as "Bin" rather than a "+0 -0" that reads
// as an empty change). OldPath holds the pre-rename name of a pure rename (one
// that edits no content, so it carries no "+++" header); it lets the diffstat
// show "old => new" the way git's "--stat" does rather than dropping the source
// name and listing a bare "+0 -0". Created and Deleted mark a file added or
// removed whole — its pre- or post-image is "/dev/null" — so the diffstat can tag
// it "(new)"/"(gone)" rather than reading as an ordinary in-place edit.
type FileStat struct {
	Path    string
	OldPath string
	Added   int
	Removed int
	Binary  bool
	Created bool
	Deleted bool
}

// renamed reports whether f is a pure rename — one named through "rename
// from"/"rename to" metadata with a recorded source path distinct from its
// destination, carrying no content edits. Such a file is shown as "old => new"
// rather than as a content change.
func (f FileStat) renamed() bool {
	return f.OldPath != "" && f.OldPath != f.Path
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
//
// A "diff --git" block that names a file only through rename or binary metadata —
// a pure rename ("rename to X") or a binary blob ("Binary files … differ"), both
// of which carry no "+++" content header — is still counted as one changed file,
// flagged Binary so callers can show it as "Bin" rather than an empty "+0 -0".
// Such metadata is realized lazily, only when the block ends without a "+++"
// header, so a rename or binary change that also carries text content is counted
// once by its "+++" rather than twice.
//
// A deletion's "+++" header is "/dev/null" — that post-image side carries no real
// name — so the file is instead named by its "---" pre-image path, the way git's
// "--stat" lists a removed file under the name it had rather than as "/dev/null".
func fileStats(patch string) []FileStat {
	var files []FileStat
	cur := -1
	ensureBare := func() {
		if cur < 0 {
			files = append(files, FileStat{})
			cur = 0
		}
	}

	// Metadata pending for the current "diff --git" block: a rename source and
	// target and/or a binary marker, none of which emits a "+++" header. The
	// created/deleted flags come from git's "new file mode"/"deleted file mode"
	// extended headers, which name a whole-file add or remove even when the change
	// is binary (and so carries no "/dev/null" content header).
	var pendingPath, pendingOld string
	var pendingBinary, sawContent, pendingCreated, pendingDeleted bool
	// oldPath holds the most recent "---" (pre-image) path, used to name a
	// deletion whose "+++" header is "/dev/null" — that side carries no real
	// name, so the diffstat would otherwise label a deleted file "/dev/null"
	// instead of the file that was removed.
	var oldPath string
	flushMeta := func() {
		if !sawContent && (pendingBinary || pendingPath != "") {
			// A pure rename carries no content, so record its source name to show
			// "old => new"; a binary rename keeps the "Bin" marker but the source
			// name is irrelevant there, so only attach it to a non-binary rename.
			old := pendingOld
			if pendingBinary {
				old = ""
			}
			files = append(files, FileStat{Path: pendingPath, OldPath: old, Binary: pendingBinary, Created: pendingCreated, Deleted: pendingDeleted})
		}
		pendingPath, pendingOld, pendingBinary, sawContent, oldPath = "", "", false, false, ""
		pendingCreated, pendingDeleted = false, false
	}

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			flushMeta()
		case strings.HasPrefix(line, "new file mode "):
			pendingCreated = true
		case strings.HasPrefix(line, "deleted file mode "):
			pendingDeleted = true
		case strings.HasPrefix(line, "rename from "):
			pendingOld = cleanDiffPath(line[len("rename from "):])
		case strings.HasPrefix(line, "rename to "):
			pendingPath = cleanDiffPath(line[len("rename to "):])
		case strings.HasPrefix(line, "Binary files "):
			pendingBinary = true
			if p := binaryPath(line); p != "" {
				pendingPath = p
			}
		case strings.HasPrefix(line, "---"):
			// Pre-image path: remembered so a deletion's "/dev/null" post-image
			// can still be named by the file that was removed. Not content.
			oldPath = cleanDiffPath(line[len("---"):])
		case strings.HasPrefix(line, "+++"):
			p := cleanDiffPath(line[len("+++"):])
			// A "/dev/null" post-image marks a deletion; a "/dev/null" pre-image
			// (the most recent "---") marks a creation. Fold in any extended-header
			// signal so a creation/deletion is still tagged when the path headers
			// are absent.
			created := pendingCreated || oldPath == "/dev/null"
			deleted := pendingDeleted || p == "/dev/null"
			if p == "/dev/null" && oldPath != "" {
				p = oldPath
			}
			files = append(files, FileStat{Path: p, Created: created, Deleted: deleted})
			cur = len(files) - 1
			sawContent = true
		case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "@@"):
			// File-boundary or hunk metadata: not counted as content.
		case strings.HasPrefix(line, "+"):
			ensureBare()
			files[cur].Added++
		case strings.HasPrefix(line, "-"):
			ensureBare()
			files[cur].Removed++
		}
	}
	flushMeta()
	return files
}

// binaryPath extracts the changed file's path from git's "Binary files <a> and
// <b> differ" annotation. It returns the b-side (post-change) path with its
// "a/"/"b/" prefix and any trailing tab metadata stripped, falling back to the
// a-side when the b-side is /dev/null (a deleted binary), and "" when the line
// does not match — letting the caller leave the path to other metadata.
func binaryPath(line string) string {
	rest, ok := strings.CutPrefix(line, "Binary files ")
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(rest, " differ")
	a, b, ok := strings.Cut(rest, " and ")
	if !ok {
		return ""
	}
	if bp := cleanDiffPath(b); bp != "" && bp != "/dev/null" {
		return bp
	}
	return cleanDiffPath(a)
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
		if isDiffHeader(line) || strings.HasPrefix(line, "@@") || isNoNewlineMarker(line) || !inHunk {
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
	case isNoNewlineMarker(line):
		// Git's "\ No newline at end of file" annotation is metadata, not added
		// or removed content; dim it so it reads as a quiet note rather than a
		// changed line, matching how git and delta present it.
		return v.theme.Muted.Render(line)
	case strings.HasPrefix(line, "+"):
		return v.styleAddedLine(line)
	case strings.HasPrefix(line, "-"):
		return v.theme.DiffRemove.Render(line)
	default:
		return line
	}
}

// styleAddedLine renders an added content line, flagging any trailing
// whitespace the edit introduced at the line's end with the DiffWhitespace
// style so a reviewer catches the kind of whitespace error git's "diff --check"
// reports, the way delta and opencode mark introduced trailing blanks. Only
// added lines are flagged — git reports whitespace errors only on introduced
// content, and an unchanged or removed line's trailing blanks are not the
// reviewer's to fix. A line with no trailing whitespace is rendered wholly in
// the add style, so the common case is byte-for-byte unchanged. (Modified lines
// paired for word-diffing render through styleWordLine and are not flagged.)
func (v *Viewer) styleAddedLine(line string) string {
	body, trail := splitTrailingWhitespace(line)
	if trail == "" {
		return v.theme.DiffAdd.Render(line)
	}
	return v.theme.DiffAdd.Render(body) + v.theme.DiffWhitespace.Render(trail)
}

// splitTrailingWhitespace splits s into its leading content and the run of
// space and tab characters at its very end. The trailing run is empty when s
// does not end in whitespace. The "+" marker is never whitespace, so body
// always retains it. Tabs are matched as well as spaces because a line whose
// indentation reaches its end as a tab is the same whitespace error once tabs
// have been expanded to spaces for display.
func splitTrailingWhitespace(s string) (body, trailing string) {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return s[:i], s[i:]
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
// leading marker and every shared run keep the line's add/remove color; each
// changed run is rendered with the emphasized variant. The comparison is
// token-level (see wordSegments), so a line carrying several separated edits has
// each one emphasized independently rather than one span swallowing the
// unchanged text between them. When the two lines share no token there is
// nothing to single out and it falls back to the plain per-line style.
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

	segs := wordSegments(line[1:], other[1:])
	if segs == nil {
		// No token is shared, so the whole line changed and there is nothing to
		// single out; keep the plain add/remove style.
		return v.styleLine(line)
	}

	// Render shared runs (and the leading marker) in the base style and changed
	// runs in the emphasized style. Consecutive base text — the marker plus a
	// leading shared run — is coalesced into one styled span so the output stays
	// compact and a "-func " head renders as a single block.
	var b strings.Builder
	pending := marker
	flush := func() {
		if pending != "" {
			b.WriteString(base.Render(pending))
			pending = ""
		}
	}
	for _, s := range segs {
		if s.changed {
			flush()
			b.WriteString(emph.Render(s.text))
			continue
		}
		pending += s.text
	}
	flush()
	return b.String()
}

// diffSeg is one run of a word-diff: a stretch of the styled line that either
// changed relative to its counterpart or is shared with it.
type diffSeg struct {
	text    string
	changed bool
}

// wordSegments splits line a into coalesced word-diff segments against b, with
// each segment marked changed (a run present in a but not aligned to b) or
// shared. Both inputs are the diff content with the leading +/-/space marker
// already stripped. Tokens are maximal runs of word characters (letters, digits,
// underscore) or of other characters, aligned by a longest-common-subsequence so
// unchanged tokens sitting between two edits stay shared — letting a line with
// several separated edits emphasize each one rather than one span swallowing the
// text between them, the way git --word-diff, delta, and opencode highlight
// intra-line changes. It returns nil when a and b share no token, signalling the
// caller to fall back to a plain whole-line style.
func wordSegments(a, b string) []diffSeg {
	at := tokenizeWords(a)
	common := commonTokens(at, tokenizeWords(b))
	shared := false
	for _, c := range common {
		if c {
			shared = true
			break
		}
	}
	if !shared {
		return nil
	}
	var segs []diffSeg
	for i, tok := range at {
		changed := !common[i]
		if n := len(segs); n > 0 && segs[n-1].changed == changed {
			segs[n-1].text += tok
			continue
		}
		segs = append(segs, diffSeg{text: tok, changed: changed})
	}
	return segs
}

// tokenizeWords splits s into maximal runs of word characters (letters, digits,
// underscore) and runs of everything else, so an identifier, an operator, or a
// stretch of whitespace each becomes one token the word-diff can align. Splitting
// is rune-wise so a multi-byte character is never cut in half.
func tokenizeWords(s string) []string {
	runes := []rune(s)
	var toks []string
	for i := 0; i < len(runes); {
		word := isWordRune(runes[i])
		j := i + 1
		for j < len(runes) && isWordRune(runes[j]) == word {
			j++
		}
		toks = append(toks, string(runes[i:j]))
		i = j
	}
	return toks
}

// isWordRune reports whether r is part of a word token (a letter, digit, or
// underscore) rather than punctuation or whitespace.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// commonTokens reports, for each token of a, whether it belongs to a
// longest-common-subsequence alignment with b. Tokens marked true are shared
// (rendered plain); the rest changed (emphasized). A standard LCS dynamic program
// fills the table, then a forward walk marks the matched a-indices, so unchanged
// tokens between edits stay shared regardless of how the edits move around them.
func commonTokens(a, b []string) []bool {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	common := make([]bool, n)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			common[i] = true
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return common
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

// extendedHeaderPrefixes are the git "extended header" lines that sit between
// the "diff --git" banner and the first hunk, describing how a file changed
// (created, deleted, renamed, copied, or had its mode altered) without carrying
// any +/- content. Git, delta, and opencode style these as part of the file
// header rather than leaving them as plain text; matching them here lets the
// renderers do the same. Each lacks a +/-/space marker, so it can never be
// confused with hunk content.
var extendedHeaderPrefixes = []string{
	"new file mode ",
	"deleted file mode ",
	"old mode ",
	"new mode ",
	"similarity index ",
	"dissimilarity index ",
	"rename from ",
	"rename to ",
	"copy from ",
	"copy to ",
}

// isDiffHeader reports whether line is unified-diff file-boundary metadata
// rather than content: the old/new path lines (---/+++), the git "diff --git"
// banner, the "index" blob line, the binary-change markers ("Binary files …
// differ" and the "GIT binary patch" header), or one of git's extended header
// lines (mode changes, rename/copy metadata, similarity index). These delimit
// one file from the next in a multi-file patch and are styled distinctly from
// added/removed content. Recognizing the binary markers here keeps a binary
// change — which carries no +/- hunk content — styled as the file metadata it
// is rather than rendering as a plain, uncolored line the gutter would also
// mis-number, matching how git, delta, and opencode present a binary diff.
func isDiffHeader(line string) bool {
	if strings.HasPrefix(line, "+++") ||
		strings.HasPrefix(line, "---") ||
		strings.HasPrefix(line, "diff --git") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "GIT binary patch") ||
		(strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ")) {
		return true
	}
	for _, p := range extendedHeaderPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// isNoNewlineMarker reports whether line is git's "\ No newline at end of file"
// annotation, which trails a +/- line whose file lacks a final newline. It is
// metadata rather than numbered content, so the renderers give it a blank
// gutter and the line-number counters skip over it; otherwise it would be
// mistaken for a context line, drawing a bogus line number and shifting every
// number that follows.
func isNoNewlineMarker(line string) bool {
	return strings.HasPrefix(line, "\\ ")
}
