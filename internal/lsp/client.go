package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
)

type client struct {
	spec      languageSpec
	root      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	cancel    context.CancelFunc
	done      chan struct{}
	requestID atomic.Int64

	mu              sync.Mutex
	writeMu         sync.Mutex
	pending         map[int64]chan responseMessage
	opened          map[string]struct{}
	versions        map[string]int
	diagnosticCache map[string][]Diagnostic
	diagnosticWait  map[string][]chan struct{}
	pullDiagnostic  bool
	closed          bool
}

func startClient(ctx context.Context, spec languageSpec, root string) (*client, error) {
	if _, err := exec.LookPath(spec.command); err != nil {
		return nil, fmt.Errorf("finding language server %s: %w", spec.command, err)
	}

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, spec.command, spec.args...)
	cmd.Dir = root
	cmd.SysProcAttr = sysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opening language server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opening language server stdout: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting language server %s: %w", spec.command, err)
	}

	c := &client{
		spec:            spec,
		root:            root,
		cmd:             cmd,
		stdin:           stdin,
		cancel:          cancel,
		done:            make(chan struct{}),
		pending:         make(map[int64]chan responseMessage),
		opened:          make(map[string]struct{}),
		versions:        make(map[string]int),
		diagnosticCache: make(map[string][]Diagnostic),
		diagnosticWait:  make(map[string][]chan struct{}),
	}
	go c.readLoop(stdout)
	go c.waitLoop()

	if err := c.initialize(ctx); err != nil {
		_ = c.forceKill()
		return nil, err
	}
	return c, nil
}

func (c *client) initialize(ctx context.Context) error {
	result, err := c.request(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(c.root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"diagnostic": map[string]any{
					"dynamicRegistration": false,
				},
				"hover": map[string]any{
					"dynamicRegistration": false,
				},
				"definition": map[string]any{
					"dynamicRegistration": false,
				},
				"references": map[string]any{
					"dynamicRegistration": false,
				},
				"rename": map[string]any{
					"dynamicRegistration": false,
				},
				"documentSymbol": map[string]any{
					"dynamicRegistration":               false,
					"hierarchicalDocumentSymbolSupport": true,
				},
				"formatting": map[string]any{
					"dynamicRegistration": false,
				},
				"rangeFormatting": map[string]any{
					"dynamicRegistration": false,
				},
				"codeAction": map[string]any{
					"dynamicRegistration": false,
				},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{
					"dynamicRegistration": false,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("initializing language server: %w", err)
	}

	var initResult struct {
		Capabilities struct {
			DiagnosticProvider any `json:"diagnosticProvider"`
		} `json:"capabilities"`
	}
	if len(result) > 0 {
		if err := json.Unmarshal(result, &initResult); err != nil {
			return fmt.Errorf("parsing initialize response: %w", err)
		}
	}
	c.pullDiagnostic = initResult.Capabilities.DiagnosticProvider != nil

	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("sending initialized notification: %w", err)
	}
	return nil
}

func (c *client) diagnostics(ctx context.Context, path string) ([]Diagnostic, error) {
	if err := c.open(ctx, path); err != nil {
		return nil, err
	}
	if c.pullDiagnostic {
		result, err := c.request(ctx, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": pathToURI(path)},
		})
		if err != nil {
			return nil, fmt.Errorf("requesting diagnostics: %w", err)
		}
		diagnostics, err := parsePullDiagnostics(path, result)
		if err != nil {
			return nil, err
		}
		c.setDiagnostics(path, diagnostics)
		return diagnostics, nil
	}

	diagnostics, ok := c.cachedDiagnostics(path)
	if ok {
		return diagnostics, nil
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for diagnostics: %w", ctx.Err())
	case <-c.waitDiagnostics(path):
		diagnostics, _ := c.cachedDiagnostics(path)
		return diagnostics, nil
	}
}

