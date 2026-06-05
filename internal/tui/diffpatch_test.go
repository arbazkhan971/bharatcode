package tui

import (
	"strings"
	"testing"
)

// byMarker groups a single-file patch's body lines by their marker, returning
// the context (" "), removed ("-"), and added ("+") content with markers
// stripped. It reads only the hunk body so the "---"/"+++" file headers never
// leak into the removed/added buckets.
func byMarker(patch string) (context, removed, added []string) {
	for _, line := range bodyLines(patch) {
		switch {
		case strings.HasPrefix(line, " "):
			context = append(context, line[1:])
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		}
	}
	return context, removed, added
}

func TestUnifiedPatch_UnchangedLinesAreContext(t *testing.T) {
	// Only the middle line changed; the surrounding lines should render as
	// context (a leading space), not as removed-then-re-added.
	before := "alpha\nbeta\ngamma"
	after := "alpha\nBETA\ngamma"
	patch := unifiedPatch([]editDiff{{Tool: "edit", Path: "f.go", Before: before, After: after}})

	context, removed, added := byMarker(patch)
	if len(context) != 2 || context[0] != "alpha" || context[1] != "gamma" {
		t.Fatalf("expected alpha and gamma as context, got %q", context)
	}
	if len(removed) != 1 || removed[0] != "beta" {
		t.Fatalf("expected only beta removed, got %q", removed)
	}
	if len(added) != 1 || added[0] != "BETA" {
		t.Fatalf("expected only BETA added, got %q", added)
	}
}

func TestUnifiedPatch_RemovedBeforeAddedInChangeBlock(t *testing.T) {
	// A wholly replaced block must list every removed line before any added line
	// so the diff viewer can pair them for word-level emphasis.
	before := "one\ntwo"
	after := "uno\ndos"
	patch := unifiedPatch([]editDiff{{Tool: "edit", Path: "f", Before: before, After: after}})

	body := bodyLines(patch)
	firstAdd, lastRemove := -1, -1
	for i, line := range body {
		if strings.HasPrefix(line, "+") {
			if firstAdd == -1 {
				firstAdd = i
			}
		}
		if strings.HasPrefix(line, "-") {
			lastRemove = i
		}
	}
	if firstAdd == -1 || lastRemove == -1 {
		t.Fatalf("expected both adds and removes, body=%q", body)
	}
	if lastRemove > firstAdd {
		t.Fatalf("removed line appeared after an added line: %q", body)
	}
}

func TestUnifiedPatch_PureAppendKeepsPrefixAsContext(t *testing.T) {
	// Appending a line should leave the original line as context and mark only
	// the new line as added.
	patch := unifiedPatch([]editDiff{{Tool: "edit", Path: "f", Before: "keep", After: "keep\nadded"}})
	body := bodyLines(patch)
	if len(body) != 2 {
		t.Fatalf("expected two body lines, got %q", body)
	}
	if body[0] != " keep" {
		t.Fatalf("expected first body line to be context %q, got %q", " keep", body[0])
	}
	if body[1] != "+added" {
		t.Fatalf("expected second body line to be added, got %q", body[1])
	}
}

func TestUnifiedPatch_WriteHasNoContext(t *testing.T) {
	// A write (empty Before) is wholly new, so every body line is an addition.
	patch := unifiedPatch([]editDiff{{Tool: "write", Path: "f", Before: "", After: "a\nb"}})
	for _, line := range bodyLines(patch) {
		if !strings.HasPrefix(line, "+") {
			t.Fatalf("expected only additions for a write, got %q", line)
		}
	}
}

func TestUnifiedPatch_HunkCountsMatchSides(t *testing.T) {
	patch := unifiedPatch([]editDiff{{Tool: "edit", Path: "f", Before: "a\nb\nc", After: "a\nX\nc\nd"}})
	if !strings.Contains(patch, "@@ -1,3 +1,4 @@") {
		t.Fatalf("expected hunk header reflecting 3 old / 4 new lines, got:\n%s", patch)
	}
}

// bodyLines returns the diff content lines of a single-file patch: everything
// after the hunk header, which is where context/add/remove markers live.
func bodyLines(patch string) []string {
	lines := strings.Split(patch, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "@@") {
			return lines[i+1:]
		}
	}
	return nil
}
