package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// descStub is a minimal Tool whose Description is fully controlled by the test,
// so ShortDescription can be exercised against single- and multi-line manuals.
type descStub struct{ desc string }

func (d descStub) Name() string                                         { return "stub" }
func (d descStub) Description() string                                  { return d.desc }
func (d descStub) Schema() json.RawMessage                              { return json.RawMessage(`{}`) }
func (d descStub) Run(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }

func TestShortDescription(t *testing.T) {
	cases := []struct {
		name string
		desc string
		want string
	}{
		{"single line", "Run a shell command.", "Run a shell command."},
		{"multi line keeps first", "Run a shell command.\nFull manual line two.\nLine three.", "Run a shell command."},
		{"trims surrounding space", "  Search files.  \n  more docs  ", "Search files."},
		{"leading blank line", "\nReal summary.\nrest", "Real summary."},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShortDescription(descStub{desc: tc.desc}); got != tc.want {
				t.Fatalf("ShortDescription(%q) = %q, want %q", tc.desc, got, tc.want)
			}
		})
	}
}