func (c *client) open(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving document path: %w", err)
	}

	c.mu.Lock()
	if _, ok := c.opened[abs]; ok {
		c.mu.Unlock()
		return nil
	}
	c.opened[abs] = struct{}{}
	c.versions[abs] = 1
	c.mu.Unlock()

	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("reading document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("opening document: %w", err)
	}
	err = c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        pathToURI(abs),
			"languageId": c.spec.languageID,
			"version":    1,
			"text":       string(data),
		},
	})
	if err != nil {
		return fmt.Errorf("sending didOpen notification: %w", err)
	}
	return nil
}

// change re-syncs an already-open document with its current on-disk contents
// and drops any cached diagnostics for it, so the next diagnostics request
// blocks for the server's analysis of the new text rather than returning the
// version the server first saw. If the document was never opened it is opened
// instead (open performs the initial sync). This is used after the agent edits
// a file so the language server reports problems against the edit.
func (c *client) change(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving document path: %w", err)
	}

	c.mu.Lock()
	_, isOpen := c.opened[abs]
	c.mu.Unlock()
	if !isOpen {
		return c.open(ctx, abs)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("reading document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("changing document: %w", err)
	}

	c.mu.Lock()
	c.versions[abs]++
	version := c.versions[abs]
	// Invalidate the cache so push-model servers' next diagnostics request waits
	// for the publish triggered by this change instead of the stale analysis.
	delete(c.diagnosticCache, abs)
	c.mu.Unlock()

	err = c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     pathToURI(abs),
			"version": version,
		},
		// Full-document sync: send the whole file as a single change with no
		// range. Every server that declares text sync support accepts this form.
		"contentChanges": []map[string]any{
			{"text": string(data)},
		},
	})
	if err != nil {
		return fmt.Errorf("sending didChange notification: %w", err)
	}
	return nil
}

func (c *client) shutdown(ctx context.Context) error {
	if _, err := c.request(ctx, "shutdown", nil); err != nil {
		slog.DebugContext(ctx, "Language server shutdown request failed", "language", c.spec.name, "error", err)
	}
	if err := c.notify("exit", nil); err != nil {
		slog.DebugContext(ctx, "Language server exit notification failed", "language", c.spec.name, "error", err)
	}

	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		if err := c.forceKill(); err != nil {
			return fmt.Errorf("killing language server after shutdown timeout: %w", err)
		}
		return fmt.Errorf("shutting down language server: %w", ctx.Err())
	}
}

