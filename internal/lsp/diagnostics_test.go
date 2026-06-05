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

func TestParsePullDiagnosticsRelatedInformation(t *testing.T) {
	raw := json.RawMessage(`{
	  "items": [
	    {
	      "range": {"start": {"line": 4, "character": 5}, "end": {"line": 4, "character": 8}},
	      "severity": 1,
	      "message": "x redeclared in this block",
	      "source": "gopls",
	      "relatedInformation": [
	        {
	          "location": {
	            "uri": "file:///work/main.go",
	            "range": {"start": {"line": 2, "character": 5}, "end": {"line": 2, "character": 6}}
	          },
	          "message": "other declaration of x"
	        },
	        {
	          "location": {"uri": "not a uri", "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 0}}},
	          "message": "dropped: unparseable uri"
	        }
	      ]
	    }
	  ]
	}`)

	diags, err := parsePullDiagnostics("main.go", raw)
	if err != nil {
		t.Fatalf("parsePullDiagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	// The non-file URI entry is dropped; only the resolvable one survives.
	if len(diags[0].Related) != 1 {
		t.Fatalf("got %d related entries, want 1", len(diags[0].Related))
	}
	rel := diags[0].Related[0]
	if rel.Message != "other declaration of x" {
		t.Errorf("related message = %q, want %q", rel.Message, "other declaration of x")
	}
	if rel.Location.Path != "/work/main.go" {
		t.Errorf("related path = %q, want %q", rel.Location.Path, "/work/main.go")
	}
	if rel.Location.Range.Start.Line != 2 || rel.Location.Range.Start.Character != 5 {
		t.Errorf("related start = %d:%d, want 2:5", rel.Location.Range.Start.Line, rel.Location.Range.Start.Character)
	}
}

func TestRelatedFromWireEmpty(t *testing.T) {
	if got := relatedFromWire(nil); got != nil {
		t.Errorf("relatedFromWire(nil) = %v, want nil", got)
	}
	// An entry with an undecodable URI is the only one, so nothing survives.
	items := []wireRelatedInformation{{Location: wireLocation{URI: "http://example.com"}, Message: "x"}}
	if got := relatedFromWire(items); got != nil {
		t.Errorf("relatedFromWire(non-file) = %v, want nil", got)
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
