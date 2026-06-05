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

// documentSymbol issues a textDocument/documentSymbol request and returns the
// symbols the server reports for the file. The path is supplied so symbols can
// carry it, since a DocumentSymbol response omits the document uri.
func (c *client) documentSymbol(ctx context.Context, path string) ([]Symbol, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	result, err := c.request(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting document symbols: %w", err)
	}
	return parseDocumentSymbols(path, result)
}

// workspaceSymbol issues a workspace/symbol request for query and returns the
// matching symbols the server reports across the workspace.
func (c *client) workspaceSymbol(ctx context.Context, query string) ([]Symbol, error) {
	result, err := c.request(ctx, "workspace/symbol", map[string]any{
		"query": query,
	})
	if err != nil {
		return nil, fmt.Errorf("requesting workspace symbols: %w", err)
	}
	return parseWorkspaceSymbols(result)
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

// codeAction issues a textDocument/codeAction request for the range and returns
// the quick fixes and refactorings the server offers. The context.diagnostics
// field is required by the LSP spec; an empty array asks for all available
// actions rather than ones scoped to specific diagnostics.
func (c *client) codeAction(ctx context.Context, path string, rng Range) ([]CodeAction, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	result, err := c.request(ctx, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"range": map[string]any{
			"start": map[string]any{"line": rng.Start.Line, "character": rng.Start.Character},
			"end":   map[string]any{"line": rng.End.Line, "character": rng.End.Character},
		},
		"context": map[string]any{"diagnostics": []any{}},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting code actions: %w", err)
	}
	return parseCodeActions(result)
}

// format issues a textDocument/formatting request for the document and returns
// the edits the server would apply to reformat it. The options field is
// required by the LSP spec; gopls and other servers override these with their
// own configuration, so the values only need to be present and valid.
func (c *client) format(ctx context.Context, path string) ([]TextEdit, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	result, err := c.request(ctx, "textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"options": map[string]any{
			"tabSize":      4,
			"insertSpaces": false,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting formatting: %w", err)
	}
	return parseFormatting(result)
}

// parseFormatting extracts the edits of a textDocument/formatting response. The
// result is a flat array of TextEdit ({range, newText}) or null, so it is
// normalized into []TextEdit.
func parseFormatting(raw json.RawMessage) ([]TextEdit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("parsing formatting response: unexpected value %q", string(raw))
	}
	var items []struct {
		Range   wireRange `json:"range"`
		NewText string    `json:"newText"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing formatting response: %w", err)
	}
	out := make([]TextEdit, 0, len(items))
	for _, item := range items {
		out = append(out, TextEdit{
			Range:   convertRange(item.Range),
			NewText: item.NewText,
		})
	}
	return out, nil
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

// parseRename extracts the file edits of a textDocument/rename response, which
// is a WorkspaceEdit or null.
func parseRename(raw json.RawMessage) (WorkspaceEdit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return WorkspaceEdit{}, nil
	}
	edit, err := parseWorkspaceEdit(raw)
	if err != nil {
		return WorkspaceEdit{}, fmt.Errorf("parsing rename response: %w", err)
	}
	return edit, nil
}

// wireTextEdit is the on-the-wire shape of a single text edit, shared by both
// the "changes" and "documentChanges" forms of a WorkspaceEdit. An
// AnnotatedTextEdit carries an extra "annotationId" that is irrelevant here, so
// it decodes through this same struct.
type wireTextEdit struct {
	Range   wireRange `json:"range"`
	NewText string    `json:"newText"`
}

// toTextEdits converts wire edits to the package TextEdit type.
func toTextEdits(wireEdits []wireTextEdit) []TextEdit {
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
	return edits
}

// parseWorkspaceEdit parses a WorkspaceEdit object. Both encodings the LSP spec
// allows are accepted: the "changes" map ({uri: [{range, newText}]}) and the
// "documentChanges" array of TextDocumentEdits ([{textDocument: {uri}, edits:
// [...]}]). Servers that advertise documentChanges support (gopls,
// rust-analyzer, typescript-language-server, ...) return the latter for rename
// and code actions, so both must be handled or those tools silently see no
// edits. URIs are converted to file paths. Pure resource operations in
// documentChanges (create/rename/delete file, identified by a "kind" field) are
// skipped: the text-edit model cannot represent them. An edit with no text
// changes yields a zero WorkspaceEdit.
func parseWorkspaceEdit(raw json.RawMessage) (WorkspaceEdit, error) {
	var result struct {
		Changes         map[string][]wireTextEdit `json:"changes"`
		DocumentChanges []json.RawMessage         `json:"documentChanges"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return WorkspaceEdit{}, fmt.Errorf("parsing workspace edit: %w", err)
	}

	changes := make(map[string][]TextEdit)
	for uri, wireEdits := range result.Changes {
		path, err := uriToPath(uri)
		if err != nil {
			return WorkspaceEdit{}, fmt.Errorf("parsing workspace edit uri: %w", err)
		}
		changes[path] = toTextEdits(wireEdits)
	}

	// documentChanges entries are either TextDocumentEdits (carrying edits) or
	// resource operations (carrying a "kind"). Decode only the former; multiple
	// entries may target the same file, so edits accumulate per path.
	for _, entry := range result.DocumentChanges {
		var probe struct {
			Kind         string `json:"kind"`
			TextDocument *struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []wireTextEdit `json:"edits"`
		}
		if err := json.Unmarshal(entry, &probe); err != nil {
			return WorkspaceEdit{}, fmt.Errorf("parsing workspace document change: %w", err)
		}
		// Resource operations (create/rename/delete) set "kind" and carry no
		// textDocument; skip them rather than fail the whole edit.
		if probe.Kind != "" || probe.TextDocument == nil {
			continue
		}
		path, err := uriToPath(probe.TextDocument.URI)
		if err != nil {
			return WorkspaceEdit{}, fmt.Errorf("parsing workspace edit uri: %w", err)
		}
		changes[path] = append(changes[path], toTextEdits(probe.Edits)...)
	}

	if len(changes) == 0 {
		return WorkspaceEdit{}, nil
	}
	return WorkspaceEdit{Changes: changes}, nil
}

