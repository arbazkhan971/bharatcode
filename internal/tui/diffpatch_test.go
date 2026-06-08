package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEditPatchForToolCall_EditYieldsRedGreenHunk asserts the live-path helper
// builds the same kind of patch /diff shows for an edit: the changed line appears
// once as a removal and once as an addition, with the unchanged surroundings as
// context.
func TestEditPatchForToolCall_EditYieldsRedGreenHunk(t *testing.T) {
	input := json.RawMessage(`{"path":"f.go","old_string":"keep\nold\nkeep2","new_string":"keep\nnew\nkeep2"}`)
	patch := editPatchForToolCall("edit", input)
	if patch == "" {
		t.Fatal("an edit call must yield a patch")
	}
	_, removed, added := byMarker(patch)
	if !contains(removed, "old") {
		t.Fatalf("edit patch must remove the old line, got removed=%v", removed)
	}
	if !contains(added, "new") {
		t.Fatalf("edit patch must add the new line, got added=%v", added)
	}
}

// TestEditPatchForToolCall_WriteIsAllGreen asserts a write to a new file yields a
// patch whose body is entirely additions — no removals — so the transcript shows
// an all-green diff for a created file.
func TestEditPatchForToolCall_WriteIsAllGreen(t *testing.T) {
	input := json.RawMessage(`{"path":"new.txt","content":"line one\nline two"}`)
	patch := editPatchForToolCall("write", input)
	if patch == "" {
		t.Fatal("a write call must yield a patch")
	}
	_, removed, added := byMarker(patch)
	if len(removed) != 0 {
		t.Fatalf("a new-file write must have no removed lines, got %v", removed)
	}
	if !contains(added, "line one") || !contains(added, "line two") {
		t.Fatalf("a write patch must add every content line, got added=%v", added)
	}
}

// TestEditPatchForToolCall_NonEditReturnsEmpty asserts a non-editing tool (or one
// whose arguments do not decode) yields no patch, so the caller renders the plain
// tool turn instead of an empty diff.
func TestEditPatchForToolCall_NonEditReturnsEmpty(t *testing.T) {
	if p := editPatchForToolCall("bash", json.RawMessage(`{"command":"ls"}`)); p != "" {
		t.Fatalf("a non-editing tool must yield no patch, got:\n%s", p)
	}
	if p := editPatchForToolCall("edit", json.RawMessage(`not json`)); p != "" {
		t.Fatalf("undecodable arguments must yield no patch, got:\n%s", p)
	}
}

// contains reports whether ss holds want.
func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

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

// A multiedit touches one file several times. The patch must carry a single
// file header so the diffstat counts one changed file, not one per edit, and
// emit one hunk per edit beneath it.
func TestUnifiedPatch_MultieditGroupsOneFileHeader(t *testing.T) {
	patch := unifiedPatch([]editDiff{
		{Tool: "multiedit", Path: "a.go", Before: "x", After: "y"},
		{Tool: "multiedit", Path: "a.go", Before: "p", After: "q"},
	})
	if n := strings.Count(patch, "+++ b/a.go"); n != 1 {
		t.Fatalf("expected a single file header for a multiedit, got %d:\n%s", n, patch)
	}
	if n := strings.Count(patch, "--- a/a.go"); n != 1 {
		t.Fatalf("expected a single old-path header for a multiedit, got %d:\n%s", n, patch)
	}
	if n := strings.Count(patch, "@@ "); n != 2 {
		t.Fatalf("expected one hunk per edit (2), got %d:\n%s", n, patch)
	}
}

// Each successive hunk in a grouped file continues the line numbering rather
// than resetting to line 1, so the numbered diff's gutter climbs through the
// file the way a real multi-hunk diff reads.
func TestUnifiedPatch_MultieditHunksAdvanceLineNumbers(t *testing.T) {
	patch := unifiedPatch([]editDiff{
		{Tool: "multiedit", Path: "a.go", Before: "a\nb", After: "A\nb"},
		{Tool: "multiedit", Path: "a.go", Before: "c", After: "C"},
	})
	if !strings.Contains(patch, "@@ -1,2 +1,2 @@") {
		t.Fatalf("expected first hunk to start at line 1, got:\n%s", patch)
	}
	// The first edit spans two lines on each side, so the second hunk starts at
	// line 3 on both sides.
	if !strings.Contains(patch, "@@ -3,1 +3,1 @@") {
		t.Fatalf("expected second hunk to continue at line 3, got:\n%s", patch)
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