func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.requestID.Add(1)
	ch := make(chan responseMessage, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("language server is closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	err := c.write(requestMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		c.removePending(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, fmt.Errorf("waiting for language server response: %w", ctx.Err())
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("language server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *client) notify(method string, params any) error {
	return c.write(notificationMessage{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  params,
	})
}

func (c *client) write(payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := writePayload(c.stdin, payload); err != nil {
		return fmt.Errorf("writing language server message: %w", err)
	}
	return nil
}

func (c *client) readLoop(stdout io.Reader) {
	defer c.closePending()

	reader := bufio.NewReader(stdout)
	for {
		raw, err := readPayload(reader)
		if err != nil {
			if !isClosedReadError(err) {
				slog.Debug("Language server reader stopped", "language", c.spec.name, "error", err)
			}
			return
		}

		var msg incomingMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			slog.Debug("Invalid language server message", "language", c.spec.name, "error", err)
			continue
		}
		msg.raw = raw
		switch {
		case msg.ID != nil && msg.Method != "":
			// A message carrying BOTH an id and a method is a server->client
			// request, not a response. Route it to the request handler so the
			// server is answered; treating it as a response would corrupt the
			// pending channel for the client's own request of the same id.
			c.handleServerRequest(*msg.ID, msg.Method)
		case msg.ID != nil:
			// An id with no method is a response to one of our requests.
			c.deliverResponse(responseMessage{
				JSONRPC: jsonRPCVersion,
				ID:      *msg.ID,
				Result:  msg.Result,
				Error:   msg.Error,
			})
		case msg.Method == "textDocument/publishDiagnostics":
			c.handlePublishDiagnostics(msg.Params)
		default:
			// Any other notification (method, no id) is ignored.
		}
	}
}

// jsonRPCMethodNotFound is the standard JSON-RPC error code for an
// unrecognized method.
const jsonRPCMethodNotFound = -32601

// handleServerRequest answers a server-initiated request so the language
// server does not stall waiting for a reply. Requests that expect data are
// answered with a minimal valid result; unrecognized requests get a
// MethodNotFound error.
func (c *client) handleServerRequest(id int64, method string) {
	switch method {
	case "workspace/configuration":
		// Reply with an empty configuration item. A null entry is a valid
		// "no configuration" answer that every server tolerates.
		c.respond(id, json.RawMessage(`[null]`), nil)
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		// These expect an empty success result.
		c.respond(id, json.RawMessage(`null`), nil)
	default:
		c.respond(id, nil, &responseError{
			Code:    jsonRPCMethodNotFound,
			Message: fmt.Sprintf("method not supported: %s", method),
		})
	}
}

// respond writes a response to a server-initiated request.
func (c *client) respond(id int64, result json.RawMessage, respErr *responseError) {
	if err := c.write(responseMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
		Error:   respErr,
	}); err != nil {
		slog.Debug("Failed to answer language server request", "language", c.spec.name, "error", err)
	}
}

func (c *client) waitLoop() {
	_ = c.cmd.Wait()
	c.cancel()
	close(c.done)
}

func (c *client) handlePublishDiagnostics(raw json.RawMessage) {
	var params struct {
		URI         string           `json:"uri"`
		Diagnostics []wireDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		slog.Debug("Invalid diagnostics notification", "language", c.spec.name, "error", err)
		return
	}
	path, err := uriToPath(params.URI)
	if err != nil {
		slog.Debug("Invalid diagnostics uri", "language", c.spec.name, "error", err)
		return
	}
	c.setDiagnostics(path, convertDiagnostics(path, params.Diagnostics))
}

func (c *client) setDiagnostics(path string, diagnostics []Diagnostic) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}

	c.mu.Lock()
	c.diagnosticCache[path] = diagnostics
	waiters := c.diagnosticWait[path]
	delete(c.diagnosticWait, path)
	c.mu.Unlock()

	for _, waiter := range waiters {
		close(waiter)
	}
}

func (c *client) cachedDiagnostics(path string) ([]Diagnostic, bool) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	diagnostics, ok := c.diagnosticCache[path]
	return append([]Diagnostic(nil), diagnostics...), ok
}

func (c *client) waitDiagnostics(path string) <-chan struct{} {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}

	waiter := make(chan struct{})
	c.mu.Lock()
	if _, ok := c.diagnosticCache[path]; ok {
		close(waiter)
	} else {
		c.diagnosticWait[path] = append(c.diagnosticWait[path], waiter)
	}
	c.mu.Unlock()
	return waiter
}

func (c *client) deliverResponse(resp responseMessage) {
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	if ok {
		delete(c.pending, resp.ID)
	}
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (c *client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *client) closePending() {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan responseMessage)
	c.mu.Unlock()

	resp := responseMessage{Error: &responseError{Code: -32000, Message: "language server closed"}}
	for _, ch := range pending {
		ch <- resp
	}
}

func (c *client) forceKill() error {
	c.cancel()
	if c.cmd.Process == nil {
		return nil
	}
	if err := killProcessGroup(c.cmd.Process.Pid); err != nil && !isProcessDone(err) {
		return fmt.Errorf("killing language server process group: %w", err)
	}
	return nil
}

func isClosedReadError(err error) bool {
	return err == io.EOF
}

func isProcessDone(err error) bool {
	return err == os.ErrProcessDone
}
