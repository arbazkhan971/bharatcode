package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// editDiff is the before/after view extracted from a single edit, multiedit, or
// write tool call. Path identifies the touched file; Before and After hold the
// file content (or replaced fragment) the tool changed.
type editDiff struct {
	Tool   string
	Path   string
	Before string
	After  string
}

// editPatchForToolCall builds the unified-diff patch for a single edit, write,
// or multiedit tool call directly from its raw arguments, so the live transcript
// can show the change inline as a diff the moment the tool runs — before the
// turn's messages are persisted. It reuses editDiffsFromToolUse and unifiedPatch,
// the same helpers /diff drives over stored messages, so an inline edit diff and
// the /diff view render identically. It returns "" for any non-editing tool or
// when the arguments do not decode, leaving the caller to render the plain tool
// turn instead.
func editPatchForToolCall(name string, input json.RawMessage) string {
	diffs := editDiffsFromToolUse(message.ToolUseBlock{Name: name, Input: input})
	if len(diffs) == 0 {
		return ""
	}
	return unifiedPatch(diffs)
}

// latestEditDiffs scans messages newest-first and returns the before/after
// diffs for the most recent edit, multiedit, or write tool invocation. It
// returns nil when no such tool call is present. A single multiedit yields one
// editDiff per edit so every replacement is shown.
func latestEditDiffs(msgs []message.Message) []editDiff {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.RoleAssistant {
			continue
		}
		for j := len(msgs[i].Content) - 1; j >= 0; j-- {
			use, ok := msgs[i].Content[j].(message.ToolUseBlock)
			if !ok {
				continue
			}
			if diffs := editDiffsFromToolUse(use); diffs != nil {
				return diffs
			}
		}
	}
	return nil
}

