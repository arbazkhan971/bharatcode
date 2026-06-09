package tui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// TestPTYTUI drives the COMPILED binary inside a real pseudo-terminal, the only
// way to exercise the alt-screen renderer the way an interactive user does. The
// unit-level model tests above feed Update directly and never touch a terminal,
// so they cannot catch a regression in terminal setup, capability negotiation,
// or the enter/leave alt-screen handshake — the rough edges that only surface
// under a PTY. This test fills that gap.
//
// It is opt-in behind BHARATCODE_TUI_PTY_SMOKE=1 and skipped by default so the
// standard `go test ./...` stays fast, offline, and free of a build step. The
// run needs no provider: it only submits prompts and quits, asserting on what
// the renderer paints, not on any model reply.
//
//	BHARATCODE_TUI_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTUI
func TestPTYTUI(t *testing.T) {
	if os.Getenv("BHARATCODE_TUI_PTY_SMOKE") != "1" {
		t.Skip("set BHARATCODE_TUI_PTY_SMOKE=1 to run the PTY/TUI smoke test")
	}

	bin := buildTestBinary(t)

	// Two prompts, one submitted with LF and one with CR, so the harness proves
	// both line-ending submissions reach the input and are drawn. A trailing
	// Ctrl-C (sent by runPTY after the inputs) quits the idle TUI.
	const lfPrompt = "pty smoke linefeed prompt"
	const crPrompt = "pty smoke carriage prompt"
	transcript := runPTY(t, bin, []ptyInput{
		{text: lfPrompt + "\n"},
		{text: crPrompt + "\r"},
	})

	require.NotEmpty(t, transcript, "PTY transcript was empty — the TUI never rendered a frame")

	// The renderer paints typed input into the prompt area, so both submitted
	// prompts must appear in the captured transcript. A blank capture or a
	// renderer that swallowed input would fail here instead of in front of a user.
	require.Contains(t, transcript, lfPrompt, "transcript is missing the LF-submitted prompt text")
	require.Contains(t, transcript, crPrompt, "transcript is missing the CR-submitted prompt text")

	// A correct alt-screen lifecycle enters the alternate buffer at most once.
	// A flap — repeatedly entering and leaving the alt-screen — is the classic
	// PTY-only regression (a redraw loop toggling smcup/rmcup) that floods the
	// terminal and blanks the screen between frames. Catch it by counting the
	// enter sequence; a healthy session shows exactly one enter.
	enters := strings.Count(transcript, altScreenEnter)
	require.LessOrEqual(t, enters, 1,
		"alt-screen entered %d times — a flap (repeated smcup/rmcup) is a redraw regression", enters)
	// The matching leave count must also stay bounded: a session that toggles the
	// alt-screen off and back on repaints from a blank screen each cycle. A clean
	// run leaves at most once (on quit).
	leaves := strings.Count(transcript, altScreenLeave)
	require.LessOrEqual(t, leaves, 1,
		"alt-screen left %d times — a flap (repeated smcup/rmcup) is a redraw regression", leaves)
}

// altScreenEnter and altScreenLeave are the DEC private-mode sequences that
// switch the terminal into and out of the alternate screen buffer. The TUI
// enters the alt-screen once at startup (View sets AltScreen) and leaves it once
// on quit; counting them detects an alt-screen flap.
const (
	altScreenEnter = "\x1b[?1049h"
	altScreenLeave = "\x1b[?1049l"
)

// ptyInput is one scripted write to the pseudo-terminal. text is sent verbatim
// (include the trailing "\n" or "\r" to submit a prompt); pause overrides the
// default settle delay applied after the write so the renderer can repaint
// before the next input.
type ptyInput struct {
	text  string
	pause time.Duration
}

