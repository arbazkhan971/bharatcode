package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// TestPTYGoldenAbout drives the COMPILED binary inside a real pseudo-terminal,
// running a deterministic subcommand, and compares the normalized transcript to
// a checked-in golden file. Running under a PTY (rather than a plain pipe) proves
// the binary behaves when stdout is a terminal — the path an interactive user
// hits — while a fixed-output subcommand keeps the transcript stable enough to
// pin in a golden.
//
// It is opt-in behind BHARATCODE_PTY_GOLDEN=1 and skipped by default so the
// standard `go test ./...` stays fast, offline, and build-free. Regenerate the
// golden after an intentional change with:
//
//	BHARATCODE_PTY_GOLDEN=1 BHARATCODE_PTY_GOLDEN_UPDATE=1 \
//	  go test ./internal/cmd -run TestPTYGoldenAbout
func TestPTYGoldenAbout(t *testing.T) {
	if os.Getenv("BHARATCODE_PTY_GOLDEN") != "1" {
		t.Skip("set BHARATCODE_PTY_GOLDEN=1 to run the PTY golden transcript test")
	}

	bin := buildGoldenBinary(t)
	transcript := capturePTY(t, bin, "about")
	got := normalizeTranscript(transcript)
	require.NotEmpty(t, got, "PTY transcript was empty — the binary produced no output")

	goldenPath := filepath.Join("testdata", "golden", "about.txt")
	if os.Getenv("BHARATCODE_PTY_GOLDEN_UPDATE") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(got+"\n"), 0o644))
		t.Logf("updated golden %s", goldenPath)
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "missing golden %s — regenerate with BHARATCODE_PTY_GOLDEN_UPDATE=1", goldenPath)
	want := strings.TrimRight(string(wantBytes), "\n")
	require.Equal(t, want, got, "PTY transcript does not match golden %s", goldenPath)
}

// ansiPattern matches the CSI and OSC escape sequences a terminal program emits
// (colors, cursor moves, capability queries). normalizeTranscript strips them so
// the golden captures the textual content, not terminal control bytes that vary
// by terminfo.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\a]*(?:\a|\x1b\\)|\x1b[=>]`)

// normalizeTranscript reduces a raw PTY transcript to stable, comparable text:
// it strips ANSI/OSC escapes and carriage returns, trims trailing whitespace on
// each line, and drops leading and trailing blank lines.
func normalizeTranscript(raw string) string {
	s := ansiPattern.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	// Trim leading/trailing blank lines.
	start, end := 0, len(lines)
	for start < end && lines[start] == "" {
		start++
	}
	for end > start && lines[end-1] == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// buildGoldenBinary compiles the bharatcode binary to a temp path. The golden
// test must drive the real binary under a PTY, so a build is unavoidable; the
// temp dir cleans it up. A build failure fails the test loudly.
func buildGoldenBinary(t *testing.T) string {
	t.Helper()
	root := goldenModuleRoot(t)
	out := filepath.Join(t.TempDir(), "bharatcode-pty-golden")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test binary: %v\n%s", err, combined)
	}
	return out
}

// goldenModuleRoot walks up from the test's working directory to the directory
// holding go.mod so the build targets the main package regardless of the
// per-package cwd `go test` uses.
func goldenModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

// capturePTY runs bin with args inside a pseudo-terminal and returns the full raw
// transcript. It never hangs: a wall-clock deadline force-kills the child and the
// reader drains to EOF, so a wedged process fails by assertion (empty transcript)
// rather than by timing out the suite.
func capturePTY(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "NO_COLOR=1")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	require.NoError(t, err, "starting binary under PTY")
	defer func() { _ = ptmx.Close() }()

	var buf bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		chunk := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	waited := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
	}

	_ = ptmx.Close()
	<-readDone
	return buf.String()
}
