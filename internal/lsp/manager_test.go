package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsPullStartsServerAndDeduplicatesPublishes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	topic := pubsub.NewTopic[Diagnostic]("test_lsp", 16)
	events, cancel := topic.Subscribe()
	defer cancel()

	manager := NewManager(testConfig("go", "fake-lsp"), topic)
	// Generous timeout: the fake server re-execs this test binary, which can be
	// slow to start when the full suite runs packages in parallel under load.
	// The test asserts behavior, not latency, so the headroom is harmless.
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	diagnostics, err := manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	require.Equal(t, Error, diagnostics[0].Severity)
	require.Equal(t, "fake diagnostic", diagnostics[0].Message)

	first := receiveDiagnostic(t, events)
	require.Equal(t, diagnostics[0].Message, first.Message)

	diagnostics, err = manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	requireNoDiagnostic(t, events)

	require.NoError(t, manager.Shutdown(ctx))
}

// TestDiagnosticsSurvivesServerInitiatedRequest proves that a server->client
// request carrying an id that collides with the client's own request ids does
// not corrupt the client's pending response. On the old id-first routing, the
// server's workspace/configuration request (id=1) was delivered as a bogus
// nil response into the channel awaiting the client's own id=1, yielding empty
// diagnostics. With method-aware routing the diagnostics arrive intact.
func TestDiagnosticsSurvivesServerInitiatedRequest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "serverrequest")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	diagnostics, err := manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1, "diagnostics must survive the colliding server request")
	require.Equal(t, Error, diagnostics[0].Severity)
	require.Equal(t, "fake diagnostic", diagnostics[0].Message)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestDiagnosticsPushFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "push")+string(os.PathListSeparator)+os.Getenv("PATH"))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	// Generous timeout: the fake server re-execs this test binary, which can be
	// slow to start when the full suite runs packages in parallel under load.
	// The test asserts behavior, not latency, so the headroom is harmless.
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	diagnostics, err := manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	require.Equal(t, Warning, diagnostics[0].Severity)
	require.Equal(t, "push diagnostic", diagnostics[0].Message)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestHoverReturnsServerText(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	text, err := manager.Hover(ctx, source, 0, 0)
	require.NoError(t, err)
	require.Equal(t, "fake hover text", text)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestDefinitionReturnsLocations(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	locations, err := manager.Definition(ctx, source, 0, 0)
	require.NoError(t, err)
	require.Len(t, locations, 1)
	require.Equal(t, source, locations[0].Path)
	require.Equal(t, Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: 0, Character: 4},
	}, locations[0].Range)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestReferencesReturnsLocations(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	locations, err := manager.References(ctx, source, 0, 0)
	require.NoError(t, err)
	require.Equal(t, []Location{
		{
			Path: source,
			Range: Range{
				Start: Position{Line: 0, Character: 0},
				End:   Position{Line: 0, Character: 4},
			},
		},
		{
			Path: source,
			Range: Range{
				Start: Position{Line: 2, Character: 0},
				End:   Position{Line: 2, Character: 4},
			},
		},
	}, locations)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestRenameReturnsEdits(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	edit, err := manager.Rename(ctx, source, 0, 0, "Renamed")
	require.NoError(t, err)
	require.Equal(t, WorkspaceEdit{
		Changes: map[string][]TextEdit{
			source: {{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 4},
				},
				NewText: "Renamed",
			}},
		},
	}, edit)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestDocumentSymbolsReturnsSymbols(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	symbols, err := manager.DocumentSymbols(ctx, source)
	require.NoError(t, err)
	require.Equal(t, []Symbol{
		{
			Name:  "main",
			Kind:  Function,
			Path:  source,
			Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}},
		},
		{
			Name:  "Server",
			Kind:  Struct,
			Path:  source,
			Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}},
		},
		{
			Name:          "Start",
			Kind:          Method,
			Path:          source,
			Range:         Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}},
			ContainerName: "Server",
		},
	}, symbols)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestWorkspaceSymbolsReturnsMatches(t *testing.T) {
	// Resolve symlinks in the temp dir so the expected path matches what the
	// fake server reports. The server derives its symbol URI from os.Getwd()
	// (after the test chdirs into tmp), which canonicalizes symlinks — e.g. on
	// macOS /tmp -> /private/tmp. EvalSymlinks is a no-op where the temp dir is
	// already canonical (Linux), so this only corrects the comparison, never
	// weakens it: the assertion stays full-path equality.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("PATH", fakeServerPath(t, "pull")+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.test\n"), 0o644))
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	manager := NewManager(testConfig("go", "fake-lsp"), nil)
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()

	wantRange := Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 4}}
	// The fake server builds its location uris from its working directory, which
	// the runtime resolves through any symlinks (on macOS /tmp -> /private/tmp),
	// so the expected path must be resolved the same way.
	resolved, err := filepath.EvalSymlinks(tmp)
	require.NoError(t, err)
	wantSource := filepath.Join(resolved, "main.go")

	// "Ser" matches only "Server"; the server filters by the query.
	symbols, err := manager.WorkspaceSymbols(ctx, "Ser")
	require.NoError(t, err)
	require.Equal(t, []Symbol{
		{Name: "Server", Kind: Struct, Path: wantSource, Range: wantRange},
	}, symbols)

	// "S" matches both "Server" and "Start".
	symbols, err = manager.WorkspaceSymbols(ctx, "S")
	require.NoError(t, err)
	require.Equal(t, []Symbol{
		{Name: "Server", Kind: Struct, Path: wantSource, Range: wantRange},
		{Name: "Start", Kind: Method, Path: wantSource, Range: wantRange, ContainerName: "Server"},
	}, symbols)

	require.NoError(t, manager.Shutdown(ctx))
}

