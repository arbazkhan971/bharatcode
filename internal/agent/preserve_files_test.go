package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// toolUseMessage builds an assistant message carrying a single tool-use block,
// for the touched-files tests.
func toolUseMessage(name, path string) message.Message {
	return message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{message.ToolUseBlock{
			ID:    "tu-" + name,
			Name:  name,
			Input: []byte(`{"path":"` + path + `"}`),
		}},
	}
}

// TestTouchedFilesClassifiesReadAndEdit asserts view calls are recorded as
// reads, mutating tools as edits, and that a file both read and edited is
// reported only as edited (editing implies reading).
func TestTouchedFilesClassifiesReadAndEdit(t *testing.T) {
	history := []message.Message{
		userMessage("do the work"),
		toolUseMessage("view", "/repo/a.go"),
		toolUseMessage("view", "/repo/shared.go"),
		toolUseMessage("edit", "/repo/b.go"),
		toolUseMessage("write", "/repo/c.go"),
		toolUseMessage("rename", "/repo/d.go"),
		toolUseMessage("edit", "/repo/shared.go"), // both read and edited
	}

	read, edited := touchedFiles(history)

	require.Equal(t, []string{"/repo/b.go", "/repo/c.go", "/repo/d.go", "/repo/shared.go"}, edited)
	// shared.go is omitted from read because it was also edited.
	require.Equal(t, []string{"/repo/a.go"}, read)
}

// TestPreservedFrameRoundTrip asserts buildPreservedFrame and parsePreservedFrame
// are inverses, so a frame's census survives being parsed back on the next
// compaction.
func TestPreservedFrameRoundTrip(t *testing.T) {
	read := []string{"/repo/a.go", "/repo/b.go"}
	edited := []string{"/repo/x.go"}

	text := buildPreservedFrame(read, edited)
	require.Contains(t, text, preservedFilesMarker)

	gotRead, gotEdited := parsePreservedFrame(text)
	require.Equal(t, read, gotRead)
	require.Equal(t, edited, gotEdited)

	// Non-frame text parses to nothing.
	r, e := parsePreservedFrame("just some prose")
	require.Nil(t, r)
	require.Nil(t, e)

	// Empty census yields no frame.
	require.Equal(t, "", buildPreservedFrame(nil, nil))
}

// TestPreserveTouchedFilesPrependsAndDedupes asserts preserveTouchedFiles
// prepends a fresh frame and drops any stale frame already in the condensed
// history, leaving exactly one frame at the front.
func TestPreserveTouchedFilesPrependsAndDedupes(t *testing.T) {
	history := []message.Message{
		toolUseMessage("view", "/repo/a.go"),
		toolUseMessage("edit", "/repo/b.go"),
	}
	condensed := []message.Message{
		// A stale preserved frame that must be replaced, not stacked.
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: preservedFilesMarker + "\nstale"}}},
		userMessage("LATEST genuine prompt"),
	}

	out := preserveTouchedFiles(history, condensed)

	// Exactly one preserved frame, at the front.
	require.True(t, isPreservedFrame(out[0]), "preserved frame must be first")
	frames := 0
	for _, m := range out {
		if isPreservedFrame(m) {
			frames++
		}
	}
	require.Equal(t, 1, frames, "exactly one preserved frame")

	// The genuine latest user message is still last, so latest-user detection
	// keeps pointing at the real prompt.
	require.Equal(t, len(out)-1, latestUserIndex(out))

	front := textContent(out[0])
	require.Contains(t, front, "/repo/a.go")
	require.Contains(t, front, "/repo/b.go")
}

// TestPreserveTouchedFilesNoFilesUnchanged asserts a text-only conversation is
// returned unchanged, so compaction of a chat without file work adds no frame.
func TestPreserveTouchedFilesNoFilesUnchanged(t *testing.T) {
	history := []message.Message{userMessage("hello"), assistantMessage("hi")}
	condensed := []message.Message{userMessage("hello")}
	out := preserveTouchedFiles(history, condensed)
	require.Equal(t, condensed, out)
}