// parseCodeActions extracts the actions of a textDocument/codeAction response.
// The result is an array whose entries are each either a bare Command
// ({title, command: "<string>", arguments?}) or a CodeAction
// ({title, kind?, edit?, command?: Command}), or null. Each entry is normalized
// into a CodeAction.
func parseCodeActions(raw json.RawMessage) ([]CodeAction, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("parsing code actions response: unexpected value %q", string(raw))
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing code actions response: %w", err)
	}
	out := make([]CodeAction, 0, len(items))
	for _, item := range items {
		action, err := parseCodeAction(item)
		if err != nil {
			return nil, err
		}
		out = append(out, action)
	}
	return out, nil
}

// parseCodeAction parses one entry of a code action response. A "command" field
// holding a JSON string marks a bare Command; an object or absent "command"
// marks a CodeAction, matching how the LSP spec multiplexes both shapes into
// one array.
func parseCodeAction(raw json.RawMessage) (CodeAction, error) {
	if isCommandEntry(raw) {
		command, err := parseCommand(raw)
		if err != nil {
			return CodeAction{}, err
		}
		return CodeAction{Title: command.Title, Command: command}, nil
	}

	var wire struct {
		Title   string          `json:"title"`
		Kind    string          `json:"kind"`
		Edit    json.RawMessage `json:"edit"`
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return CodeAction{}, fmt.Errorf("parsing code action: %w", err)
	}
	action := CodeAction{Title: wire.Title, Kind: wire.Kind}
	if len(wire.Edit) > 0 && string(wire.Edit) != "null" {
		edit, err := parseWorkspaceEdit(wire.Edit)
		if err != nil {
			return CodeAction{}, fmt.Errorf("parsing code action edit: %w", err)
		}
		action.Edit = edit
	}
	if len(wire.Command) > 0 && string(wire.Command) != "null" {
		command, err := parseCommand(wire.Command)
		if err != nil {
			return CodeAction{}, err
		}
		action.Command = command
	}
	return action, nil
}

// isCommandEntry reports whether a code action array entry is a bare Command,
// distinguished from a CodeAction by a "command" field holding a JSON string
// rather than an object.
func isCommandEntry(raw json.RawMessage) bool {
	var probe struct {
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.Command) > 0 && probe.Command[0] == '"'
}

// parseCommand parses one LSP Command object ({title, command, arguments?}).
func parseCommand(raw json.RawMessage) (*Command, error) {
	var wire struct {
		Title     string            `json:"title"`
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("parsing command: %w", err)
	}
	return &Command{
		Title:     wire.Title,
		Command:   wire.Command,
		Arguments: wire.Arguments,
	}, nil
}