func TestMissingServerWarnsOnceAndDegrades(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\n"), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWd)) })

	topic := pubsub.NewTopic[Diagnostic]("test_lsp_missing", 16)
	events, cancel := topic.Subscribe()
	defer cancel()

	manager := NewManager(testConfig("go", "definitely-missing-language-server"), topic)
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	diagnostics, err := manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Empty(t, diagnostics)

	warning := receiveDiagnostic(t, events)
	require.Equal(t, Warning, warning.Severity)
	require.Contains(t, warning.Message, "not available")

	diagnostics, err = manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	requireNoDiagnostic(t, events)
	require.NoError(t, manager.Shutdown(ctx))
}

func TestDiagnosticsUnsupportedExtensionDoesNothing(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "README.md")
	require.NoError(t, os.WriteFile(source, []byte("# test\n"), 0o644))

	manager := NewManager(testConfig("go", "definitely-missing-language-server"), nil)
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	diagnostics, err := manager.Diagnostics(ctx, source)
	require.NoError(t, err)
	require.Empty(t, diagnostics)
}

func testConfig(language, command string) *config.Config {
	cfg := config.Default()
	cfg.LSP = []config.LSPServer{{
		Name:      "test",
		Command:   command,
		Languages: []string{language},
		RootFiles: []string{"go.mod"},
	}}
	return cfg
}

func receiveDiagnostic(t *testing.T, events <-chan Diagnostic) Diagnostic {
	t.Helper()
	select {
	case diagnostic := <-events:
		return diagnostic
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for diagnostic")
		return Diagnostic{}
	}
}

func requireNoDiagnostic(t *testing.T, events <-chan Diagnostic) {
	t.Helper()
	select {
	case diagnostic := <-events:
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	case <-time.After(100 * time.Millisecond):
	}
}

func fakeServerPath(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	name := "fake-lsp"
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".bat"
		content := fmt.Sprintf("@echo off\r\nset BHARATCODE_FAKE_LSP=1\r\nset BHARATCODE_FAKE_LSP_MODE=%s\r\n\"%s\" -test.run=TestFakeLSPServer --\r\n", mode, os.Args[0])
		require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
		return dir
	}

	content := fmt.Sprintf("#!/bin/sh\nBHARATCODE_FAKE_LSP=1 BHARATCODE_FAKE_LSP_MODE=%s %q -test.run=TestFakeLSPServer --\n", mode, os.Args[0])
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return dir
}

func TestFakeLSPServer(t *testing.T) {
	if os.Getenv("BHARATCODE_FAKE_LSP") != "1" {
		return
	}
	runFakeLSPServer()
	os.Exit(0)
}

