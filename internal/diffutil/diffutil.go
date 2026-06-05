// Package diffutil produces compact unified diffs for tool results so the model
// sees exactly which lines an edit changed, not just a replacement count. It
// wraps go-difflib's LCS-based unified-diff generator and clamps the output so a
// large rewrite cannot flood the model's context window.
package diffutil

import (
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	// contextLines is the number of unchanged lines shown around each hunk.
	contextLines = 3
	// maxBodyLines caps the diff body. Beyond this the diff is truncated with a
	// one-line summary so an edit to a huge file stays compact.
	maxBodyLines = 160
)

// Unified returns a compact unified diff of before→after with a few lines of
// surrounding context per hunk. It returns "" when the inputs are identical or
// when no textual difference can be rendered.
//
// The conventional ---/+++ filename header is intentionally omitted: callers
// already name the file in their result, so only @@ hunk headers and the
// ±/space content lines are emitted. When the diff exceeds maxBodyLines it is
// truncated and an "N more diff line(s) omitted" notice is appended.
func Unified(before, after string) string {
	if before == after {
		return ""
	}
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:       difflib.SplitLines(before),
		B:       difflib.SplitLines(after),
		Context: contextLines,
	})
	if err != nil {
		return ""
	}
	return clamp(strings.TrimRight(out, "\n"))
}

// clamp truncates a rendered diff to maxBodyLines, appending a summary of how
// many lines were dropped. An empty or already-short diff is returned as-is.
func clamp(diff string) string {
	if diff == "" {
		return ""
	}
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxBodyLines {
		return diff
	}
	omitted := len(lines) - maxBodyLines
	kept := strings.Join(lines[:maxBodyLines], "\n")
	return fmt.Sprintf("%s\n... (%d more diff line(s) omitted)", kept, omitted)
}