// wireDocumentSymbol mirrors the LSP DocumentSymbol structure, a hierarchical
// symbol scoped to a single document. The document uri is not repeated on each
// entry, so the caller supplies the path.
type wireDocumentSymbol struct {
	Name     string               `json:"name"`
	Kind     int                  `json:"kind"`
	Range    wireRange            `json:"range"`
	Children []wireDocumentSymbol `json:"children"`
}

// wireSymbolInformation mirrors the LSP SymbolInformation/WorkspaceSymbol
// structure, a flat symbol that carries its own location. WorkspaceSymbol may
// omit the location range, leaving only the uri.
type wireSymbolInformation struct {
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	ContainerName string `json:"containerName"`
	Location      struct {
		URI   string    `json:"uri"`
		Range wireRange `json:"range"`
	} `json:"location"`
}

// parseDocumentSymbols extracts the symbols of a textDocument/documentSymbol
// response. The result is either an array of DocumentSymbol (hierarchical) or
// SymbolInformation (flat), or null. Hierarchical children are flattened into a
// single slice so every named construct is returned.
func parseDocumentSymbols(path string, raw json.RawMessage) ([]Symbol, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("parsing document symbols response: unexpected value %q", string(raw))
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing document symbols response: %w", err)
	}
	out := make([]Symbol, 0, len(items))
	for _, item := range items {
		// A "location" field is the discriminator for SymbolInformation; a
		// DocumentSymbol has none and instead carries an inline range and
		// optional children.
		if hasLocationField(item) {
			symbol, ok, err := parseSymbolInformation(item)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, symbol)
			}
			continue
		}
		symbols, err := flattenDocumentSymbol(path, item)
		if err != nil {
			return nil, err
		}
		out = append(out, symbols...)
	}
	return out, nil
}

// flattenDocumentSymbol parses one DocumentSymbol object and appends it and all
// of its descendants, recording each child's parent name as its container.
func flattenDocumentSymbol(path string, raw json.RawMessage) ([]Symbol, error) {
	var node wireDocumentSymbol
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("parsing document symbol: %w", err)
	}
	return appendDocumentSymbol(nil, path, "", node), nil
}

func appendDocumentSymbol(out []Symbol, path, container string, node wireDocumentSymbol) []Symbol {
	out = append(out, Symbol{
		Name:          node.Name,
		Kind:          SymbolKind(node.Kind),
		Path:          path,
		Range:         convertRange(node.Range),
		ContainerName: container,
	})
	for _, child := range node.Children {
		out = appendDocumentSymbol(out, path, node.Name, child)
	}
	return out
}

// parseWorkspaceSymbols extracts the symbols of a workspace/symbol response.
// The result is an array of SymbolInformation or WorkspaceSymbol, or null.
func parseWorkspaceSymbols(raw json.RawMessage) ([]Symbol, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("parsing workspace symbols response: unexpected value %q", string(raw))
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing workspace symbols response: %w", err)
	}
	out := make([]Symbol, 0, len(items))
	for _, item := range items {
		symbol, ok, err := parseSymbolInformation(item)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, symbol)
		}
	}
	return out, nil
}

// parseSymbolInformation parses one SymbolInformation or WorkspaceSymbol
// object. It reports ok=false when the entry carries no location uri.
func parseSymbolInformation(raw json.RawMessage) (Symbol, bool, error) {
	var info wireSymbolInformation
	if err := json.Unmarshal(raw, &info); err != nil {
		return Symbol{}, false, fmt.Errorf("parsing symbol information: %w", err)
	}
	if info.Location.URI == "" {
		return Symbol{}, false, nil
	}
	path, err := uriToPath(info.Location.URI)
	if err != nil {
		return Symbol{}, false, fmt.Errorf("parsing symbol information uri: %w", err)
	}
	return Symbol{
		Name:          info.Name,
		Kind:          SymbolKind(info.Kind),
		Path:          path,
		Range:         convertRange(info.Location.Range),
		ContainerName: info.ContainerName,
	}, true, nil
}

// hasLocationField reports whether a JSON object carries a non-null "location"
// field, the discriminator between SymbolInformation and DocumentSymbol.
func hasLocationField(raw json.RawMessage) bool {
	var probe struct {
		Location json.RawMessage `json:"location"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.Location) > 0 && string(probe.Location) != "null"
}

// convertRange converts a wire range into the exported Range type.
func convertRange(r wireRange) Range {
	return Range{
		Start: Position{Line: r.Start.Line, Character: r.Start.Character},
		End:   Position{Line: r.End.Line, Character: r.End.Character},
	}
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
