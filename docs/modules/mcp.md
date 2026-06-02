# mcp

**Path:** `internal/mcp/`
**Status:** Completed

## Purpose

The `mcp` package is BharatCode's Model Context Protocol client. It connects to user-defined MCP servers (configured under `mcp` in `bharatcode.json`), maintains their lifecycle for the duration of a session, and exposes the tools and resources each server advertises as ordinary BharatCode tools. From the agent loop's perspective there is no difference between calling a built-in tool (`bash`, `view`, `edit`) and calling an MCP-provided tool (`filesystem__read_file`, `github__create_issue`); both implement the same `tools.Tool` interface and are subject to the same permission check.

This module owns three concerns and nothing else. First, transport: stdio (spawn a subprocess and speak JSON-RPC over its stdin/stdout), HTTP (long-lived POST/stream over a URL), and SSE (server-sent events for streaming responses). Second, discovery: enumerate tools and resources after a successful handshake and refresh them when the server signals a change. Third, resilience: reconnect with exponential backoff on transient failures, surface terminal failures via `ErrToolUnavailable`, and never let a misbehaving MCP server stall the agent loop.

Configuration lives in `internal/config/`; permission gating lives in `internal/permission/`. This module is the glue between MCP servers and the rest of BharatCode, packaged behind a small `Client` struct that the `app` wiring code instantiates once per session.

## Public interface

```go
// Package mcp implements BharatCode's Model Context Protocol client.
// It connects to user-defined MCP servers, discovers their tools and
// resources, and bridges those tools into BharatCode's internal tool
// registry so the agent loop treats them identically to built-ins.
package mcp

// Transport names the wire protocol used to reach an MCP server.
// Valid values are "stdio", "http", and "sse". Any other value is
// rejected at config-validation time, not at Start.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

// ServerConfig describes a single MCP server. Name is the unique
// identifier used to prefix tool names ("filesystem" produces
// "filesystem__read_file"); it must match the regex
// [a-z][a-z0-9_]{0,31}. Command is required for stdio transport and
// ignored otherwise. URL is required for http and sse transports and
// ignored otherwise. Env adds or overrides environment variables for
// the spawned process under stdio transport.
type ServerConfig struct {
	Name      string            `json:"name"`
	Transport Transport         `json:"transport"`
	Command   []string          `json:"command,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Timeout   time.Duration     `json:"timeout,omitempty"`
}

// Resource is a server-advertised resource the agent may read by URI.
// MimeType is informational and may be empty if the server did not
// supply it; callers should fall back to content sniffing.
type Resource struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mime_type"`
}

// State reports the connection state of a single MCP server.
type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StateFailed
)

// Event is published on the client's pubsub topic whenever a server's
// state changes or its tool list is refreshed.
type Event struct {
	Server    string
	State     State
	Err       error
	ToolNames []string
}

// ErrToolUnavailable is returned by a bridged MCP tool's Run method
// when the underlying server is disconnected and reconnection has
// failed permanently. The agent loop treats this as a tool-call error
// (Result{IsError: true}) and surfaces it to the LLM.
var ErrToolUnavailable = errors.New("mcp tool unavailable")

// Server represents a single connected MCP server. It is created by
// the Client and not constructed directly. Callers obtain Server
// instances via Client.Servers().
type Server struct {
	// Unexported fields: name, transport, conn, tools, resources, state.
}

// Name returns the configured server name.
func (s *Server) Name() string

// State returns the current connection state.
func (s *Server) State() State

// Client manages all MCP servers configured for the running
// BharatCode session. It is safe for concurrent use.
type Client struct {
	// Unexported fields: cfg, bus, perms, servers, tools, mu.
}

// NewClient constructs a Client that will connect to every server
// listed under cfg.MCP. Servers are not contacted until Start is
// called. Events flow on the supplied pubsub topic; callers that do
// not care may pass a topic with no subscribers.
func NewClient(cfg *config.Config, perms *permission.Checker, bus *pubsub.Topic[Event]) *Client

