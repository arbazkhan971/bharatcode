package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// replacement is the UTF-8 encoding of the Unicode replacement character (U+FFFD)
// that the repair substitutes for an unpaired or malformed surrogate.
const replacement = "�"

// ctrlInput builds a tool-call body that embeds the raw NUL and SOH control
// bytes inside a JSON string value. Constructing it from a byte slice keeps any
// literal control characters out of the test source, where they would be both
// unreadable and rejected by the compiler.
func ctrlInput() string {
	nul := string([]byte{0x00})
	soh := string([]byte{0x01})
	return `{"text":"x` + nul + `y` + soh + `z"}`
}

// ctrlWant is the expected repair of ctrlInput: the two control bytes rewritten
// as their six-character escape forms so the string becomes valid JSON. The
// escapes are assembled from a bare backslash plus plain text so no
// backslash-u sequence ever appears literally in this source file.
func ctrlWant() string {
	bs := string([]byte{0x5c})
	return `{"text":"x` + bs + `u0000y` + bs + `u0001z"}`
}

// TestRepairToolCallJSON exercises the streaming tool-call repair across the
// failure modes models actually produce: raw control characters inside strings,
// invalid backslash escapes, unpaired UTF-16 surrogates, and truncated bodies,
// alongside the well-formed inputs that must survive untouched. Every repaired
// result is additionally re-parsed to prove the rewrite yields valid JSON.
func TestRepairToolCallJSON(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		want         string
		wantRepaired bool
		// wantValid asserts the repaired output parses as JSON. It is false only
		// for inputs whose structure is beyond a syntactic fix (here, the empty
		// string).
		wantValid bool
	}{
		{
			name:         "empty passthrough",
			in:           "",
			want:         "",
			wantRepaired: false,
			wantValid:    false,
		},
		{
			name:         "valid object passthrough",
			in:           `{"path":"main.go","line":42}`,
			want:         `{"path":"main.go","line":42}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid nested passthrough",
			in:           `{"a":[1,2,{"b":"c"}],"d":true,"e":null}`,
			want:         `{"a":[1,2,{"b":"c"}],"d":true,"e":null}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid string with proper escapes passthrough",
			in:           `{"q":"line1\nline2\t\"quoted\"\\done"}`,
			want:         `{"q":"line1\nline2\t\"quoted\"\\done"}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid multibyte utf8 passthrough",
			in:           `{"emoji":"Aé中"}`,
			want:         `{"emoji":"Aé中"}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid escaped unicode passthrough",
			in:           `{"s":"é"}`,
			want:         `{"s":"é"}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid surrogate pair passthrough",
			in:           `{"emoji":"😀"}`,
			want:         `{"emoji":"😀"}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "valid escaped surrogate pair passthrough",
			in:           `{"s":"😀"}`,
			want:         `{"s":"😀"}`,
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "raw newline in string",
			in:           "{\"text\":\"hello\nworld\"}",
			want:         `{"text":"hello\nworld"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "raw tab and carriage return in string",
			in:           "{\"text\":\"a\tb\rc\"}",
			want:         `{"text":"a\tb\rc"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "raw NUL and other control char in string",
			in:           ctrlInput(),
			want:         ctrlWant(),
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "raw backspace and formfeed in string",
			in:           "{\"text\":\"a\bb\fc\"}",
			want:         `{"text":"a\bb\fc"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "control char outside string is left intact",
			in:           "{\n\"k\":1}",
			want:         "{\n\"k\":1}",
			wantRepaired: false,
			wantValid:    true,
		},
		{
			name:         "invalid escape backslash-x",
			in:           `{"path":"C:\xfoo"}`,
			want:         `{"path":"C:\\xfoo"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "invalid escape windows path",
			in:           `{"path":"C:\Users\me"}`,
			want:         `{"path":"C:\\Users\\me"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "trailing backslash in string then truncated",
			in:           `{"path":"dir\`,
			want:         `{"path":"dir\\"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "lone high surrogate dropped",
			in:           `{"s":"\uD83D"}`,
			want:         `{"s":"` + replacement + `"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "lone low surrogate dropped",
			in:           `{"s":"\uDE00"}`,
			want:         `{"s":"` + replacement + `"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "high surrogate followed by non-surrogate text dropped",
			in:           `{"s":"\uD83Dabc"}`,
			want:         `{"s":"` + replacement + `abc"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "high surrogate followed by another high surrogate dropped",
			in:           `{"s":"\uD83D\uD83D"}`,
			want:         `{"s":"` + replacement + replacement + `"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "truncated unicode escape becomes replacement",
			in:           `{"s":"\u00"}`,
			want:         `{"s":"` + replacement + `"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "non-hex unicode escape becomes replacement",
			in:           `{"s":"\uZZZZ"}`,
			want:         `{"s":"` + replacement + `ZZZZ"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "truncated object closed",
			in:           `{"path":"main.go"`,
			want:         `{"path":"main.go"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "truncated nested structure closed",
			in:           `{"a":[1,2,{"b":"c"`,
			want:         `{"a":[1,2,{"b":"c"}]}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "truncated mid-string closed",
			in:           `{"path":"some/long/pa`,
			want:         `{"path":"some/long/pa"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "array truncated closed",
			in:           `["a","b"`,
			want:         `["a","b"]`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "combined control char and truncation",
			in:           "{\"text\":\"a\nb",
			want:         `{"text":"a\nb"}`,
			wantRepaired: true,
			wantValid:    true,
		},
		{
			name:         "structural chars inside string are not counted",
			in:           "{\"code\":\"if (x) {\ny\n}\"}",
			want:         `{"code":"if (x) {\ny\n}"}`,
			wantRepaired: true,
			wantValid:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, repaired := RepairToolCallJSON(tc.in)
			require.Equal(t, tc.want, got, "repaired output mismatch")
			require.Equal(t, tc.wantRepaired, repaired, "repaired flag mismatch")
			if tc.wantValid {
				require.True(t, json.Valid([]byte(got)),
					"expected repaired output to be valid JSON: %q", got)
			}
		})
	}
}

// TestRepairToolCallJSONUnmarshals confirms a repaired payload not only parses
// but decodes into the structure a tool handler would expect, which is the real
// reason the repair exists: to turn a recoverable stream into a usable argument
// object instead of a hard decode error.
func TestRepairToolCallJSONUnmarshals(t *testing.T) {
	// A raw newline in one value plus a Windows-style path whose separators form
	// invalid JSON escapes (\g, \p are not escape characters), and a truncated
	// tail — the way a length-capped stream tends to land. The Go source escapes
	// each backslash, so the runtime input string is C:\game\proj.txt.
	in := "{\"path\":\"C:\\game\\proj.txt\",\"note\":\"first\nsecond\""
	got, repaired := RepairToolCallJSON(in)
	require.True(t, repaired)
	require.True(t, json.Valid([]byte(got)))

	var args struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &args))
	require.Equal(t, `C:\game\proj.txt`, args.Path)
	require.Equal(t, "first\nsecond", args.Note)
}

// TestRepairToolCallJSONIdempotent checks that feeding an already-valid payload
// back through the repair is a no-op, so re-running the pass can never corrupt
// or progressively rewrite good output.
func TestRepairToolCallJSONIdempotent(t *testing.T) {
	inputs := []string{
		`{"path":"main.go"}`,
		`{"q":"a\nb\t\"c\"\\d"}`,
		`{"emoji":"😀"}`,
		`["x","y",{"z":1}]`,
		`{"s":"😀"}`,
	}
	for _, in := range inputs {
		first, repaired := RepairToolCallJSON(in)
		require.False(t, repaired, "valid input should not be repaired: %q", in)
		require.Equal(t, in, first)

		second, again := RepairToolCallJSON(first)
		require.False(t, again, "second pass should be a no-op: %q", first)
		require.Equal(t, first, second)
	}
}
