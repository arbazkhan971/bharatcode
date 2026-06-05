package lsp

import (
	"encoding/json"
	"testing"
)

func TestParsePullDiagnosticsCodeStringAndInteger(t *testing.T) {
	raw := json.RawMessage(`{
	  "items": [
	    {
	      "range": {"start": {"line": 2, "character": 8}, "end": {"line": 2, "character": 9}},
	      "severity": 1,
	      "message": "cannot find value x",
	      "source": "rustc",
	      "code": "E0425"
	    },
	    {
	      "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 3}},
	      "severity": 1,
	      "message": "Cannot find name 'foo'.",
	      "source": "ts",
	      "code": 2304
	    }
	  ]
	}`)

	diags, err := parsePullDiagnostics("main.rs", raw)
	if err != nil {
		t.Fatalf("parsePullDiagnostics: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2", len(diags))
	}
	if diags[0].Code != "E0425" {
		t.Errorf("string code = %q, want %q", diags[0].Code, "E0425")
	}
	if diags[1].Code != "2304" {
		t.Errorf("integer code = %q, want %q", diags[1].Code, "2304")
	}
}

func TestCodeFromWire(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"string", `"unused-import"`, "unused-import"},
		{"integer", `2304`, "2304"},
		{"null", `null`, ""},
		{"absent", ``, ""},
		{"object", `{"value":"x"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codeFromWire(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("codeFromWire(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
