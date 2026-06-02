package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/arbazkhan971/bharatcode/internal/util"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultTimeout     = 60 * time.Second
	maxReconnectTries  = 5
	initialBackoff     = 500 * time.Millisecond
	maxBackoff         = 30 * time.Second
	maxToolNameRunes   = 64
	reconnectJitterPct = 0.2
	// stopDeadline caps how long Stop waits for a server's Close before
	// force-killing the child process and abandoning the close.
	stopDeadline = 5 * time.Second
)

type remoteClient interface {
	Close() error
	CallTool(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)
	ListTools(context.Context, mcpsdk.ListToolsRequest) (*mcpsdk.ListToolsResult, error)
	ListResources(context.Context, mcpsdk.ListResourcesRequest) (*mcpsdk.ListResourcesResult, error)
	ReadResource(context.Context, mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error)
	OnConnectionLost(func(error))
}

// forceKiller is implemented by remote clients that own a child process and
// can hard-kill it when a graceful Close hangs past the stop deadline.
type forceKiller interface {
	// forceKill terminates the underlying child process. It reports whether a
	// process was actually killed.
	forceKill() bool
}

type connector func(context.Context, ServerConfig) (remoteClient, error)

var newRemote connector = connectMCP

// stdioRemote wraps a stdio-backed remote client so Stop can hard-kill the
// child process when Close hangs. mcp-go keeps the *exec.Cmd in an unexported
// field with no accessor, so the process handle is captured at launch time via
// a custom command factory.
type stdioRemote struct {
	remoteClient
	proc *os.Process
}

// forceKill sends SIGKILL to the captured child process, if any.
func (s *stdioRemote) forceKill() bool {
	if s.proc == nil {
		return false
	}
	// Process.Kill is SIGKILL on Unix; signal explicitly to be unambiguous and
	// fall back to Kill on platforms where Signal is unsupported.
	if err := s.proc.Signal(syscall.SIGKILL); err != nil {
		_ = s.proc.Kill()
	}
	return true
}

var (
	randMu     sync.Mutex
	randSource = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(os.Getpid())))
)

// Server represents a single configured MCP server.
type Server struct {
	name      string
	cfg       ServerConfig
	logger    *slog.Logger
	mu        sync.RWMutex
	conn      remoteClient
	state     State
	tools     []tools.Tool
	resources []Resource
}

// Name returns the configured server name.
func (s *Server) Name() string {
	return s.name
}

// State returns the current connection state.
func (s *Server) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Server) setState(state State, conn remoteClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.conn = conn
}

func (s *Server) snapshot() (State, remoteClient) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state, s.conn
}

// Client manages all configured MCP servers for one session.
type Client struct {
	cfg     *config.Config
	perms   *permission.Checker
	bus     *pubsub.Topic[Event]
	mu      sync.RWMutex
	servers []*Server
}

// NewClient constructs a Client without contacting MCP servers.
func NewClient(cfg *config.Config, perms *permission.Checker, bus *pubsub.Topic[Event]) *Client {
	c := &Client{
		cfg:   cfg,
		perms: perms,
		bus:   bus,
	}
	if cfg == nil {
		return c
	}

	c.servers = make([]*Server, 0, len(cfg.MCP))
	for _, raw := range cfg.MCP {
		if raw.Disabled {
			continue
		}
		command := make([]string, 0, 1+len(raw.Args))
		if raw.Command != "" {
			command = append(command, raw.Command)
		}
		command = append(command, raw.Args...)
		serverCfg := ServerConfig{
			Name:      raw.Name,
			Transport: Transport(raw.Transport),
			Command:   command,
			URL:       raw.URL,
			Env:       raw.Env,
		}
		c.servers = append(c.servers, &Server{
			name:   serverCfg.Name,
			cfg:    serverCfg,
			state:  StateDisconnected,
			logger: slog.With("mcp_server", serverCfg.Name),
		})
	}
	return c
}

