package chat

import (
	"regexp"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

// diffTurnBody builds the body the live edit/write path appends for a tool turn:
// the "tool: <name>" marker so the verb reads "Editing", then the diff sentinel
// and the unified patch.
func diffTurnBody(name, patch string) string {
	return "tool: " + name + "\n" + DiffMarker + "\n" + patch
}

// ansiEscapeRe strips SGR styling so a content assertion holds even when the
// viewer's word-diff emphasis splits a line into several styled spans.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiEscapeRe.ReplaceAllString(s, "") }

// TestDiffTurn_RendersThroughViewer asserts an edit tool turn whose body is
// tagged with DiffMarker renders as a numbered, tinted unified diff (the way the
// viewer draws it) rather than as the raw patch text under the plain styler. The
// removed and added lines carry ANSI styling and the leading "tool:"/marker lines
// never leak into the output.
func TestDiffTurn_RendersThroughViewer(t *testing.T) {
	patch := "--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-old line\n+new line"

	l := New()
	l.EnableDiff(styles.Default())
	l.Append(message.Message{
		ID:      "edit-1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: diffTurnBody("edit", patch)}},
	})
	out := l.Render(80)
	plain := stripANSI(out)

	require.Contains(t, plain, "Editing", "an edit turn must lead with the Editing verb")
	require.Contains(t, plain, "old line", "removed content must reach the transcript")
	require.Contains(t, plain, "new line", "added content must reach the transcript")
	require.Contains(t, out, "\x1b[", "the diff must be tinted, not plain text")
	// The marker and the "tool:" lead are renderer plumbing — they must not show.
	require.NotContains(t, plain, DiffMarker, "the diff sentinel must be stripped before drawing")
	require.NotContains(t, plain, "tool: edit", "the tool marker must not leak as a literal line")
}

// TestDiffTurn_NewFileWriteIsAllGreen asserts a write to a new file (empty
// before) renders every content line as an addition — there are no removed lines
// in the body the renderer draws.
func TestDiffTurn_NewFileWriteIsAllGreen(t *testing.T) {
	patch := "--- a/new.txt\n+++ b/new.txt\n@@ -1,0 +1,2 @@\n+first\n+second"

	l := New()
	l.EnableDiff(styles.Default())
	l.Append(message.Message{
		ID:      "write-1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: diffTurnBody("write", patch)}},
	})
	plain := stripANSI(l.Render(80))

	require.Contains(t, plain, "Editing", "a write turn reads as an Editing action")
	require.Contains(t, plain, "first")
	require.Contains(t, plain, "second")
	// No body line in this patch is a removal, so the rendered turn shows none.
	for _, line := range strings.Split(plain, "\n") {
		require.NotContains(t, line, "-first", "a new-file write has no removed content lines")
		require.NotContains(t, line, "-second", "a new-file write has no removed content lines")
	}
}

// TestDiffTurn_FallsBackWithoutViewer asserts that before a theme wires a viewer,
// a diff-tagged turn still renders the patch (via the plain sub-output styler)
// rather than dropping the change or printing the sentinel.
func TestDiffTurn_FallsBackWithoutViewer(t *testing.T) {
	patch := "--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-old line\n+new line"

	l := New() // no EnableDiff
	l.Append(message.Message{
		ID:      "edit-1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: diffTurnBody("edit", patch)}},
	})
	plain := stripANSI(l.Render(80))

	require.Contains(t, plain, "new line", "the patch must still render without a viewer")
	require.NotContains(t, plain, DiffMarker, "the sentinel must be stripped even on the fallback path")
}

// TestDiffTurn_LongDiffElides asserts a long diff is collapsed with the shared
// "… +N lines" hint so a sprawling rewrite does not bury the conversation.
func TestDiffTurn_LongDiffElides(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- a/big.txt\n+++ b/big.txt\n@@ -1,0 +1,40 @@\n")
	for i := 0; i < 40; i++ {
		b.WriteString("+line\n")
	}

	l := New()
	l.EnableDiff(styles.Default())
	l.Append(message.Message{
		ID:      "write-big",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: diffTurnBody("write", b.String())}},
	})
	plain := stripANSI(l.Render(80))

	require.Regexp(t, `… \+\d+ lines`, plain, "a long inline diff must elide with the shared hint")
}
