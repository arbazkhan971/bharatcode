package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// errNoClipboardTool reports that no supported system clipboard utility is
// available on this host. It is returned by the default copy function so the
// caller can degrade gracefully (surfacing a hint) instead of failing hard.
var errNoClipboardTool = errors.New("no clipboard utility found")

// copyFunc writes text to the system clipboard. It is a model seam so tests can
// inject a stub that records the copied text without touching a real clipboard.
type copyFunc func(text string) error

// systemClipboardCopy writes text to the system clipboard by shelling out to the
// first available platform utility: pbcopy (macOS), wl-copy (Wayland), or xclip
// / xsel (X11). It returns errNoClipboardTool when none is installed so the TUI
// can report a graceful message rather than crashing.
func systemClipboardCopy(text string) error {
	name, args, ok := clipboardCommand()
	if !ok {
		return errNoClipboardTool
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("writing to clipboard via %s: %w", name, err)
	}
	return nil
}

// clipboardCommand returns the clipboard write command for the current platform,
// choosing the first installed utility. The bool is false when none is found.
func clipboardCommand() (name string, args []string, ok bool) {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip"}}
	default:
		// Linux/BSD: prefer Wayland, then X11 utilities.
		candidates = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c[0], c[1:], true
		}
	}
	return "", nil, false
}
