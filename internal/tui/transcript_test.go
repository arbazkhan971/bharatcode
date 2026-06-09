package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// updateTranscriptGolden regenerates the normalized transcript snapshot from the
// raw fixture: `go test ./internal/tui -run TestTranscriptVisibilityFixture
// -update`. It is off by default so a normal run only compares.
var updateTranscriptGolden = flag.Bool("update", false, "update transcript golden files")

// Transcript normalization makes a captured TUI session comparable across runs.
// A raw capture is full of per-run noise — ANSI styling and cursor moves, the
// animated spinner glyph, freshly minted session UUIDs, wall-clock timestamps,
// elapsed-time counters, and absolute temp-dir paths — none of which describe
// what the user actually saw happen. normalizeTranscript strips that noise down
// to the stable, human-meaningful content so a transcript can be snapshot-tested
// and diffed.
//
// The patterns below are ordered the way they must run: escape sequences first
// (so later text patterns see clean glyphs), then the volatile tokens, then a
// final whitespace collapse.
var (
	// oscSequence matches an Operating System Command (e.g. the title/background
	// queries the renderer emits), terminated by BEL or ST (ESC \).
	oscSequence = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	// csiSequence matches any CSI control sequence: colors, cursor moves, mode
	// toggles, erases — every "ESC [ … final-byte" form.
	csiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	// escSequence matches the remaining two-byte ESC forms (e.g. ESC \ as a bare
	// string terminator, ESC = / ESC >) left after CSI/OSC removal.
	escSequence = regexp.MustCompile(`\x1b[@-Z\\-_=>]`)

	// spinnerFrame matches the MiniDot braille spinner glyphs (the bubbles
	// spinner.MiniDot frames). The exact frame depends on when the capture was
	// taken, so every frame collapses to one token.
	spinnerFrame = regexp.MustCompile("[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]")

	// uuidPattern matches a v4-style UUID such as a session id.
	uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	// isoTimestamp matches an RFC3339-ish timestamp (date, time, optional zone).
	isoTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	// durationToken matches the elapsed counters the status bar shows while a turn
	// runs ("working 3s", "1m2s", "250ms"), which change every frame.
	durationToken = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:h\d+m\d+s|m\d+s|h|m|s|ms|µs|us|ns)\b`)
	// absPath matches an absolute filesystem path so per-run temp dirs do not leak
	// into the snapshot. The leading boundary ((^|[\s"'`(])) keeps it from biting
	// into mid-token slashes like "build/test" or the relative "./..." in a tool
	// label — only a slash that starts a fresh token begins a path. The boundary is
	// captured and restored so the surrounding character is preserved.
	absPath = regexp.MustCompile("(^|[\\s\"'`(])(/[^\\s\"'`()]+)+")

	// trailingSpace and blankRun collapse the whitespace the renderer pads frames
	// with, so a snapshot compares content rather than terminal geometry.
	trailingSpace = regexp.MustCompile(`[ \t]+\n`)
	blankRun      = regexp.MustCompile(`\n{3,}`)
)

// normalizeTranscript reduces a raw PTY/TUI capture to stable, comparable text.
// It removes terminal control sequences, replaces volatile tokens (spinner,
// UUID, timestamp, duration, absolute path) with fixed placeholders, and
// collapses redraw whitespace. The result is deterministic: the same logical
// session normalizes to the same string regardless of timing, run directory, or
// session id.
func normalizeTranscript(raw string) string {
	s := raw
	s = oscSequence.ReplaceAllString(s, "")
	s = csiSequence.ReplaceAllString(s, "")
	s = escSequence.ReplaceAllString(s, "")

	// Drop the remaining lone control bytes (BEL, etc.) but keep tab and newline
	// so line structure survives for the whitespace collapse below.
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)

	s = spinnerFrame.ReplaceAllString(s, "<SPINNER>")
	s = uuidPattern.ReplaceAllString(s, "<UUID>")
	s = isoTimestamp.ReplaceAllString(s, "<TIMESTAMP>")
	s = durationToken.ReplaceAllString(s, "<DUR>")
	// Restore the captured leading boundary (group 1) before the placeholder so a
	// preceding space/quote is not eaten along with the path.
	s = absPath.ReplaceAllString(s, "${1}<PATH>")

	// Normalize CR-only and CRLF line endings to LF, then collapse padding.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = trailingSpace.ReplaceAllString(s, "\n")
	s = blankRun.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s) + "\n"
}

