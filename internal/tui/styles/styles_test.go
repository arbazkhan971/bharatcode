package styles

import "testing"

// TestDefaultForBackground_MatchesDetectedBackground asserts the startup theme
// follows the detected terminal background: the dark Default on a dark terminal
// and the Light theme on a light one. This is the fix for light-grey assistant
// text washing out on a light terminal — the theme (and its paired markdown
// style) must track hasDarkBackground rather than always being dark.
func TestDefaultForBackground_MatchesDetectedBackground(t *testing.T) {
	got := DefaultForBackground()
	if hasDarkBackground {
		if got.Name != NameDark {
			t.Fatalf("dark background: got theme %q, want %q", got.Name, NameDark)
		}
		if got.Markdown != "dark" {
			t.Fatalf("dark background: got markdown style %q, want %q", got.Markdown, "dark")
		}
	} else {
		if got.Name != NameLight {
			t.Fatalf("light background: got theme %q, want %q", got.Name, NameLight)
		}
		if got.Markdown != "light" {
			t.Fatalf("light background: got markdown style %q, want %q", got.Markdown, "light")
		}
	}
}

// TestDefaultForBackground_IsAKnownTheme asserts the returned theme is one the
// rest of the TUI recognizes — i.e. it round-trips through ByName — so /theme and
// persistence stay consistent with whatever the startup default resolves to.
func TestDefaultForBackground_IsAKnownTheme(t *testing.T) {
	got := DefaultForBackground()
	if _, ok := ByName(got.Name); !ok {
		t.Fatalf("DefaultForBackground returned unknown theme %q", got.Name)
	}
}
