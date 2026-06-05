package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// isNotebookPath reports whether path names a Jupyter notebook.
func isNotebookPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ipynb")
}

// nbNotebook mirrors the parts of the nbformat schema we render. nbformat 4 puts
// cells at the top level; nbformat 3 nests them under worksheets.
type nbNotebook struct {
	Cells      []nbCell `json:"cells"`
	Worksheets []struct {
		Cells []nbCell `json:"cells"`
	} `json:"worksheets"`
}

type nbCell struct {
	CellType       string          `json:"cell_type"`
	Source         json.RawMessage `json:"source"`
	Input          json.RawMessage `json:"input"` // nbformat 3 code source
	ExecutionCount *int            `json:"execution_count"`
	PromptNumber   *int            `json:"prompt_number"` // nbformat 3
	Outputs        []nbOutput      `json:"outputs"`
}

type nbOutput struct {
	OutputType string                     `json:"output_type"`
	Name       string                     `json:"name"`
	Text       json.RawMessage            `json:"text"`
	Data       map[string]json.RawMessage `json:"data"`
	EName      string                     `json:"ename"`
	EValue     string                     `json:"evalue"`
	Traceback  []string                   `json:"traceback"`
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// renderNotebook turns raw .ipynb JSON into a compact, agent-friendly transcript
// of cells and their outputs. It returns ok=false when the bytes do not parse as
// a notebook so the caller can fall back to showing the raw text.
func renderNotebook(data []byte) (string, bool) {
	var nb nbNotebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return "", false
	}
	cells := nb.Cells
	if len(cells) == 0 {
		for _, ws := range nb.Worksheets {
			cells = append(cells, ws.Cells...)
		}
	}
	if len(cells) == 0 {
		return "", false
	}

	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString("\n")
		}
		kind := cell.CellType
		if kind == "" {
			kind = "unknown"
		}
		header := fmt.Sprintf("[Cell %d · %s", i+1, kind)
		if n := cellNumber(cell); n != nil {
			header += fmt.Sprintf(" · execution_count %d", *n)
		}
		header += "]"
		b.WriteString(header)
		b.WriteString("\n")

		src := nbSource(cell.Source)
		if src == "" {
			src = nbSource(cell.Input)
		}
		if strings.TrimSpace(src) != "" {
			b.WriteString(strings.TrimRight(src, "\n"))
			b.WriteString("\n")
		}

		for _, out := range cell.Outputs {
			rendered := renderNotebookOutput(out)
			if rendered != "" {
				b.WriteString(rendered)
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func cellNumber(cell nbCell) *int {
	if cell.ExecutionCount != nil {
		return cell.ExecutionCount
	}
	return cell.PromptNumber
}

func renderNotebookOutput(out nbOutput) string {
	switch out.OutputType {
	case "stream":
		name := out.Name
		if name == "" {
			name = "stdout"
		}
		return indentOutput(fmt.Sprintf("[%s]\n%s", name, cleanOutput(nbSource(out.Text))))
	case "error", "pyerr":
		head := strings.TrimSpace(out.EName + ": " + out.EValue)
		head = strings.TrimSuffix(head, ":")
		body := cleanOutput(strings.Join(out.Traceback, "\n"))
		if body != "" {
			return indentOutput("[error] " + head + "\n" + body)
		}
		return indentOutput("[error] " + head)
	case "execute_result", "display_data", "pyout":
		if txt := nbDataText(out.Data); txt != "" {
			return indentOutput("[result]\n" + cleanOutput(txt))
		}
		if mimes := nbDataMimes(out.Data); mimes != "" {
			return indentOutput("[result: " + mimes + "]")
		}
		// nbformat 3 stored text directly on the output.
		if txt := cleanOutput(nbSource(out.Text)); txt != "" {
			return indentOutput("[result]\n" + txt)
		}
		return ""
	default:
		return ""
	}
}

// nbDataText returns the text/plain representation of a rich output when present.
func nbDataText(data map[string]json.RawMessage) string {
	if data == nil {
		return ""
	}
	if raw, ok := data["text/plain"]; ok {
		return strings.TrimRight(nbSource(raw), "\n")
	}
	return ""
}

// nbDataMimes lists the available mime types for a non-text rich output (e.g. an
// image/png plot) so the model knows something was produced without the bytes.
func nbDataMimes(data map[string]json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	mimes := make([]string, 0, len(data))
	for k := range data {
		mimes = append(mimes, k)
	}
	// Deterministic order for stable output.
	for i := 0; i < len(mimes); i++ {
		for j := i + 1; j < len(mimes); j++ {
			if mimes[j] < mimes[i] {
				mimes[i], mimes[j] = mimes[j], mimes[i]
			}
		}
	}
	return strings.Join(mimes, ", ")
}

// nbSource coerces a notebook source/text field, which may be either a JSON
// string or an array of line strings, into a single string.
func nbSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, "")
	}
	return ""
}

func cleanOutput(s string) string {
	return strings.TrimRight(ansiEscape.ReplaceAllString(s, ""), "\n")
}

func indentOutput(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}
