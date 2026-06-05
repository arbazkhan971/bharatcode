package lsp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

type wireDiagnostic struct {
	Range    wireRange `json:"range"`
	Severity int       `json:"severity,omitempty"`
	Message  string    `json:"message"`
	Source   string    `json:"source,omitempty"`
	// Code is the rule identifier. The LSP spec allows a string or an integer
	// here, so it is decoded as raw JSON and normalized by codeFromWire.
	Code json.RawMessage `json:"code,omitempty"`
	// Tags classify the diagnostic beyond its severity (1=Unnecessary,
	// 2=Deprecated).
	Tags []int `json:"tags,omitempty"`
	// RelatedInformation links other source locations to this diagnostic, such as
	// the conflicting prior declaration behind a redeclaration error.
	RelatedInformation []wireRelatedInformation `json:"relatedInformation,omitempty"`
}

type wireRelatedInformation struct {
	Location wireLocation `json:"location"`
	Message  string       `json:"message"`
}

type wireRange struct {
	Start wirePosition `json:"start"`
	End   wirePosition `json:"end"`
}

type wirePosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func parsePullDiagnostics(path string, raw json.RawMessage) ([]Diagnostic, error) {
	var result struct {
		Items []wireDiagnostic `json:"items"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing diagnostics response: %w", err)
	}
	return convertDiagnostics(path, result.Items), nil
}

func convertDiagnostics(path string, items []wireDiagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(items))
	for _, item := range items {
		out = append(out, Diagnostic{
			Path: path,
			Range: Range{
				Start: Position{
					Line:      item.Range.Start.Line,
					Character: item.Range.Start.Character,
				},
				End: Position{
					Line:      item.Range.End.Line,
					Character: item.Range.End.Character,
				},
			},
			Severity: severityFromWire(item.Severity),
			Message:  item.Message,
			Source:   item.Source,
			Code:     codeFromWire(item.Code),
			Tags:     tagsFromWire(item.Tags),
			Related:  relatedFromWire(item.RelatedInformation),
		})
	}
	return out
}

// relatedFromWire converts a diagnostic's relatedInformation entries, resolving
// each entry's file URI to a local path. Entries whose URI cannot be parsed
// (e.g. a non-file scheme) are dropped rather than failing the whole
// diagnostic, since they are supplementary context. It returns nil when no
// entries survive so a server that sends none leaves Related empty.
func relatedFromWire(items []wireRelatedInformation) []RelatedInformation {
	if len(items) == 0 {
		return nil
	}
	out := make([]RelatedInformation, 0, len(items))
	for _, item := range items {
		path, err := uriToPath(item.Location.URI)
		if err != nil {
			continue
		}
		out = append(out, RelatedInformation{
			Location: Location{
				Path: path,
				Range: Range{
					Start: Position{
						Line:      item.Location.Range.Start.Line,
						Character: item.Location.Range.Start.Character,
					},
					End: Position{
						Line:      item.Location.Range.End.Line,
						Character: item.Location.Range.End.Character,
					},
				},
			},
			Message: item.Message,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// tagsFromWire converts a diagnostic's wire tag codes to DiagnosticTag values,
// keeping only the recognized ones (1=Unnecessary, 2=Deprecated) so an unknown
// future tag does not surface as a meaningless number. It returns nil when no
// recognized tag survives, leaving Tags empty for servers that send none.
func tagsFromWire(tags []int) []DiagnosticTag {
	if len(tags) == 0 {
		return nil
	}
	out := make([]DiagnosticTag, 0, len(tags))
	for _, t := range tags {
		switch DiagnosticTag(t) {
		case Unnecessary, Deprecated:
			out = append(out, DiagnosticTag(t))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// codeFromWire normalizes an LSP diagnostic "code" to a string. The wire value
// is either a JSON string ("unused-import") or an integer (2304); both are
// rendered as plain text. An absent, null, or otherwise undecodable code yields
// the empty string.
func codeFromWire(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

func severityFromWire(value int) Severity {
	switch value {
	case int(Error), int(Warning), int(Information), int(Hint):
		return Severity(value)
	default:
		return Information
	}
}

func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if runtime.GOOS == "windows" {
		path = filepath.ToSlash(path)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	} else {
		path = filepath.ToSlash(path)
	}
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

func uriToPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing document uri: %w", err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("parsing document uri: unsupported scheme %q", u.Scheme)
	}
	path := u.Path
	if runtime.GOOS == "windows" {
		path = strings.TrimPrefix(path, "/")
	}
	return filepath.FromSlash(path), nil
}
