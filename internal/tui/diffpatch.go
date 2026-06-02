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
// string suitable for diff.Viewer.RenderUnified. Each diff contributes a file
// header and one hunk where removed lines are prefixed with "-" and added lines
// with "+". The output is plain text; styling is applied by the viewer.
func unifiedPatch(diffs []editDiff) string {
	var b strings.Builder
	for _, d := range diffs {
		path := d.Path
		if path == "" {
			path = "(unknown)"
		}
		fmt.Fprintf(&b, "--- a/%s\n", path)
		fmt.Fprintf(&b, "+++ b/%s\n", path)
		before := splitLines(d.Before)
		after := splitLines(d.After)
		fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(before), len(after))
		for _, line := range before {
			b.WriteString("-" + line + "\n")
		}
		for _, line := range after {
			b.WriteString("+" + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// splitLines splits s into lines, returning an empty slice for empty input so a
// zero-line hunk count is reported rather than a single blank line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