// Start connects to every configured server in parallel.
func (c *Client) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, server := range c.Servers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.connectServer(ctx, server)
		}()
	}
	wg.Wait()
	return nil
}

// Stop disconnects every server. Each server gets a bounded deadline (the
// caller's context, capped at stopDeadline); if a server's Close has not
// returned by then, Stop force-kills the child process where the handle is
// reachable and abandons the close goroutine rather than leaking it.
func (c *Client) Stop(ctx context.Context) error {
	deadline := stopDeadline
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	stopCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, len(c.servers))
	for _, server := range c.Servers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.stopServer(stopCtx, server); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// stopServer closes a single server, force-killing its child process if Close
// does not return before stopCtx's deadline. The Close goroutine uses a
// buffered channel so it never blocks on send, even when abandoned.
func (c *Client) stopServer(stopCtx context.Context, server *Server) error {
	_, conn := server.snapshot()
	if conn == nil {
		server.setState(StateDisconnected, nil)
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- conn.Close()
	}()

	select {
	case <-stopCtx.Done():
		killed := false
		if killer, ok := conn.(forceKiller); ok {
			killed = killer.forceKill()
		}
		server.logger.Warn(
			"MCP server did not close before deadline",
			"err", stopCtx.Err(),
			"force_killed", killed,
		)
		// The Close goroutine is abandoned but cannot leak a send: done is
		// buffered. Mark the server disconnected regardless.
		server.setState(StateDisconnected, nil)
		return fmt.Errorf("stopping mcp server %q: %w", server.Name(), stopCtx.Err())
	case err := <-done:
		server.setState(StateDisconnected, nil)
		if err != nil {
			return fmt.Errorf("stopping mcp server %q: %w", server.Name(), err)
		}
		return nil
	}
}

// Tools returns a snapshot of MCP-bridged tools across every server.
func (c *Client) Tools() []tools.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []tools.Tool
	for _, server := range c.servers {
		server.mu.RLock()
		out = append(out, server.tools...)
		server.mu.RUnlock()
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Resources returns a snapshot of resources across every server.
func (c *Client) Resources() []Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []Resource
	for _, server := range c.servers {
		server.mu.RLock()
		out = append(out, server.resources...)
		server.mu.RUnlock()
	}
	return out
}

// ReadResource fetches the contents of a resource by URI.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]byte, string, error) {
	serverName, _, ok := strings.Cut(uri, "://")
	if !ok || serverName == "" {
		return nil, "", fmt.Errorf("reading mcp resource %q: missing server URI prefix", uri)
	}
	server := c.serverByName(serverName)
	if server == nil {
		return nil, "", fmt.Errorf("reading mcp resource %q: unknown server %q", uri, serverName)
	}
	state, conn := server.snapshot()
	if state != StateConnected || conn == nil {
		return nil, "", fmt.Errorf("reading mcp resource %q: %w", uri, ErrToolUnavailable)
	}

	ctx, cancel := withServerTimeout(ctx, server.cfg.Timeout)
	defer cancel()

	result, err := conn.ReadResource(ctx, mcpsdk.ReadResourceRequest{
		Params: mcpsdk.ReadResourceParams{URI: uri},
	})
	if err != nil {
		return nil, "", fmt.Errorf("reading mcp resource %q: %w", uri, err)
	}
	if len(result.Contents) == 0 {
		return nil, "", nil
	}
	return resourceBytes(result.Contents[0])
}

// Servers returns configured servers in config order.
func (c *Client) Servers() []*Server {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Server, len(c.servers))
	copy(out, c.servers)
	return out
}