// TestCompactHistoryPreservesTouchedFiles drives compactHistory through a stub
// Compactor that drops everything to a bare summary, and asserts the touched
// files survive as a preserved frame. A second compaction over the result proves
// the census is durable: the files persist even after the original tool calls
// are gone.
func TestCompactHistoryPreservesTouchedFiles(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)

	// The stub drops everything to a single summary marker, simulating an
	// aggressive compaction that loses all tool-call detail.
	stub := &stubCompactor{condensed: []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: compactionSummaryMarker + " older turns"}}},
	}}

	loop := New(Config{
		Name:      "coder",
		Model:     "fake-model",
		Provider:  &scriptProvider{},
		Tools:     newFakeRegistry(),
		Sessions:  repo,
		Bus:       pubsub.NewTopic[Event]("agent-test", 16),
		Compactor: stub,
	})

	history := []message.Message{
		userMessage("refactor the budget code"),
		toolUseMessage("view", "/repo/budget.go"),
		toolUseMessage("edit", "/repo/budget.go"),
		userMessage("LATEST keep going"),
	}

	condensed, err := loop.compactHistory(ctx, history)
	require.NoError(t, err)

	require.True(t, isPreservedFrame(condensed[0]), "compacted history must lead with a preserved frame")
	// budget.go was edited (and read), so it is reported as edited.
	frame := textContent(condensed[0])
	require.Contains(t, frame, "/repo/budget.go")
	require.Contains(t, frame, preservedEditHeading)

	// Durability: compacting the already-compacted history again — by which point
	// the original tool calls are gone — still preserves the file census via the
	// prior frame.
	condensed2, err := loop.compactHistory(ctx, condensed)
	require.NoError(t, err)
	require.True(t, isPreservedFrame(condensed2[0]))
	require.Contains(t, textContent(condensed2[0]), "/repo/budget.go")
}

// TestPatchToolPathsExtractsPaths verifies that patchToolPaths correctly
// extracts the edited file paths from a unified-diff patch tool call, including
// the "b/" git-diff prefix stripping.
func TestPatchToolPathsExtractsPaths(t *testing.T) {
	diff := `--- a/internal/agent/budget.go
+++ b/internal/agent/budget.go
@@ -1,3 +1,4 @@
 package agent
+// changed
--- a/internal/session/tree.go
+++ b/internal/session/tree.go
@@ -10,2 +10,3 @@
 import "fmt"
+// also changed
`
	patchJSON, err := json.Marshal(map[string]string{"patch": diff})
	require.NoError(t, err)

	paths := patchToolPaths(patchJSON)
	require.Equal(t, []string{
		"internal/agent/budget.go",
		"internal/session/tree.go",
	}, paths)

	// A non-patch input (path-based tool) returns nil.
	require.Nil(t, patchToolPaths([]byte(`{"path":"/some/file.go"}`)))
	// An empty patch returns nil.
	require.Nil(t, patchToolPaths([]byte(`{"patch":""}`)))
	// /dev/null is filtered out (deletions only).
	devNull := `--- a/old.go
+++ /dev/null
@@ -1 +0,0 @@
-gone
`
	devNullJSON, err := json.Marshal(map[string]string{"patch": devNull})
	require.NoError(t, err)
	require.Nil(t, patchToolPaths(devNullJSON))
}

// TestTouchedFiles_PatchToolCollectsEdits verifies that a patch tool call's
// affected file paths are collected in the edited set so they survive
// compaction. This is the end-to-end path through touchedFiles → classify →
// patchToolPaths.
func TestTouchedFiles_PatchToolCollectsEdits(t *testing.T) {
	diff := `--- a/pkg/foo.go
+++ b/pkg/foo.go
@@ -5,3 +5,4 @@
 func Foo() {}
+func Bar() {}
--- a/pkg/baz.go
+++ b/pkg/baz.go
@@ -1,2 +1,3 @@
 package pkg
+// hello
`
	patchJSON, err := json.Marshal(map[string]string{"patch": diff})
	require.NoError(t, err)

	history := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.ContentBlock{message.ToolUseBlock{
				ID:    "patch-1",
				Name:  "patch",
				Input: patchJSON,
			}},
		},
	}

	_, edited := touchedFiles(history)
	require.Contains(t, edited, "pkg/foo.go", "patch tool should record foo.go as edited")
	require.Contains(t, edited, "pkg/baz.go", "patch tool should record baz.go as edited")
}