// Start connects to every configured server in parallel. It returns
// nil as soon as the connection phase finishes; per-server failures
// are reported via the event bus, not as a Start error. A nil return
// guarantees only that the dial attempt completed, not that every
// server is healthy. Use Client.Servers to inspect per-server state.
func (c *Client) Start(ctx context.Context) error

// Stop disconnects every server. It is idempotent and safe to call
// from a signal handler. Stop blocks until each server has either
// closed cleanly or ctx is canceled.
func (c *Client) Stop(ctx context.Context) error

// Tools returns the flattened list of MCP-bridged tools across every
// server. The returned slice is a snapshot; callers must call Tools
// again after a tool-list-changed Event to pick up new tools.
func (c *Client) Tools() []tools.Tool

// Resources returns the flattened list of resources across every
// server.
func (c *Client) Resources() []Resource

// ReadResource fetches the contents of a resource by URI. The URI
// must include a server prefix of the form "<server>://..." matching
// the server that owns the resource. It returns the raw bytes and
// the MIME type reported by the server (may be empty).
func (c *Client) ReadResource(ctx context.Context, uri string) ([]byte, string, error)

// Servers returns the list of configured servers with their current
// state. The order matches the order in config.
func (c *Client) Servers() []*Server
```

## Dependencies

- `internal/util` — `util.ExpandPath` for resolving stdio command paths that contain `~` or `$HOME`.
- `internal/config` — `config.Config.MCP` is the source of `ServerConfig` slices.
- `internal/permission` — every bridged tool's `Run` calls `permission.Checker.Check` before forwarding to the server.
- `internal/pubsub` — `Event` is published on a `pubsub.Topic[Event]` supplied by the caller.
- `internal/tools` — bridged servers expose `tools.Tool` via an adapter type defined in this package. (`tools` does not import `mcp`; `mcp` produces `tools.Tool` values and the agent registry consumes them.)
- External: `github.com/mark3labs/mcp-go` for the underlying client. Use only the high-level `client` package and its `transport` subpackages.

## Acceptance criteria

1. `go test ./internal/mcp/...` passes on linux, darwin, and windows runners.
2. `go test -race ./internal/mcp/...` passes with no data-race reports.
3. `go vet ./internal/mcp/...` and `golangci-lint run ./internal/mcp/...` are clean.
4. An integration test (build tag `integration`) connects to `npx -y @modelcontextprotocol/server-filesystem .` over stdio, lists tools, and successfully calls `filesystem__read_file` on a fixture path; the result round-trips back to the test as a string.
5. Discovered tools surface in `Client.Tools()` with names of the form `<server>__<tool>`. Collision: two servers exposing a tool called `read_file` produce `serverA__read_file` and `serverB__read_file`; no panic, no silent overwrite.
6. JSON schema reported by an MCP tool is passed through to `Tool.Schema()` byte-for-byte (round-trip via `bytes.Equal` in a unit test using a recorded server fixture under `testdata/`).
7. Permission gating: a unit test with a stub `permission.Checker` that returns `denied` causes the bridged tool's `Run` to return `Result{IsError: true}` and **not** call the server. Confirm the server-side call counter stays at zero.
8. Disconnection: when the stdio child process exits unexpectedly, the client publishes `Event{State: StateDisconnected}`, attempts reconnect with exponential backoff (starting at 500ms, doubling, capped at 30s, jitter ±20%), publishes `Event{State: StateConnecting}` for each attempt, and after five consecutive failures publishes `Event{State: StateFailed}`. Subsequent `Run` calls return `ErrToolUnavailable`.
9. Reconnection success: after a transient failure, a successful reconnect re-discovers tools and publishes `Event{ToolNames: ...}` reflecting the (possibly changed) tool set.
10. `Client.Stop(ctx)` terminates all stdio child processes within 5 seconds; on timeout it sends `SIGKILL` (Unix) or `Process.Kill()` (Windows) and returns the timeout error wrapped via `fmt.Errorf`.
11. `ReadResource(ctx, "filesystem:///etc/hosts")` returns the resource bytes and the MIME type reported by the server; unknown URIs return an error wrapping `mcp-go`'s underlying error.
12. Configuration validation: an unknown `Transport` value, a name not matching `[a-z][a-z0-9_]{0,31}`, missing `Command` for stdio, or missing `URL` for http/sse causes `config.Validate` to fail with a clear message identifying the offending server (this acceptance criterion is enforced by `internal/config`; mcp ships the regex constant and a `ValidateServerConfig` helper that config calls).
13. `NewClient` is cheap and pure — it does not touch the network or spawn processes. A unit test constructs a `Client` with five servers and asserts zero file descriptors are opened.
14. Tool names that would exceed 64 runes after the `<server>__<tool>` join are truncated with a `…` mid-name; a deterministic suffix derived from `sha1(server+tool)[:6]` is appended to keep names unique. Documented and unit-tested.

## Notes for the implementer

- Use `mcp-go`'s `client.NewStdioClient`, `client.NewHTTPClient`, and `client.NewSSEClient`. Do not implement the JSON-RPC framing yourself.
- The `<server>__<tool>` separator is two underscores to make collisions with single-underscore tool names unambiguous. The regex `[a-z][a-z0-9_]{0,31}` on server names ensures the prefix is shell-safe and stays out of the way in LLM prompts.
- Reconnect backoff uses `math/rand/v2` for jitter; seed with the package-level `rand.NewPCG` initialized in `init()` from `time.Now().UnixNano()`. No global mutable state otherwise.
- Stdio child processes inherit BharatCode's environment by default, with `ServerConfig.Env` applied on top. Do not pass through `BHARATCODE_*` variables; filter them out to avoid leaking session state into untrusted MCP servers.
- Per-call timeouts default to `ServerConfig.Timeout` (zero means 60s). Honor `ctx.Done()` and return `ctx.Err()` wrapped, not `ErrToolUnavailable`, on cancellation.
- The adapter that turns an MCP tool into a `tools.Tool` should embed a reference to the parent `*Server` and check `Server.State()` before forwarding. Disconnected servers short-circuit to `Result{IsError: true, Content: "mcp server disconnected: " + name}` and return `ErrToolUnavailable` as the Go error for logging.
- Hooks (`PreToolUse`/`PostToolUse`) are fired by the agent's `hooked_tool.go` decorator, **not** here. Bridged tools must remain plain `tools.Tool` implementations so the decorator can wrap them uniformly.
- Logging: use `slog` with a `slog.With("mcp_server", name)` logger held on the `*Server`. Log level guidance: connection events at `Info`, reconnect attempts at `Warn`, terminal failure at `Error`, every tool-call result at `Debug`.
- This module is not the place for skill loading, prompt enrichment, or sampling. Resources are raw byte buffers; interpretation lives upstream.
- Future-proofing: `mcp-go` v0.x has had breaking changes. Pin a specific version in `go.mod` and document the version in a `docs/decisions/` ADR before bumping.

## Implementation status

Implemented:

- `internal/mcp` public types, server config validation, client lifecycle, server snapshots, tool/resource discovery, resource reads, and tool-name truncation.
- Stdio, streamable HTTP, and SSE production connectors through `github.com/mark3labs/mcp-go` pinned at `v0.40.0`.
- MCP tool adapters that preserve input schema bytes when available, bridge tool results into `tools.Result`, check `permission.Checker`, and short-circuit disconnected servers with `ErrToolUnavailable`.
- Event publication for connection, failure, disconnection, and refreshed tool lists.
- Reconnect loop with exponential backoff, jitter, and a five-attempt terminal failure state.
- Focused unit tests for validation, discovery, schema pass-through, permission denial, unavailable tools, resource reading, connection failure events, and name truncation.
- Integration test behind `-tags=integration` for the stdio filesystem server.

Deviations and follow-ups:

- `mcp-go` v0.40.0 requires Go 1.23+, so `go.mod` now uses `go 1.23.0`.
- The stdio transport close path delegates process shutdown to `mcp-go`; this implementation does not add a separate SIGKILL fallback around the child process because the dependency does not expose the child process handle.
- `internal/tools` currently contains only the shared `Tool` and `Result` interface needed by MCP. The full built-in tools registry remains for the tools module.