// editDiffsFromToolUse decodes an edit, multiedit, or write tool-use block into
// one or more before/after diffs. It returns nil for any other tool.
func editDiffsFromToolUse(use message.ToolUseBlock) []editDiff {
	switch use.Name {
	case "edit":
		var in struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return nil
		}
		return []editDiff{{Tool: "edit", Path: in.Path, Before: in.OldString, After: in.NewString}}
	case "write":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return nil
		}
		return []editDiff{{Tool: "write", Path: in.Path, Before: "", After: in.Content}}
	case "multiedit":
		var in struct {
			Path  string `json:"path"`
			Edits []struct {
				Old string `json:"old"`
				New string `json:"new"`
			} `json:"edits"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return nil
		}
		diffs := make([]editDiff, 0, len(in.Edits))
		for _, e := range in.Edits {
			diffs = append(diffs, editDiff{Tool: "multiedit", Path: in.Path, Before: e.Old, After: e.New})
		}
		if len(diffs) == 0 {
			return nil
		}
		return diffs
	default:
		return nil
	}
}

// unifiedPatch renders the before/after diffs as a single unified-diff-format
// string suitable for diff.Viewer.RenderUnified. Each changed file contributes
// one file header followed by one hunk per edit, computed as a real line-level
// diff: lines common to both sides render as unchanged context (a leading
// space), and only the lines that differ are marked removed ("-") or added
// ("+"). This matches how Claude Code and opencode show an edit — the
// surrounding code stays put and the eye lands on the changed lines — rather
// than blanket-replacing the whole fragment. The output is plain text; styling
// is applied by the viewer.
//
// Consecutive edits to the same file are grouped under a single file header so
// a multiedit reads as one changed file with several hunks, the way git and
// Claude Code present multiple edits to one file. Repeating the "+++" header per
// edit would otherwise make the diffstat count one file as several changed
// files. Each hunk's line numbers continue from the previous one so the gutter
// climbs monotonically through the file rather than resetting to 1 per edit.
func unifiedPatch(diffs []editDiff) string {
	var b strings.Builder
	for i := 0; i < len(diffs); {
		path := patchPath(diffs[i])
		fmt.Fprintf(&b, "--- a/%s\n", path)
		fmt.Fprintf(&b, "+++ b/%s\n", path)
		oldLine, newLine := 1, 1
		for i < len(diffs) && patchPath(diffs[i]) == path {
			before := splitLines(diffs[i].Before)
			after := splitLines(diffs[i].After)
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldLine, len(before), newLine, len(after))
			for _, line := range diffLines(before, after) {
				b.WriteString(line + "\n")
			}
			oldLine += len(before)
			newLine += len(after)
			i++
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// patchPath returns the file path used in d's diff header, substituting a
// placeholder for an unnamed edit so a missing path still renders a stable
// header and groups with other unnamed edits rather than splitting them.
func patchPath(d editDiff) string {
	if d.Path == "" {
		return "(unknown)"
	}
	return d.Path
}

// diffLines computes a line-level unified diff between before and after,
// returning each line prefixed with " " (unchanged context), "-" (removed), or
// "+" (added). It uses a longest-common-subsequence backtrace so the lines the
// two sides share are emitted once as context and only the genuinely changed
// lines carry a +/- marker. Within a contiguous change block every removed line
// is emitted before the added lines that replaced it, the ordering the diff
// viewer's word-level pairing expects.
func diffLines(before, after []string) []string {
	lcs := lcsTable(before, after)

	// Backtrace from the full sequences toward the origin, recording each step.
	// Walking back produces the diff in reverse, so the result is flipped once at
	// the end.
	var rev []string
	i, j := len(before), len(after)
	for i > 0 && j > 0 {
		switch {
		case before[i-1] == after[j-1]:
			rev = append(rev, " "+before[i-1])
			i--
			j--
		case lcs[i-1][j] >= lcs[i][j-1]:
			rev = append(rev, "-"+before[i-1])
			i--
		default:
			rev = append(rev, "+"+after[j-1])
			j--
		}
	}
	for i > 0 {
		rev = append(rev, "-"+before[i-1])
		i--
	}
	for j > 0 {
		rev = append(rev, "+"+after[j-1])
		j--
	}

	// Reverse into forward order, then normalize each change block so removed
	// lines precede added lines (the backtrace can interleave them).
	out := make([]string, len(rev))
	for k, line := range rev {
		out[len(rev)-1-k] = line
	}
	return orderChangeBlocks(out)
}

// lcsTable builds the dynamic-programming table whose cell [i][j] holds the
// length of the longest common subsequence of before[:i] and after[:j]. The
// backtrace in diffLines reads it to choose, at each step, whether a line was
// kept, removed, or added.
func lcsTable(before, after []string) [][]int {
	rows, cols := len(before)+1, len(after)+1
	table := make([][]int, rows)
	for i := range table {
		table[i] = make([]int, cols)
	}
	for i := 1; i < rows; i++ {
		for j := 1; j < cols; j++ {
			if before[i-1] == after[j-1] {
				table[i][j] = table[i-1][j-1] + 1
			} else if table[i-1][j] >= table[i][j-1] {
				table[i][j] = table[i-1][j]
			} else {
				table[i][j] = table[i][j-1]
			}
		}
	}
	return table
}

// orderChangeBlocks rewrites each maximal run of changed lines so its removed
// ("-") lines all come before its added ("+") lines, leaving context (" ")
// lines in place. The LCS backtrace can emit an add before a remove within one
// block; grouping removes ahead of adds gives the diff viewer the
// remove-then-add adjacency it relies on to pair modified lines for word-level
// emphasis, and reads the way git presents a replaced block.
func orderChangeBlocks(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if strings.HasPrefix(lines[i], " ") {
			out = append(out, lines[i])
			i++
			continue
		}
		var removed, added []string
		for i < len(lines) && !strings.HasPrefix(lines[i], " ") {
			if strings.HasPrefix(lines[i], "-") {
				removed = append(removed, lines[i])
			} else {
				added = append(added, lines[i])
			}
			i++
		}
		out = append(out, removed...)
		out = append(out, added...)
	}
	return out
}

// splitLines splits s into lines, returning an empty slice for empty input so a
// zero-line hunk count is reported rather than a single blank line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