// buildTestBinary compiles the bharatcode binary to a temp path and returns it.
// The PTY smoke test must drive the real binary (not the in-process model), so a
// build is unavoidable; t.TempDir cleans it up. The build is plain `go build .`
// from the module root so it picks up the same toolchain and flags the suite runs
// under. A build failure fails the test loudly rather than skipping.
func buildTestBinary(t *testing.T) string {
	t.Helper()

	root := moduleRoot(t)
	out := filepath.Join(t.TempDir(), "bharatcode-ptytest")

	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test binary: %v\n%s", err, combined)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("built binary missing at %s: %v", out, err)
	}
	return out
}

// moduleRoot walks up from the test's working directory to the directory holding
// go.mod, so buildTestBinary can `go build .` the main package regardless of the
// per-package cwd `go test` uses.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err, "resolving working directory")
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

// runPTY launches bin in a pseudo-terminal sized like a real session, plays the
// scripted inputs, lets output settle, then quits with Ctrl-C and returns the
// full raw transcript. It never hangs: a wall-clock deadline force-kills the
// child and the reader drains until EOF, so a wedged TUI fails the test by
// assertion (empty/short transcript) rather than by timing out the suite.
//
// The harness answers the terminal-capability queries the renderer emits at
// startup (background-color via OSC 11 and Primary Device Attributes via
// CSI c). A real terminal replies to these; creack/pty does not, and the
// bubbletea renderer blocks on the reply before painting its first frame. Without
// the responder the capture is just the unanswered queries and no UI — so the
// responder is what makes the transcript meaningful.
func runPTY(t *testing.T, bin string, inputs []ptyInput) string {
	t.Helper()

	cmd := exec.Command(bin)
	// QUIET_REDRAW keeps the live renderer (TERM is set) but slows its timers and
	// dedupes the status bar, matching how a recorder/capture session drives the
	// TUI — a stable, low-noise transcript. TERM must name a capable terminal so
	// the renderer does not fall back to its non-rendering headless path.
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"BHARATCODE_QUIET_REDRAW=1",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	require.NoError(t, err, "starting binary under PTY")
	defer func() { _ = ptmx.Close() }()

	var (
		mu      sync.Mutex
		buf     bytes.Buffer
		readEnd = make(chan struct{})
	)
	go func() {
		defer close(readEnd)
		chunk := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf.Write(chunk[:n])
				mu.Unlock()
				answerTerminalQueries(ptmx, chunk[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Let the renderer negotiate capabilities and paint its first frame before
	// typing, so the prompts land in a drawn input area rather than racing setup.
	time.Sleep(1500 * time.Millisecond)

	for _, in := range inputs {
		if _, werr := ptmx.Write([]byte(in.text)); werr != nil {
			break
		}
		settle := in.pause
		if settle <= 0 {
			settle = 800 * time.Millisecond
		}
		time.Sleep(settle)
	}

	// Quit the idle TUI. Ctrl-C on an empty prompt requests quit; a second one
	// covers the case where the first interrupted in-flight work instead.
	_, _ = ptmx.Write([]byte{0x03})
	time.Sleep(300 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03})

	// Wait for a clean exit, but never block the suite: force-kill past a deadline.
	waited := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
	}

	// Closing the PTY unblocks the reader's final Read with EOF.
	_ = ptmx.Close()
	<-readEnd

	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

// answerTerminalQueries replies to the capability probes the bubbletea renderer
// emits during startup so it stops waiting and renders. It mimics a minimal,
// well-behaved terminal: an OSC 11 background-color report and a Primary Device
// Attributes (CSI c) response. Replies are best-effort — a closed PTY simply
// drops the write.
func answerTerminalQueries(w *os.File, chunk []byte) {
	s := string(chunk)
	if strings.Contains(s, "\x1b]11;?") {
		// Report an opaque black background.
		_, _ = w.WriteString("\x1b]11;rgb:0000/0000/0000\x07")
	}
	if strings.Contains(s, "\x1b[c") || strings.Contains(s, "\x1b[0c") {
		// Identify as a VT100 with Advanced Video Option.
		_, _ = w.WriteString("\x1b[?1;2c")
	}
}