func runFakeLSPServer() {
	reader := bufio.NewReader(os.Stdin)
	mode := os.Getenv("BHARATCODE_FAKE_LSP_MODE")
	for {
		raw, err := readPayload(reader)
		if err != nil {
			return
		}
		var msg incomingMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		switch msg.Method {
		case "initialize":
			result := map[string]any{
				"capabilities": map[string]any{},
			}
			if mode == "pull" || mode == "serverrequest" {
				result["capabilities"] = map[string]any{
					"diagnosticProvider": map[string]any{
						"interFileDependencies": false,
						"workspaceDiagnostics":  false,
					},
				}
			}
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result:  mustRaw(result),
			})
		case "textDocument/didOpen":
			if mode == "push" {
				var params struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(msg.Params, &params)
				_ = writePayload(os.Stdout, notificationMessage{
					JSONRPC: jsonRPCVersion,
					Method:  "textDocument/publishDiagnostics",
					Params: map[string]any{
						"uri": params.TextDocument.URI,
						"diagnostics": []map[string]any{{
							"range":    fakeRange(),
							"severity": int(Warning),
							"message":  "push diagnostic",
							"source":   "fake",
						}},
					},
				})
			}
		case "textDocument/diagnostic":
			if mode == "serverrequest" {
				// Fire a server->client request that REUSES the client's
				// in-flight request id. Fire-and-forget: a correct client
				// replies (a response with no method, which it ignores) and
				// still matches the real response below to its pending channel.
				// Buggy id-first routing instead delivers this as a nil-result
				// response into that channel, yielding zero diagnostics.
				_ = writePayload(os.Stdout, requestMessage{
					JSONRPC: jsonRPCVersion,
					ID:      *msg.ID,
					Method:  "workspace/configuration",
					Params:  map[string]any{"items": []map[string]any{{"section": "fake"}}},
				})
			}
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw(map[string]any{
					"kind": "full",
					"items": []map[string]any{{
						"range":    fakeRange(),
						"severity": int(Error),
						"message":  "fake diagnostic",
						"source":   "fake",
					}},
				}),
			})
		case "textDocument/hover":
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw(map[string]any{
					"contents": map[string]any{
						"kind":  "markdown",
						"value": "fake hover text",
					},
					"range": fakeRange(),
				}),
			})
		case "textDocument/definition":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw([]map[string]any{{
					"uri":   params.TextDocument.URI,
					"range": fakeRange(),
				}}),
			})
		case "textDocument/references":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw([]map[string]any{
					{
						"uri":   params.TextDocument.URI,
						"range": fakeRange(),
					},
					{
						"uri": params.TextDocument.URI,
						"range": map[string]any{
							"start": map[string]any{"line": 2, "character": 0},
							"end":   map[string]any{"line": 2, "character": 4},
						},
					},
				}),
			})
		case "textDocument/documentSymbol":
			// Answer with hierarchical DocumentSymbol nodes (no location uri),
			// including a nested child, so the flattening path is exercised.
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw([]map[string]any{
					{
						"name":  "main",
						"kind":  int(Function),
						"range": fakeRange(),
					},
					{
						"name":  "Server",
						"kind":  int(Struct),
						"range": fakeRange(),
						"children": []map[string]any{{
							"name":  "Start",
							"kind":  int(Method),
							"range": fakeRange(),
						}},
					},
				}),
			})
		case "workspace/symbol":
			var params struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// The fake server runs with its working directory set to the
			// workspace root, so the source file lives alongside it.
			workspaceDir, _ := os.Getwd()
			uri := pathToURI(filepath.Join(workspaceDir, "main.go"))
			// Return SymbolInformation entries whose name contains the query, so
			// the test can assert real query filtering.
			all := []map[string]any{
				{
					"name": "Server",
					"kind": int(Struct),
					"location": map[string]any{
						"uri":   uri,
						"range": fakeRange(),
					},
				},
				{
					"name":          "Start",
					"kind":          int(Method),
					"containerName": "Server",
					"location": map[string]any{
						"uri":   uri,
						"range": fakeRange(),
					},
				},
				{
					"name": "Helper",
					"kind": int(Function),
					"location": map[string]any{
						"uri":   uri,
						"range": fakeRange(),
					},
				},
			}
			matches := make([]map[string]any, 0, len(all))
			for _, sym := range all {
				if params.Query == "" || strings.Contains(sym["name"].(string), params.Query) {
					matches = append(matches, sym)
				}
			}
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result:  mustRaw(matches),
			})
		case "textDocument/rename":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				NewName string `json:"newName"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result: mustRaw(map[string]any{
					"changes": map[string]any{
						params.TextDocument.URI: []map[string]any{{
							"range":   fakeRange(),
							"newText": params.NewName,
						}},
					},
				}),
			})
		case "shutdown":
			_ = writePayload(os.Stdout, responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result:  mustRaw(nil),
			})
		case "exit":
			return
		}
	}
}

func fakeRange() map[string]any {
	return map[string]any{
		"start": map[string]any{"line": 0, "character": 0},
		"end":   map[string]any{"line": 0, "character": 4},
	}
}

func mustRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
