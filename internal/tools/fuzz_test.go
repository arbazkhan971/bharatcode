package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

// panicSentinel is the marker the recover wrappers (safe.go-style) embed in a
// Result when a tool panics. Its presence in tool output proves a panic slipped
// through, which the fuzz harness treats as a contract violation.
const panicSentinel = "internal tool panic"

// fuzzDeps builds an isolated, offline, default-denying dependency set for the
// fuzz harness. The permission checker is constructed with a nil prompt bus and
// the default Auto approval mode, so any tool that asks for permission (bash,
// edit, write, multiedit) is denied instead of executing. The workspace is a
// throwaway temp directory so no fuzzed argument can touch real files.
func fuzzDeps(t *testing.T) Dependencies {
	t.Helper()
	cfg := &config.Config{
		// Deny every tool explicitly so bash never runs an arbitrary command
		// even if the prompt fallback behaviour changes.
		Permissions: config.PermConfig{Deny: []string{"*"}},
	}
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("tools_fuzz", 8)
	t.Cleanup(bus.Close)
	sh := shell.New(bus)
	t.Cleanup(sh.Shutdown)

	workDir := t.TempDir()
	// Seed a small, real file so path-shaped fuzz inputs occasionally hit a
	// readable file instead of always missing, exercising more code paths.
	_ = os.WriteFile(filepath.Join(workDir, "seed.txt"), []byte("alpha beta gamma\n"), 0o644)

	return Dependencies{
		Config:     cfg,
		Permission: permission.New(cfg, nil),
		Shell:      sh,
		WorkDir:    workDir,
		SessionID:  "fuzz-session",
	}
}

// assertToolContract runs one tool with the given raw args and asserts the
// no-panic contract: the tool must return either an error result with a nil
// error, or a real error (which for a cancelled context must be the context
// error). A recovered panic — detectable by the sentinel the safe wrappers
// inject — is always a failure.
func assertToolContract(t *testing.T, ctx context.Context, tool Tool, raw json.RawMessage) {
	t.Helper()
	res, err := tool.Run(ctx, raw)

	// A recovered panic surfaces as the sentinel in either the Result content
	// or the returned error. Neither is acceptable: the wrapper exists to make
	// the tool safe, but the underlying Run must not panic in the first place
	// is what we prove — and if it did, the sentinel exposes it.
	if strings.Contains(res.Content, panicSentinel) {
		t.Fatalf("tool %q panicked (sentinel in content) for args %q: %s", tool.Name(), raw, res.Content)
	}
	if err != nil && strings.Contains(err.Error(), panicSentinel) {
		t.Fatalf("tool %q panicked (sentinel in error) for args %q: %v", tool.Name(), raw, err)
	}

	switch {
	case err == nil:
		// Success or a graceful error result are both fine. The only hard
		// requirement is that a non-successful outcome is flagged as an error
		// result rather than a silent empty success when the input was junk —
		// but tools may legitimately succeed on parseable inputs, so we only
		// require the absence of a panic here.
		return
	case ctx.Err() != nil:
		// When the context is cancelled, a non-nil error must be the context
		// error (wrapped or bare). Anything else means the tool ignored
		// cancellation and failed for an unrelated reason.
		if !errors.Is(err, ctx.Err()) {
			t.Fatalf("tool %q returned non-context error under cancellation for args %q: %v", tool.Name(), raw, err)
		}
	default:
		// A non-nil error with a live context is allowed only for genuine I/O
		// style failures (e.g. reading a missing file). It must still not be a
		// panic, which we already checked above.
		return
	}
}

