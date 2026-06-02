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

func TestParseReferencesShapes(t *testing.T) {
	uri := pathToURI("/tmp/example/main.go")
	wantPath, err := uriToPath(uri)
	require.NoError(t, err)
	wantRange := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 3, Character: 4},
	}
	rangeJSON := `"range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`

	t.Run("location_array", func(t *testing.T) {
		raw := `[{"uri":"` + uri + `",` + rangeJSON + `}]`
		locations, err := parseReferences(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Location{{Path: wantPath, Range: wantRange}}, locations)
	})

	t.Run("null_result", func(t *testing.T) {
		locations, err := parseReferences(json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, locations)
	})

	t.Run("empty_array", func(t *testing.T) {
		locations, err := parseReferences(json.RawMessage(`[]`))
		require.NoError(t, err)
		require.Empty(t, locations)
	})
}

func TestParseDocumentSymbolsShapes(t *testing.T) {
	uri := pathToURI("/tmp/example/main.go")
	wantPath, err := uriToPath(uri)
	require.NoError(t, err)
	rng := `"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}}`
	wantRange := Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}}

	t.Run("hierarchical_document_symbols", func(t *testing.T) {
		raw := `[{"name":"Server","kind":23,` + rng + `,"children":[{"name":"Start","kind":6,` + rng + `}]}]`
		symbols, err := parseDocumentSymbols(wantPath, json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Symbol{
			{Name: "Server", Kind: Struct, Path: wantPath, Range: wantRange},
			{Name: "Start", Kind: Method, Path: wantPath, Range: wantRange, ContainerName: "Server"},
		}, symbols)
	})

	t.Run("flat_symbol_information", func(t *testing.T) {
		raw := `[{"name":"Helper","kind":12,"location":{"uri":"` + uri + `",` + rng + `}}]`
		symbols, err := parseDocumentSymbols("/ignored", json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Symbol{
			{Name: "Helper", Kind: Function, Path: wantPath, Range: wantRange},
		}, symbols)
	})

	t.Run("null_result", func(t *testing.T) {
		symbols, err := parseDocumentSymbols(wantPath, json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, symbols)
	})

	t.Run("empty_array", func(t *testing.T) {
		symbols, err := parseDocumentSymbols(wantPath, json.RawMessage(`[]`))
		require.NoError(t, err)
		require.Empty(t, symbols)
	})
}

func TestParseWorkspaceSymbolsShapes(t *testing.T) {
	uri := pathToURI("/tmp/example/main.go")
	wantPath, err := uriToPath(uri)
	require.NoError(t, err)
	rng := `"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}}`
	wantRange := Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}}

	t.Run("symbol_information_array", func(t *testing.T) {
		raw := `[{"name":"Server","kind":23,"containerName":"pkg","location":{"uri":"` + uri + `",` + rng + `}}]`
		symbols, err := parseWorkspaceSymbols(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Symbol{
			{Name: "Server", Kind: Struct, Path: wantPath, Range: wantRange, ContainerName: "pkg"},
		}, symbols)
	})

	t.Run("skips_entry_without_uri", func(t *testing.T) {
		raw := `[{"name":"Orphan","kind":12,"location":{"uri":"",` + rng + `}}]`
		symbols, err := parseWorkspaceSymbols(json.RawMessage(raw))
		require.NoError(t, err)
		require.Empty(t, symbols)
	})

	t.Run("null_result", func(t *testing.T) {
		symbols, err := parseWorkspaceSymbols(json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, symbols)
	})

	t.Run("empty_array", func(t *testing.T) {
		symbols, err := parseWorkspaceSymbols(json.RawMessage(`[]`))
		require.NoError(t, err)
		require.Empty(t, symbols)
	})
}

func TestParseRenameShapes(t *testing.T) {
	uri := pathToURI("/tmp/example/main.go")
	wantPath, err := uriToPath(uri)
	require.NoError(t, err)
	wantRange := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 3, Character: 4},
	}
	rangeJSON := `"range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`

	t.Run("changes_map", func(t *testing.T) {
		raw := `{"changes":{"` + uri + `":[{` + rangeJSON + `,"newText":"Renamed"}]}}`
		edit, err := parseRename(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, WorkspaceEdit{
			Changes: map[string][]TextEdit{
				wantPath: {{Range: wantRange, NewText: "Renamed"}},
			},
		}, edit)
	})

	t.Run("null_result", func(t *testing.T) {
		edit, err := parseRename(json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, edit.Changes)
	})

	t.Run("empty_changes", func(t *testing.T) {
		edit, err := parseRename(json.RawMessage(`{"changes":{}}`))
		require.NoError(t, err)
		require.Nil(t, edit.Changes)
	})
}