func (c *Client) connectServer(ctx context.Context, server *Server) {
	c.publish(ctx, Event{Server: server.Name(), State: StateConnecting})
	server.setState(StateConnecting, nil)

	conn, err := newRemote(ctx, server.cfg)
	if err != nil {
		server.logger.WarnContext(ctx, "MCP server connection failed", "err", err)
		c.publish(ctx, Event{Server: server.Name(), State: StateFailed, Err: err})
		server.setState(StateFailed, nil)
		return
	}

	conn.OnConnectionLost(func(err error) {
		c.handleDisconnect(server, err)
	})
	if err := c.refresh(ctx, server, conn); err != nil {
		_ = conn.Close()
		c.publish(ctx, Event{Server: server.Name(), State: StateFailed, Err: err})
		server.setState(StateFailed, nil)
		return
	}
	server.setState(StateConnected, conn)
	names := toolNames(server)
	c.publish(ctx, Event{Server: server.Name(), State: StateConnected, ToolNames: names})
	server.logger.InfoContext(ctx, "MCP server connected")
}

func (c *Client) handleDisconnect(server *Server, lost error) {
	ctx := context.Background()
	server.logger.WarnContext(ctx, "MCP server disconnected", "err", lost)
	server.setState(StateDisconnected, nil)
	c.publish(ctx, Event{Server: server.Name(), State: StateDisconnected, Err: lost})

	backoff := initialBackoff
	for attempt := 0; attempt < maxReconnectTries; attempt++ {
		delay := jitter(backoff)
		timer := time.NewTimer(delay)
		<-timer.C
		c.publish(ctx, Event{Server: server.Name(), State: StateConnecting})
		server.setState(StateConnecting, nil)
		conn, err := newRemote(ctx, server.cfg)
		if err == nil {
			conn.OnConnectionLost(func(err error) {
				c.handleDisconnect(server, err)
			})
			if err = c.refresh(ctx, server, conn); err == nil {
				server.setState(StateConnected, conn)
				c.publish(ctx, Event{
					Server:    server.Name(),
					State:     StateConnected,
					ToolNames: toolNames(server),
				})
				server.logger.InfoContext(ctx, "MCP server reconnected")
				return
			}
			_ = conn.Close()
		}
		server.logger.WarnContext(ctx, "MCP server reconnect failed", "attempt", attempt+1, "err", err)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	server.setState(StateFailed, nil)
	c.publish(ctx, Event{Server: server.Name(), State: StateFailed, Err: lost})
	server.logger.ErrorContext(ctx, "MCP server failed after reconnect attempts", "err", lost)
}

func (c *Client) refresh(ctx context.Context, server *Server, conn remoteClient) error {
	ctx, cancel := withServerTimeout(ctx, server.cfg.Timeout)
	defer cancel()

	list, err := conn.ListTools(ctx, mcpsdk.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("listing mcp tools for %q: %w", server.Name(), err)
	}
	resources, err := conn.ListResources(ctx, mcpsdk.ListResourcesRequest{})
	if err != nil {
		resources = &mcpsdk.ListResourcesResult{}
	}

	bridged := make([]tools.Tool, 0, len(list.Tools))
	for _, remoteTool := range list.Tools {
		schema := remoteTool.RawInputSchema
		if len(schema) == 0 {
			schema, err = json.Marshal(remoteTool.InputSchema)
			if err != nil {
				return fmt.Errorf("marshaling schema for %q: %w", remoteTool.Name, err)
			}
		}
		bridged = append(bridged, &toolAdapter{
			server:      server,
			perms:       c.perms,
			name:        joinedToolName(server.Name(), remoteTool.Name),
			remoteName:  remoteTool.Name,
			description: remoteTool.Description,
			schema:      append(json.RawMessage(nil), schema...),
		})
	}

	advertised := make([]Resource, 0, len(resources.Resources))
	for _, resource := range resources.Resources {
		advertised = append(advertised, Resource{
			Server:      server.Name(),
			URI:         resource.URI,
			Name:        resource.Name,
			Description: resource.Description,
			MimeType:    resource.MIMEType,
		})
	}

	server.mu.Lock()
	server.tools = bridged
	server.resources = advertised
	server.mu.Unlock()
	return nil
}

func (c *Client) publish(ctx context.Context, event Event) {
	if c.bus != nil {
		c.bus.Publish(ctx, event)
	}
}

func (c *Client) serverByName(name string) *Server {
	for _, server := range c.Servers() {
		if server.Name() == name {
			return server
		}
	}
	return nil
}

func connectMCP(ctx context.Context, cfg ServerConfig) (remoteClient, error) {
	if err := ValidateServerConfig(cfg); err != nil {
		return nil, err
	}

	ctx, cancel := withServerTimeout(ctx, cfg.Timeout)
	defer cancel()

	var cli *mcpclient.Client
	var err error
	// capturedCmd records the stdio child command so Stop can hard-kill it if a
	// graceful Close hangs; mcp-go exposes no accessor for its internal handle.
	var capturedCmd *exec.Cmd
	switch cfg.Transport {
	case TransportStdio:
		command := util.ExpandPath(cfg.Command[0])
		env := filteredEnv(cfg.Env)
		cmdFunc := func(cmdCtx context.Context, name string, cmdEnv []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(cmdCtx, name, args...)
			cmd.Env = append(os.Environ(), cmdEnv...)
			capturedCmd = cmd
			return cmd, nil
		}
		cli, err = mcpclient.NewStdioMCPClientWithOptions(
			command, env, cfg.Command[1:],
			mcptransport.WithCommandFunc(cmdFunc),
		)
	case TransportHTTP:
		cli, err = mcpclient.NewStreamableHttpClient(cfg.URL)
	case TransportSSE:
		cli, err = mcpclient.NewSSEMCPClient(cfg.URL)
	}
	if err != nil {
		return nil, fmt.Errorf("creating mcp client for %q: %w", cfg.Name, err)
	}
	if err := cli.Start(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("starting mcp client for %q: %w", cfg.Name, err)
	}
	if _, err := cli.Initialize(ctx, mcpsdk.InitializeRequest{
		Params: mcpsdk.InitializeParams{
			ProtocolVersion: mcpsdk.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcpsdk.Implementation{
				Name:    "bharatcode",
				Version: "0.1.0",
			},
			Capabilities: mcpsdk.ClientCapabilities{},
		},
	}); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("initializing mcp client for %q: %w", cfg.Name, err)
	}
	if cfg.Transport == TransportStdio && capturedCmd != nil && capturedCmd.Process != nil {
		return &stdioRemote{remoteClient: cli, proc: capturedCmd.Process}, nil
	}
	return cli, nil
}