// fuzzCorpus returns a set of real-and-malformed argument blobs reused as the
// seed corpus for every tool fuzz target. Running `go test -run=Fuzz` replays
// these as ordinary tests, proving each tool handles them without panicking.
func fuzzCorpus() [][]byte {
	return [][]byte{
		// Empty and whitespace.
		[]byte(``),
		[]byte(`   `),
		// Structurally broken JSON.
		[]byte(`{`),
		[]byte(`}`),
		[]byte(`[`),
		[]byte(`null`),
		[]byte(`true`),
		[]byte(`123`),
		[]byte(`"a string"`),
		[]byte(`{"`),
		[]byte(`{"path"`),
		// Wrong types for known fields.
		[]byte(`{"path": 123}`),
		[]byte(`{"path": ["a", "b"]}`),
		[]byte(`{"path": null, "content": false}`),
		[]byte(`{"offset": "not-a-number", "limit": -1}`),
		[]byte(`{"edits": "not-an-array"}`),
		[]byte(`{"edits": [null, {}, {"old": 1, "new": 2}]}`),
		[]byte(`{"items": {"not": "an array"}}`),
		// Valid-shaped but adversarial values.
		[]byte(`{"path": "../../../../etc/passwd"}`),
		[]byte(`{"path": "seed.txt"}`),
		[]byte(`{"path": "seed.txt", "offset": 1, "limit": 1}`),
		[]byte(`{"path": "seed.txt", "old_string": "alpha", "new_string": "x"}`),
		[]byte(`{"path": "seed.txt", "edits": [{"old": "alpha", "new": "x"}]}`),
		[]byte(`{"path": "new.txt", "content": "hello"}`),
		[]byte(`{"command": "echo should-not-run"}`),
		[]byte(`{"command": "rm -rf /", "timeout": 1}`),
		[]byte(`{"pattern": "**/*.go"}`),
		[]byte(`{"pattern": "[", "path": "."}`),
		[]byte(`{"pattern": "(unclosed", "output_mode": "bogus"}`),
		[]byte(`{"action": "list"}`),
		[]byte(`{"action": "add", "text": "do a thing"}`),
		[]byte(`{"action": "💣", "items": [{"content": "x"}]}`),
		// Control characters and unicode.
		[]byte("{\"path\": \"\x00\x01\x02\"}"),
		[]byte(`{"path": "日本語/ファイル.go"}`),
		// Deeply nested / large-ish nonsense.
		[]byte(`{"items": [` + strings.Repeat(`{"id":"a","content":"b"},`, 50) + `{}]}`),
	}
}

// runFuzzCorpusAgainstAllTools is the shared body for the per-tool fuzz targets
// and for the corpus replay test: it feeds raw bytes to every registered tool
// and enforces the no-panic contract.
func runFuzzCorpusAgainstAllTools(t *testing.T, raw []byte) {
	t.Helper()
	registry := NewRegistry(fuzzDeps(t))
	for _, tool := range registry.List() {
		assertToolContract(t, context.Background(), tool, json.RawMessage(raw))
	}
}

// FuzzAllTools feeds arbitrary bytes as JSON arguments to every registered tool
// and asserts none of them panic. The registry hands back the safeTool wrapper,
// so this exercises both the per-tool recover wrappers and the registry-level
// one. The seed corpus runs under `go test -run=Fuzz`.
func FuzzAllTools(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		runFuzzCorpusAgainstAllTools(t, raw)
	})
}

// FuzzToolsUnderCancelledContext repeats the contract checks with an
// already-cancelled context, asserting that no tool panics and that any error
// returned is the context error rather than an unrelated failure.
func FuzzToolsUnderCancelledContext(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		registry := NewRegistry(fuzzDeps(t))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, tool := range registry.List() {
			assertToolContract(t, ctx, tool, json.RawMessage(raw))
		}
	})
}

// FuzzViewArgs targets the view tool directly so the corpus stays focused on
// path/offset/limit shapes.
func FuzzViewArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	deps := func(t *testing.T) Tool { return newViewTool(fuzzDeps(t)) }
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), deps(t), json.RawMessage(raw))
	})
}

// FuzzEditArgs targets the edit tool directly.
func FuzzEditArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), newEditTool(fuzzDeps(t)), json.RawMessage(raw))
	})
}

// FuzzMultiEditArgs targets the multiedit tool directly.
func FuzzMultiEditArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), newMultiEditTool(fuzzDeps(t)), json.RawMessage(raw))
	})
}

// FuzzWriteArgs targets the write tool directly.
func FuzzWriteArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), newWriteTool(fuzzDeps(t)), json.RawMessage(raw))
	})
}

// FuzzBashArgs targets the bash tool directly. Permission denial in fuzzDeps
// keeps it from executing any command.
func FuzzBashArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), newBashTool(fuzzDeps(t)), json.RawMessage(raw))
	})
}