// TestNormalizeTranscriptDeterministic proves normalizeTranscript erases every
// class of per-run noise and is idempotent: two captures of the same logical
// session — differing only in spinner frame, session UUID, timestamp, elapsed
// duration, and run directory — normalize to the identical string, and that
// string is fixed (normalizing it again is a no-op).
func TestNormalizeTranscriptDeterministic(t *testing.T) {
	runA := "\x1b[2J\x1b[H\x1b[38;2;1;2;3m⠋ working 3s\x1b[m\n" +
		"session 1b4e28ba-2fa1-11d2-883f-0016d3cca427\n" +
		"started 2026-06-09T10:11:12Z\n" +
		"reading /tmp/run-aaa/notes.txt\n"
	runB := "\x1b[2J\x1b[H\x1b[38;2;9;9;9m⠹ working 27s\x1b[m\n" +
		"session 7c9e6679-7425-40de-944b-e07fc1f90ae7\n" +
		"started 2026-06-09T22:33:44Z\n" +
		"reading /var/folders/zz/run-bbb/notes.txt\n"

	gotA := normalizeTranscript(runA)
	gotB := normalizeTranscript(runB)
	require.Equal(t, gotA, gotB,
		"two captures of the same session must normalize identically once noise is stripped")

	// No raw escape, spinner glyph, UUID, ISO timestamp, or run-specific path may
	// survive normalization.
	require.NotContains(t, gotA, "\x1b", "ANSI escapes must be stripped")
	require.NotContains(t, gotA, "⠋", "spinner glyphs must be replaced")
	require.NotContains(t, gotA, "1b4e28ba", "UUIDs must be replaced")
	require.NotContains(t, gotA, "2026-06-09", "timestamps must be replaced")
	require.NotContains(t, gotA, "3s", "elapsed durations must be replaced")
	require.NotContains(t, gotA, "run-aaa", "absolute paths must be replaced")
	require.Contains(t, gotA, "<SPINNER>")
	require.Contains(t, gotA, "<UUID>")
	require.Contains(t, gotA, "<TIMESTAMP>")
	require.Contains(t, gotA, "<DUR>")
	require.Contains(t, gotA, "<PATH>")

	// Idempotent: normalizing an already-normalized transcript changes nothing.
	require.Equal(t, gotA, normalizeTranscript(gotA), "normalization must be idempotent")
}

// TestTranscriptVisibilityFixture asserts that one realistic, normalized session
// transcript surfaces all five things a `bharatcode` user must be able to read
// without scrolling a log: their own input, the tool activity the agent ran, the
// assistant's final answer, the changed-files summary, and the verification
// status. The fixture lives under testdata so a regression that hides any of
// these (a renderer change, a summary that stops printing) fails here.
//
// The expected snapshot is regenerated by running this test with -update; the
// raw fixture is the input and the .golden file is the normalized output, so the
// normalizer itself is exercised end to end on representative content.
func TestTranscriptVisibilityFixture(t *testing.T) {
	dir := filepath.Join("testdata", "transcripts")
	raw, err := os.ReadFile(filepath.Join(dir, "session.raw"))
	require.NoError(t, err, "reading raw transcript fixture")

	got := normalizeTranscript(string(raw))

	goldenPath := filepath.Join(dir, "session.golden")
	if *updateTranscriptGolden {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644), "updating golden")
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "reading golden transcript (run with -update to create it)")
	require.Equal(t, string(want), got, "normalized transcript drifted from golden; rerun with -update if intended")

	// The five visibility elements, checked against the normalized golden so the
	// assertions describe stable content, not styling.
	norm := string(want)
	require.Contains(t, norm, "build me a todo app",
		"element 1/5: the user's own prompt must be visible")
	require.Contains(t, norm, "Bash(go test ./...)",
		"element 2/5: tool activity the agent ran must be visible")
	require.Contains(t, norm, "Done — the todo app is built and its tests pass.",
		"element 3/5: the assistant's final answer must be visible")
	require.Contains(t, norm, "Changed files:",
		"element 4/5: the changed-files summary must be visible")
	require.Contains(t, norm, "Verification: build/test passed.",
		"element 5/5: the verification status must be visible")
}
