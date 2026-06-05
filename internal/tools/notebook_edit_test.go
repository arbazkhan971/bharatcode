package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sampleNotebook is a minimal nbformat 4 notebook with two code cells, one
// carrying an output and an execution count, plus top-level metadata and format
// fields that edits must preserve.
const sampleNotebook = `{
 "cells": [
  {
   "cell_type": "code",
   "execution_count": 1,
   "metadata": {},
   "outputs": [
    {
     "name": "stdout",
     "output_type": "stream",
     "text": ["hello\n"]
    }
   ],
   "source": ["print('hello')\n"]
  },
  {
   "cell_type": "markdown",
   "metadata": {},
   "source": ["# Title\n"]
  }
 ],
 "metadata": {
  "kernelspec": {"display_name": "Python 3", "language": "python", "name": "python3"}
 },
 "nbformat": 4,
 "nbformat_minor": 5
}
`

// parseCells decodes a notebook's cells into generic maps for assertions.
func parseCells(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var nb struct {
		Cells []map[string]any `json:"cells"`
	}
	require.NoError(t, json.Unmarshal(data, &nb))
	return nb.Cells
}

func TestApplyNotebookEditReplacePreservesStructure(t *testing.T) {
	out, summary, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		CellNumber: 1,
		NewSource:  "print('updated')\n",
	})
	require.NoError(t, err)
	require.Equal(t, "replaced cell 1", summary)

	// The output stays valid JSON and the second cell + metadata survive.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &top))
	require.Contains(t, string(top["metadata"]), "kernelspec")
	require.Equal(t, "4", strings.TrimSpace(string(top["nbformat"])))

	cells := parseCells(t, out)
	require.Len(t, cells, 2)
	// Source is stored as an array of line strings, not a bare string.
	require.Equal(t, []any{"print('updated')\n"}, cells[0]["source"])
	// Editing the code invalidates the old outputs and execution count.
	require.Equal(t, []any{}, cells[0]["outputs"])
	require.Nil(t, cells[0]["execution_count"])
	// The untouched markdown cell is preserved verbatim.
	require.Equal(t, "markdown", cells[1]["cell_type"])
	require.Equal(t, []any{"# Title\n"}, cells[1]["source"])
}

func TestApplyNotebookEditReplaceMultilineSource(t *testing.T) {
	out, _, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		CellNumber: 1,
		NewSource:  "a = 1\nb = 2\nprint(a + b)",
	})
	require.NoError(t, err)
	cells := parseCells(t, out)
	// Each line keeps its trailing newline except the last (nbformat convention).
	require.Equal(t, []any{"a = 1\n", "b = 2\n", "print(a + b)"}, cells[0]["source"])
}

func TestApplyNotebookEditReplaceChangesType(t *testing.T) {
	out, _, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		CellNumber: 1,
		NewSource:  "# Now markdown\n",
		CellType:   "markdown",
	})
	require.NoError(t, err)
	cells := parseCells(t, out)
	require.Equal(t, "markdown", cells[0]["cell_type"])
	// Converting to markdown drops code-only fields entirely.
	_, hasOutputs := cells[0]["outputs"]
	require.False(t, hasOutputs)
	_, hasExec := cells[0]["execution_count"]
	require.False(t, hasExec)
}

func TestApplyNotebookEditInsertAtPosition(t *testing.T) {
	out, summary, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		EditMode:   "insert",
		CellNumber: 1,
		NewSource:  "import os\n",
		CellType:   "code",
	})
	require.NoError(t, err)
	require.Equal(t, "inserted code cell at position 1", summary)

	cells := parseCells(t, out)
	require.Len(t, cells, 3)
	require.Equal(t, []any{"import os\n"}, cells[0]["source"])
	require.Equal(t, []any{}, cells[0]["outputs"])
	// Original first cell shifted to position 2, unchanged.
	require.Equal(t, []any{"print('hello')\n"}, cells[1]["source"])
}

func TestApplyNotebookEditInsertAppendsWhenNoPosition(t *testing.T) {
	out, summary, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		EditMode:  "insert",
		NewSource: "## Appended\n",
		CellType:  "markdown",
	})
	require.NoError(t, err)
	require.Equal(t, "inserted markdown cell at position 3", summary)
	cells := parseCells(t, out)
	require.Len(t, cells, 3)
	require.Equal(t, "markdown", cells[2]["cell_type"])
	require.Equal(t, []any{"## Appended\n"}, cells[2]["source"])
}

func TestApplyNotebookEditInsertRequiresCellType(t *testing.T) {
	_, _, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		EditMode:  "insert",
		NewSource: "x = 1\n",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cell_type is required")
}

func TestApplyNotebookEditDelete(t *testing.T) {
	out, summary, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{
		EditMode:   "delete",
		CellNumber: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "deleted cell 1", summary)
	cells := parseCells(t, out)
	require.Len(t, cells, 1)
	require.Equal(t, "markdown", cells[0]["cell_type"])
}

func TestApplyNotebookEditOutOfRange(t *testing.T) {
	_, _, err := applyNotebookEdit([]byte(sampleNotebook), notebookEditArgs{CellNumber: 9})
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestApplyNotebookEditRejectsNonV4(t *testing.T) {
	_, _, err := applyNotebookEdit([]byte(`{"nbformat": 3, "worksheets": []}`), notebookEditArgs{CellNumber: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no top-level")
}

func TestNotebookEditRunReplacesAndRecords(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "nb.ipynb")
	require.NoError(t, os.WriteFile(path, []byte(sampleNotebook), 0o644))

	sessionID := "nb-edit"
	tracker := newToolsTestTracker(t, sessionID)
	view := newViewTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: sessionID})
	tool := newNotebookEditTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: sessionID})

	// View first so the read-before-edit guard is satisfied.
	viewed, err := view.Run(ctx, mustJSON(t, map[string]string{"path": "nb.ipynb"}))
	require.NoError(t, err)
	require.False(t, viewed.IsError)

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":        "nb.ipynb",
		"cell_number": 1,
		"new_source":  "print('changed')\n",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)
	require.Contains(t, result.Content, "replaced cell 1")
	// The result carries a diff so the model sees exactly what changed.
	require.Contains(t, result.Content, "@@")
	require.NotEmpty(t, result.Metadata["diff"])

	// The file on disk is still valid, edited, and records as a write.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), "print('changed')")
	cells := parseCells(t, got)
	require.Equal(t, []any{"print('changed')\n"}, cells[0]["source"])

	changes, err := tracker.ChangesForSession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, changes, 1)
}

func TestNotebookEditRunRefusesUnviewed(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "nb.ipynb")
	require.NoError(t, os.WriteFile(path, []byte(sampleNotebook), 0o644))

	sessionID := "nb-unviewed"
	tracker := newToolsTestTracker(t, sessionID)
	tool := newNotebookEditTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: sessionID})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":        "nb.ipynb",
		"cell_number": 1,
		"new_source":  "x\n",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "has not been read")

	// File is untouched.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sampleNotebook, string(got))
}

func TestNotebookEditRunRejectsNonNotebook(t *testing.T) {
	tool := newNotebookEditTool(Dependencies{WorkDir: t.TempDir(), SessionID: "nb-bad"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":        "main.go",
		"cell_number": 1,
		"new_source":  "x",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "only operates on .ipynb")
}

func TestNotebookEditRegistered(t *testing.T) {
	r := NewRegistry(Dependencies{WorkDir: t.TempDir()})
	_, ok := r.Get("notebook_edit")
	require.True(t, ok)
}
