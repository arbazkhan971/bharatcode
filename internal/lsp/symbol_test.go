package lsp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHoverShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "markup_content",
			raw:  `{"contents":{"kind":"markdown","value":"hello"}}`,
			want: "hello",
		},
		{
			name: "marked_string_object",
			raw:  `{"contents":{"language":"go","value":"func F()"}}`,
			want: "func F()",
		},
		{
			name: "marked_string_bare",
			raw:  `{"contents":"plain text"}`,
			want: "plain text",
		},
		{
			name: "marked_string_array",
			raw:  `{"contents":["first",{"language":"go","value":"second"}]}`,
			want: "first\nsecond",
		},
		{
			name: "null_result",
			raw:  `null`,
			want: "",
		},
		{
			name: "empty_contents",
			raw:  `{"contents":""}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHover(json.RawMessage(tc.raw))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseDefinitionShapes(t *testing.T) {
	uri := pathToURI("/tmp/example/main.go")
	wantPath, err := uriToPath(uri)
	require.NoError(t, err)
	wantRange := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 3, Character: 4},
	}
	rangeJSON := `"range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`
	targetRangeJSON := `"targetRange":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`

	t.Run("single_location", func(t *testing.T) {
		raw := `{"uri":"` + uri + `",` + rangeJSON + `}`
		locations, err := parseDefinition(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Location{{Path: wantPath, Range: wantRange}}, locations)
	})

	t.Run("location_array", func(t *testing.T) {
		raw := `[{"uri":"` + uri + `",` + rangeJSON + `}]`
		locations, err := parseDefinition(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Location{{Path: wantPath, Range: wantRange}}, locations)
	})

	t.Run("location_link_array", func(t *testing.T) {
		raw := `[{"targetUri":"` + uri + `",` + targetRangeJSON + `}]`
		locations, err := parseDefinition(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Location{{Path: wantPath, Range: wantRange}}, locations)
	})

	t.Run("null_result", func(t *testing.T) {
		locations, err := parseDefinition(json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, locations)
	})

	t.Run("empty_array", func(t *testing.T) {
		locations, err := parseDefinition(json.RawMessage(`[]`))
		require.NoError(t, err)
		require.Empty(t, locations)
	})
}
