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
		})
	}
	return out
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
