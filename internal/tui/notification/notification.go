// Package notification abstracts terminal-focus-aware notifications.
package notification

import "sync"

// Notifier sends an out-of-terminal notification.
type Notifier interface {
	Notify(title string, body string) error
}

// Noop discards notifications.
type Noop struct{}

// Notify implements Notifier.
func (Noop) Notify(string, string) error {
	return nil
}

// FocusAware suppresses notifications while the terminal has focus.
type FocusAware struct {
	mu       sync.Mutex
	focused  bool
	notifier Notifier
	sent     int
}

// NewFocusAware constructs a focus-aware notifier.
func NewFocusAware(notifier Notifier) *FocusAware {
	if notifier == nil {
		notifier = Noop{}
	}
	return &FocusAware{focused: true, notifier: notifier}
}

// SetFocused records whether the terminal currently has focus.
func (f *FocusAware) SetFocused(focused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused = focused
}

// Notify sends only when focus is lost.
func (f *FocusAware) Notify(title string, body string) error {
	f.mu.Lock()
	focused := f.focused
	if !focused {
		f.sent++
	}
	f.mu.Unlock()
	if focused {
		return nil
	}
	return f.notifier.Notify(title, body)
}

// Sent returns the number of notifications passed through.
func (f *FocusAware) Sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent
}
