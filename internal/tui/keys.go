package tui

import (
	"github.com/charmbracelet/bubbles/v2/help"
	"github.com/charmbracelet/bubbles/v2/key"
)

// keyMap is the set of global prompt bindings surfaced in the footer help bar
// and matched in handleKey via key.Matches. It is the bubbles/key counterpart
// to the keybindingGroups table that backs the fuller /keys overlay: the help
// strings here mirror those rows so the always-visible footer hint and the
// on-demand overlay describe the same keymap. Keeping the bindings in one
// struct lets the footer (help.Model) and the key dispatch read from a single
// source of truth rather than drifting between a switch and a help string.
type keyMap struct {
	// Submit sends the prompt to the agent.
	Submit key.Binding
	// Newline inserts a literal newline for multi-line prompts.
	Newline key.Binding
	// Palette opens the filterable command palette.
	Palette key.Binding
	// Model opens the model picker.
	Model key.Binding
	// Sessions opens the session picker.
	Sessions key.Binding
	// Files toggles the file-tree panel.
	Files key.Binding
	// Diff shows the latest edit diff.
	Diff key.Binding
	// NewTab opens a new session tab.
	NewTab key.Binding
	// Help toggles the expanded footer help.
	Help key.Binding
	// Quit interrupts a running turn, or quits on an empty idle prompt.
	Quit key.Binding
}

// defaultKeyMap returns the global bindings with their help text. The keys
// listed here must stay in step with the cases handled in handleKey; the help
// strings are deliberately terse so the footer's single line fits an 80-column
// terminal, while the longer phrasing lives in the /keys overlay.
func defaultKeyMap() keyMap {
	return keyMap{
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send"),
		),
		Newline: key.NewBinding(
			key.WithKeys("alt+enter"),
			key.WithHelp("alt+enter", "newline"),
		),
		Palette: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("ctrl+k", "commands"),
		),
		Model: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "model"),
		),
		Sessions: key.NewBinding(
			// The session picker has no dedicated key chord; it is reached via the
			// /sessions slash command, so the footer documents the command form.
			key.WithKeys("/sessions"),
			key.WithHelp("/sessions", "sessions"),
		),
		Files: key.NewBinding(
			key.WithKeys("ctrl+f"),
			key.WithHelp("ctrl+f", "files"),
		),
		Diff: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "diff"),
		),
		NewTab: key.NewBinding(
			key.WithKeys("ctrl+t"),
			key.WithHelp("ctrl+t", "new tab"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "more"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
	}
}

// ShortHelp returns the compact one-line footer bindings, ordered by how often
// a user reaches for them at the prompt. It satisfies help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Palette, k.Model, k.Files, k.Help, k.Quit}
}

// FullHelp returns the expanded, column-grouped bindings shown when the footer
// help is toggled open. It satisfies help.KeyMap.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Submit, k.Newline, k.NewTab},
		{k.Palette, k.Model, k.Sessions},
		{k.Files, k.Diff, k.Quit},
	}
}

// compile-time assertion that keyMap drives the help bubble.
var _ help.KeyMap = keyMap{}
