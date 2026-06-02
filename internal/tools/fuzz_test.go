package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// offlineTransport fails every HTTP round-trip so the web tools can never reach
// the real network during fuzzing, even if the mutator synthesizes a valid URL.
type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network disabled during fuzzing")
}

// installOfflineWebClients replaces the package-level HTTP clients and endpoint
// used by web_fetch and web_search with offline-only stubs, restoring the
// originals when the test ends. It must run before any tool is constructed,
// because the web tools capture their client at construction time.
func installOfflineWebClients(t *testing.T) {
	t.Helper()
	oldHTTP := httpClient
	oldSearchClient := webSearchClient
	oldSearchEndpoint := webSearchEndpoint
	httpClient = &http.Client{Transport: offlineTransport{}}
	webSearchClient = &http.Client{Transport: offlineTransport{}}
	webSearchEndpoint = "http://127.0.0.1:0/"
	t.Cleanup(func() {
		httpClient = oldHTTP
		webSearchClient = oldSearchClient
		webSearchEndpoint = oldSearchEndpoint
	})
}

// fuzzDeps builds an isolated, offline, default-denying dependency set for the
// fuzz harness. The permission checker is constructed with a nil prompt bus and
// the default Auto approval mode, so any tool that asks for permission (bash,
// edit, write, multiedit) is denied instead of executing. The workspace is a
// throwaway temp directory so no fuzzed argument can touch real files.
func fuzzDeps(t *testing.T) Dependencies {
	t.Helper()
	installOfflineWebClients(t)
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

// assertNoPanic enforces the core invariant the recover wrappers exist to
// guarantee: a tool's Run must never surface a recovered panic. The wrappers
// fold any panic into a Result/error carrying panicSentinel, so its presence in
// either output proves Run panicked.
func assertNoPanic(t *testing.T, tool Tool, raw json.RawMessage, res Result, err error) {
	t.Helper()
	if strings.Contains(res.Content, panicSentinel) {
		t.Fatalf("tool %q panicked (sentinel in content) for args %q: %s", tool.Name(), raw, res.Content)
	}
	if err != nil && strings.Contains(err.Error(), panicSentinel) {
		t.Fatalf("tool %q panicked (sentinel in error) for args %q: %v", tool.Name(), raw, err)
	}
}

// assertToolContract runs one tool with the given raw args and asserts the
// no-panic contract. A tool may legitimately succeed (parseable input), return
// a graceful (IsError, nil) error result, or return a real I/O-style error; the
// only forbidden outcome is a raw panic. The task lists (_, ctx.Err()) as an
// allowed return under cancellation, not a mandated one, so the cancelled path
// holds the same no-panic invariant rather than requiring the context error.
func assertToolContract(t *testing.T, ctx context.Context, tool Tool, raw json.RawMessage) {
	t.Helper()
	res, err := tool.Run(ctx, raw)
	assertNoPanic(t, tool, raw, res, err)
}

// assertGarbageRejected runs one tool with definitely-invalid JSON and asserts
// the positive side of the contract: the tool must report a graceful error
// result (IsError true, nil error) rather than panicking, succeeding silently,
// or returning a non-nil error. This proves tools actively reject garbage, not
// merely that they avoid crashing.
func assertGarbageRejected(t *testing.T, tool Tool, raw json.RawMessage) {
	t.Helper()
	res, err := tool.Run(context.Background(), raw)
	assertNoPanic(t, tool, raw, res, err)
	if err != nil {
		t.Fatalf("tool %q returned non-nil error for garbage args %q: %v", tool.Name(), raw, err)
	}
	if !res.IsError {
		t.Fatalf("tool %q accepted garbage args %q without flagging an error result: %q", tool.Name(), raw, res.Content)
	}
}

// garbageCorpus is the set of inputs that are invalid JSON for every tool, so
// each tool must reject them with a graceful error result regardless of its
// argument shape.
func garbageCorpus() [][]byte {
	return [][]byte{
		[]byte(`{`),
		[]byte(`}`),
		[]byte(`[`),
		[]byte(`]`),
		[]byte(`{"`),
		[]byte(`{"path"`),
		[]byte(`{"path":}`),
		[]byte(`not json at all`),
		[]byte(`{,}`),
		[]byte("{\x00}"),
	}
}

// fuzzCorpus returns a set of real-and-malformed argument blobs reused as the
// seed corpus for every tool fuzz target. Running `go test -run=Fuzz` replays
// these as ordinary tests, proving each tool handles them without panicking.
func fuzzCorpus() [][]byte {
	seeds := [][]byte{
		// Empty and whitespace.
		[]byte(``),
		[]byte(`   `),
		// Structurally broken JSON.
		[]byte(`null`),
		[]byte(`true`),
		[]byte(`123`),
		[]byte(`"a string"`),
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
		[]byte(`{"url": "http://127.0.0.1:0/", "prompt": "x"}`),
		[]byte(`{"query": "anything"}`),
		// Control characters and unicode.
		[]byte("{\"path\": \"\x00\x01\x02\"}"),
		[]byte(`{"path": "日本語/ファイル.go"}`),
		// Deeply nested / large-ish nonsense.
		[]byte(`{"items": [` + strings.Repeat(`{"id":"a","content":"b"},`, 50) + `{}]}`),
	}
	// The pure-garbage inputs belong in the seed corpus too.
	return append(seeds, garbageCorpus()...)
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
		registry := NewRegistry(fuzzDeps(t))
		for _, tool := range registry.List() {
			assertToolContract(t, context.Background(), tool, json.RawMessage(raw))
		}
	})
}

// FuzzToolsUnderCancelledContext repeats the contract checks with an
// already-cancelled context, asserting that no tool panics regardless of how
// far it gets before observing cancellation.
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

// TestToolsRejectGarbageArgs asserts the positive side of the contract: every
// registered tool turns pure-garbage JSON into a graceful (IsError, nil) result
// rather than panicking or silently succeeding.
func TestToolsRejectGarbageArgs(t *testing.T) {
	registry := NewRegistry(fuzzDeps(t))
	for _, tool := range registry.List() {
		for _, garbage := range garbageCorpus() {
			assertGarbageRejected(t, tool, json.RawMessage(garbage))
		}
	}
}

// FuzzViewArgs targets the view tool directly so the corpus stays focused on
// path/offset/limit shapes.
func FuzzViewArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		assertToolContract(t, context.Background(), newViewTool(fuzzDeps(t)), json.RawMessage(raw))
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

// FuzzGrepArgs targets the grep tool directly with the Go fallback forced on,
// so the corpus exercises pattern compilation paths independent of whether
// ripgrep happens to be installed.
func FuzzGrepArgs(f *testing.F) {
	for _, seed := range fuzzCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		old := lookPath
		lookPath = func(string) (string, error) { return "", os.ErrNotExist }
		defer func() { lookPath = old }()
		assertToolContract(t, context.Background(), newGrepTool(fuzzDeps(t)), json.RawMessage(raw))
	})
}
