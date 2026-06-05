package lsp

import (
	"context"
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
			name: "markup_content_plaintext",
			raw:  `{"contents":{"kind":"plaintext","value":"hello"}}`,
			want: "hello",
		},
		{
			// A MarkedString with a language is, per the LSP spec, equivalent to a
			// fenced code block in that language, so the value is wrapped in a fence.
			name: "marked_string_object",
			raw:  `{"contents":{"language":"go","value":"func F()"}}`,
			want: "```go\nfunc F()\n```",
		},
		{
			name: "marked_string_bare",
			raw:  `{"contents":"plain text"}`,
			want: "plain text",
		},
		{
			// Distinct sections are separated by a blank line, and the
			// language-tagged section is fenced.
			name: "marked_string_array",
			raw:  `{"contents":["first",{"language":"go","value":"second"}]}`,
			want: "first\n\n```go\nsecond\n```",
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

func TestParseFormattingShapes(t *testing.T) {
	wantRange := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 3, Character: 4},
	}
	rangeJSON := `"range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`

	t.Run("edit_array", func(t *testing.T) {
		raw := `[{` + rangeJSON + `,"newText":"formatted\n"}]`
		edits, err := parseFormatting(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []TextEdit{{Range: wantRange, NewText: "formatted\n"}}, edits)
	})

	t.Run("null_result", func(t *testing.T) {
		edits, err := parseFormatting(json.RawMessage(`null`))
		require.NoError(t, err)
		require.Nil(t, edits)
	})

	t.Run("empty_array", func(t *testing.T) {
		edits, err := parseFormatting(json.RawMessage(`[]`))
		require.NoError(t, err)
		require.Empty(t, edits)
	})

	t.Run("unexpected_value", func(t *testing.T) {
		_, err := parseFormatting(json.RawMessage(`{"changes":{}}`))
		require.Error(t, err)
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

	t.Run("detail_carried_through", func(t *testing.T) {
		// The server-supplied "detail" (signature/type) is parsed onto each
		// document symbol, including nested children.
		raw := `[{"name":"Add","kind":12,"detail":"func(a int, b int) int",` + rng + `,` +
			`"children":[{"name":"sum","kind":13,"detail":"int",` + rng + `}]}]`
		symbols, err := parseDocumentSymbols(wantPath, json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, []Symbol{
			{Name: "Add", Kind: Function, Path: wantPath, Range: wantRange, Detail: "func(a int, b int) int"},
			{Name: "sum", Kind: Variable, Path: wantPath, Range: wantRange, ContainerName: "Add", Detail: "int"},
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

	t.Run("document_changes", func(t *testing.T) {
		// Servers advertising documentChanges support (gopls, rust-analyzer, ...)
		// return TextDocumentEdits instead of a changes map.
		raw := `{"documentChanges":[{"textDocument":{"uri":"` + uri + `","version":7},"edits":[{` + rangeJSON + `,"newText":"Renamed"}]}]}`
		edit, err := parseRename(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, WorkspaceEdit{
			Changes: map[string][]TextEdit{
				wantPath: {{Range: wantRange, NewText: "Renamed"}},
			},
		}, edit)
	})

	t.Run("document_changes_skips_resource_ops", func(t *testing.T) {
		// A create-file resource operation is interleaved with a text edit; only
		// the text edit is representable, so the op is skipped rather than failing.
		other := pathToURI("/tmp/example/new.go")
		raw := `{"documentChanges":[` +
			`{"kind":"create","uri":"` + other + `"},` +
			`{"textDocument":{"uri":"` + uri + `","version":7},"edits":[{` + rangeJSON + `,"newText":"Renamed"}]}` +
			`]}`
		edit, err := parseRename(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, WorkspaceEdit{
			Changes: map[string][]TextEdit{
				wantPath: {{Range: wantRange, NewText: "Renamed"}},
			},
		}, edit)
	})

	t.Run("document_changes_empty", func(t *testing.T) {
		// A documentChanges array carrying only resource operations leaves no text
		// edits, yielding a zero WorkspaceEdit.
		raw := `{"documentChanges":[{"kind":"delete","uri":"` + uri + `"}]}`
		edit, err := parseRename(json.RawMessage(raw))
		require.NoError(t, err)
		require.Nil(t, edit.Changes)
	})
}

func TestParseCodeActionPreservesResolveData(t *testing.T) {
	t.Run("editless_action_keeps_data", func(t *testing.T) {
		// An action without an edit retains its raw object so a follow-up
		// codeAction/resolve can echo it back to the server.
		raw := `{"title":"Extract function","kind":"refactor.extract","data":{"fn":"x"}}`
		action, err := parseCodeAction(json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, "Extract function", action.Title)
		require.Empty(t, action.Edit.Changes)
		require.JSONEq(t, raw, string(action.Data))
	})

	t.Run("bare_command_has_no_data", func(t *testing.T) {
		// A bare Command entry cannot be resolved, so it carries no resolve data.
		raw := `{"title":"Generate","command":"gopls.generate","arguments":[]}`
		action, err := parseCodeAction(json.RawMessage(raw))
		require.NoError(t, err)
		require.Nil(t, action.Data)
	})
}

func TestResolveCodeActionRequiresData(t *testing.T) {
	// Resolving an action with no round-trip data is rejected before any request
	// is issued, so a nil client never gets dereferenced.
	c := &client{}
	_, err := c.resolveCodeAction(context.Background(), CodeAction{Title: "Extract function"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not resolvable")
}