func filteredEnv(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "BHARATCODE_") {
			continue
		}
		env = append(env, item)
	}
	for key, value := range overrides {
		if strings.HasPrefix(key, "BHARATCODE_") {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func withServerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	randMu.Lock()
	n := randSource.Float64()
	randMu.Unlock()
	factor := 1 - reconnectJitterPct + n*(2*reconnectJitterPct)
	return time.Duration(float64(base) * factor)
}

func joinedToolName(server, tool string) string {
	joined := server + "__" + tool
	if utf8.RuneCountInString(joined) <= maxToolNameRunes {
		return joined
	}

	sum := sha1.Sum([]byte(server + tool))
	suffix := hex.EncodeToString(sum[:])[:6]
	runes := []rune(joined)
	keep := maxToolNameRunes - len(suffix) - 1
	if keep < 1 {
		return suffix
	}
	left := keep / 2
	right := keep - left
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:]) + suffix
}

func toolNames(server *Server) []string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	names := make([]string, 0, len(server.tools))
	for _, tool := range server.tools {
		names = append(names, tool.Name())
	}
	sort.Strings(names)
	return names
}

func resourceBytes(content mcpsdk.ResourceContents) ([]byte, string, error) {
	switch v := content.(type) {
	case mcpsdk.TextResourceContents:
		return []byte(v.Text), v.MIMEType, nil
	case mcpsdk.BlobResourceContents:
		data, err := base64.StdEncoding.DecodeString(v.Blob)
		if err != nil {
			return nil, v.MIMEType, fmt.Errorf("decoding mcp resource blob: %w", err)
		}
		return data, v.MIMEType, nil
	default:
		return nil, "", fmt.Errorf("unsupported mcp resource content %T", content)
	}
}
