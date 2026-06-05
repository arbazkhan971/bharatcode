package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsNotebookPath(t *testing.T) {
	cases := map[string]bool{
		"analysis.ipynb":     true,
		"dir/Foo.IPYNB":      true,
		"notebook.ipynb.bak": false,
		"main.go":            false,
		"ipynb":              false,
		"/abs/path/x.ipynb":  true,
	}
	for path, want := range cases {
		if got := isNotebookPath(path); got != want {
			t.Errorf("isNotebookPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRenderNotebook_CellsAndOutputs(t *testing.T) {
	ec := 3
	nb := nbNotebook{
		Cells: []nbCell{
			{
				CellType: "markdown",
				Source:   json.RawMessage(`["# Title\n", "intro"]`),
			},
			{
				CellType:       "code",
				ExecutionCount: &ec,
				Source:         json.RawMessage(`["import numpy as np\n", "print(np.pi)"]`),
				Outputs: []nbOutput{
					{OutputType: "stream", Name: "stdout", Text: json.RawMessage(`"3.14159\n"`)},
				},
			},
			{
				CellType: "code",
				Source:   json.RawMessage(`"1/0"`),
				Outputs: []nbOutput{
					{
						OutputType: "error",
						EName:      "ZeroDivisionError",
						EValue:     "division by zero",
						Traceback:  []string{"Traceback...", "ZeroDivisionError: division by zero"},
					},
				},
			},
		},
	}
	data, err := json.Marshal(nb)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := renderNotebook(data)
	if !ok {
		t.Fatal("renderNotebook returned ok=false for a valid notebook")
	}

	for _, want := range []string{
		"[Cell 1 · markdown]",
		"# Title",
		"intro",
		"[Cell 2 · code · execution_count 3]",
		"import numpy as np",
		"print(np.pi)",
		"[stdout]",
		"3.14159",
		"[Cell 3 · code]",
		"1/0",
		"[error] ZeroDivisionError: division by zero",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered notebook missing %q.\nGot:\n%s", want, got)
		}
	}
	// Outputs must be indented to distinguish them from cell source.
	if !strings.Contains(got, "  [stdout]") {
		t.Errorf("stream output not indented.\nGot:\n%s", got)
	}
}

func TestRenderNotebook_RichResultAndAnsi(t *testing.T) {
	nb := nbNotebook{
		Cells: []nbCell{
			{
				CellType: "code",
				Source:   json.RawMessage(`"df.head()"`),
				Outputs: []nbOutput{
					{
						OutputType: "execute_result",
						Data: map[string]json.RawMessage{
							"text/plain": json.RawMessage(`"   col\n0    1"`),
							"text/html":  json.RawMessage(`"<table>"`),
						},
					},
					{
						OutputType: "display_data",
						Data: map[string]json.RawMessage{
							"image/png": json.RawMessage(`"base64..."`),
						},
					},
					{
						OutputType: "stream",
						Name:       "stderr",
						Text:       json.RawMessage(`"\u001b[31mred\u001b[0m warning"`),
					},
				},
			},
		},
	}
	data, _ := json.Marshal(nb)
	got, ok := renderNotebook(data)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(got, "[result]") || !strings.Contains(got, "col") {
		t.Errorf("text/plain result not rendered.\nGot:\n%s", got)
	}
	// Non-text rich output should advertise its mime type, not dump bytes.
	if !strings.Contains(got, "[result: image/png]") {
		t.Errorf("image output mime not listed.\nGot:\n%s", got)
	}
	if strings.Contains(got, "base64...") {
		t.Errorf("image bytes leaked into output.\nGot:\n%s", got)
	}
	// ANSI escapes must be stripped from outputs.
	if strings.Contains(got, "\x1b[") {
		t.Errorf("ANSI escapes not stripped.\nGot:\n%q", got)
	}
	if !strings.Contains(got, "red") || !strings.Contains(got, "warning") {
		t.Errorf("stderr text lost during ANSI strip.\nGot:\n%s", got)
	}
}

func TestRenderNotebook_Invalid(t *testing.T) {
	if _, ok := renderNotebook([]byte(`{"not": "a notebook"}`)); ok {
		t.Error("expected ok=false for a JSON object without cells")
	}
	if _, ok := renderNotebook([]byte(`not json at all`)); ok {
		t.Error("expected ok=false for non-JSON input")
	}
}

func TestRenderNotebook_NbformatV3Worksheets(t *testing.T) {
	pn := 1
	nb := nbNotebook{
		Worksheets: []struct {
			Cells []nbCell `json:"cells"`
		}{
			{Cells: []nbCell{
				{
					CellType:     "code",
					PromptNumber: &pn,
					Input:        json.RawMessage(`["print('hi')"]`),
					Outputs: []nbOutput{
						{OutputType: "pyout", Text: json.RawMessage(`["hi\n"]`)},
					},
				},
			}},
		},
	}
	data, _ := json.Marshal(nb)
	got, ok := renderNotebook(data)
	if !ok {
		t.Fatalf("ok=false for nbformat v3 notebook")
	}
	if !strings.Contains(got, "print('hi')") || !strings.Contains(got, "execution_count 1") {
		t.Errorf("v3 worksheet cell not rendered.\nGot:\n%s", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("v3 pyout text not rendered.\nGot:\n%s", got)
	}
}

func TestViewTool_RendersNotebook(t *testing.T) {
	dir := t.TempDir()
	nbPath := filepath.Join(dir, "demo.ipynb")
	raw := `{
		"cells": [
			{"cell_type": "code", "source": ["x = 1\n", "x + 1"],
			 "outputs": [{"output_type": "stream", "name": "stdout", "text": ["2\n"]}]}
		],
		"metadata": {}, "nbformat": 4, "nbformat_minor": 5
	}`
	if err := os.WriteFile(nbPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := newViewTool(Dependencies{WorkDir: dir})
	args, _ := json.Marshal(map[string]any{"path": "demo.ipynb"})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("view returned error: %s", res.Content)
	}
	// Should be the rendered transcript with line numbers, not raw JSON.
	if strings.Contains(res.Content, `"cell_type"`) {
		t.Errorf("view dumped raw notebook JSON instead of rendering it:\n%s", res.Content)
	}
	for _, want := range []string{"[Cell 1 · code]", "x = 1", "x + 1", "[stdout]", "2"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("view notebook output missing %q.\nGot:\n%s", want, res.Content)
		}
	}
}
