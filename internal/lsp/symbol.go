package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// hover issues a textDocument/hover request for the position and returns the
// server's hover text. An empty string means the server reported no hover.
func (c *client) hover(ctx context.Context, path string, line, col int) (string, error) {
	if err := c.open(ctx, path); err != nil {
		return "", err
	}
	result, err := c.request(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     map[string]any{"line": line, "character": col},
	})
	if err != nil {
		return "", fmt.Errorf("requesting hover: %w", err)
	}
	return parseHover(result)
}

// definition issues a textDocument/definition request for the position and
// returns the locations the server resolves it to.
func (c *client) definition(ctx context.Context, path string, line, col int) ([]Location, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	result, err := c.request(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     map[string]any{"line": line, "character": col},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting definition: %w", err)
	}
	return parseDefinition(result)
}

// references issues a textDocument/references request for the position and
// returns every location the server reports referencing the symbol, including
// its declaration.
func (c *client) references(ctx context.Context, path string, line, col int) ([]Location, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	result, err := c.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     map[string]any{"line": line, "character": col},
		"context":      map[string]any{"includeDeclaration": true},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting references: %w", err)
	}
	return parseReferences(result)
}

// rename issues a textDocument/rename request for the position and returns the
// edits the server would apply to rename the symbol to newName.
func (c *client) rename(ctx context.Context, path string, line, col int, newName string) (WorkspaceEdit, error) {
	if err := c.open(ctx, path); err != nil {
		return WorkspaceEdit{}, err
	}
	result, err := c.request(ctx, "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     map[string]any{"line": line, "character": col},
		"newName":      newName,
	})
	if err != nil {
		return WorkspaceEdit{}, fmt.Errorf("requesting rename: %w", err)
	}
	return parseRename(result)
}

// parseReferences extracts the locations of a textDocument/references response.
// The result is an array of Locations or null, so it is normalized into
// []Location, reusing the definition array parser.
func parseReferences(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("parsing references response: unexpected value %q", string(raw))
	}
	return parseLocationArray(raw)
}

// parseRename extracts the file edits of a textDocument/rename response. Only
// the WorkspaceEdit "changes" map ({uri: [{range, newText}]}) is parsed; the
// keys are URIs and are converted to file paths.
func parseRename(raw json.RawMessage) (WorkspaceEdit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return WorkspaceEdit{}, nil
	}
	var result struct {
		Changes map[string][]struct {
			Range   wireRange `json:"range"`
			NewText string    `json:"newText"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return WorkspaceEdit{}, fmt.Errorf("parsing rename response: %w", err)
	}
	if len(result.Changes) == 0 {
		return WorkspaceEdit{}, nil
	}
	changes := make(map[string][]TextEdit, len(result.Changes))
	for uri, wireEdits := range result.Changes {
		path, err := uriToPath(uri)
		if err != nil {
			return WorkspaceEdit{}, fmt.Errorf("parsing rename edit uri: %w", err)
		}
		edits := make([]TextEdit, 0, len(wireEdits))
		for _, edit := range wireEdits {
			edits = append(edits, TextEdit{
				Range: Range{
					Start: Position{Line: edit.Range.Start.Line, Character: edit.Range.Start.Character},
					End:   Position{Line: edit.Range.End.Line, Character: edit.Range.End.Character},
				},
				NewText: edit.NewText,
			})
		}
		changes[path] = edits
	}
	return WorkspaceEdit{Changes: changes}, nil
}

// parseHover extracts the textual contents of a textDocument/hover response.
// The contents field may be a MarkupContent object, a MarkedString (a bare
// string or {language, value} object), or an array of MarkedStrings, so each
// shape is handled and joined into a single string.
func parseHover(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var result struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parsing hover response: %w", err)
	}
	parts, err := markupStrings(result.Contents)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, "\n"), nil
}

// markupStrings flattens a hover contents value into its constituent strings.
func markupStrings(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("parsing hover contents: %w", err)
		}
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("parsing hover contents: %w", err)
		}
		var out []string
		for _, item := range items {
			parts, err := markupStrings(item)
			if err != nil {
				return nil, err
			}
			out = append(out, parts...)
		}
		return out, nil
	default:
		// MarkupContent ({kind, value}) or MarkedString ({language, value}):
		// both carry the displayable text in the "value" field.
		var obj struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("parsing hover contents: %w", err)
		}
		if obj.Value == "" {
			return nil, nil
		}
		return []string{obj.Value}, nil
	}
}

// wireLocation mirrors the LSP Location structure.
type wireLocation struct {
	URI   string    `json:"uri"`
	Range wireRange `json:"range"`
}

// wireLocationLink mirrors the LSP LocationLink structure returned by some
// servers in place of a plain Location.
type wireLocationLink struct {
	TargetURI   string    `json:"targetUri"`
	TargetRange wireRange `json:"targetRange"`
}

// parseDefinition extracts the locations of a textDocument/definition response.
// The result may be a single Location, an array of Locations, an array of
// LocationLinks, or null, so each shape is normalized into []Location.
func parseDefinition(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '[':
		return parseLocationArray(raw)
	case '{':
		loc, ok, err := parseSingleLocation(raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return []Location{loc}, nil
	default:
		return nil, fmt.Errorf("parsing definition response: unexpected value %q", string(raw))
	}
}

func parseLocationArray(raw json.RawMessage) ([]Location, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing definition response: %w", err)
	}
	out := make([]Location, 0, len(items))
	for _, item := range items {
		loc, ok, err := parseSingleLocation(item)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, loc)
		}
	}
	return out, nil
}

// parseSingleLocation parses one Location or LocationLink object. It reports
// ok=false when the object carries neither a uri nor a targetUri.
func parseSingleLocation(raw json.RawMessage) (Location, bool, error) {
	var both struct {
		wireLocation
		wireLocationLink
	}
	if err := json.Unmarshal(raw, &both); err != nil {
		return Location{}, false, fmt.Errorf("parsing definition location: %w", err)
	}
	uri := both.URI
	rng := both.wireLocation.Range
	if uri == "" && both.TargetURI != "" {
		uri = both.TargetURI
		rng = both.TargetRange
	}
	if uri == "" {
		return Location{}, false, nil
	}
	path, err := uriToPath(uri)
	if err != nil {
		return Location{}, false, fmt.Errorf("parsing definition location uri: %w", err)
	}
	return Location{
		Path: path,
		Range: Range{
			Start: Position{Line: rng.Start.Line, Character: rng.Start.Character},
			End:   Position{Line: rng.End.Line, Character: rng.End.Character},
		},
	}, true, nil
}
