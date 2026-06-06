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
	Subscribe(context.Context, mcpsdk.SubscribeRequest) error
	Unsubscribe(context.Context, mcpsdk.UnsubscribeRequest) error
	ListPrompts(context.Context, mcpsdk.ListPromptsRequest) (*mcpsdk.ListPromptsResult, error)
	GetPrompt(context.Context, mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error)
	OnConnectionLost(func(error))
	OnNotification(func(mcpsdk.JSONRPCNotification))
}

// forceKiller is implemented by remote clients that own a child process and
// can hard-kill it when a graceful Close hangs past the stop deadline.
type forceKiller interface {
	// forceKill terminates the underlying child process. It reports whether a
	// process was actually killed.
	forceKill() bool
}

// samplingReceiver is an optional interface a remote client may implement to
// receive the sampling handler after connecting. The production *mcpclient.Client
// has its handler wired at construction via WithSamplingHandler and does not
// implement this; it exists so a conn (such as a test double) can capture and
// drive the handler that fields a server's sampling/createMessage request.
type samplingReceiver interface {
	setSamplingHandler(mcpclient.SamplingHandler)
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
	prompts   []Prompt
	// subscribed is the set of resource URIs the client has an active
	// subscription for on this server. Notification delivery is gated on
	// membership: an updated notification for a URI not in the set is dropped.
	// It is touched by Subscribe/Unsubscribe on the caller goroutine and by the
	// notification handler on the conn's read goroutine, so it is guarded by mu.
	subscribed map[string]struct{}
}

// addSubscription records uri as actively subscribed on this server.
func (s *Server) addSubscription(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribed == nil {
		s.subscribed = make(map[string]struct{})
	}
	s.subscribed[uri] = struct{}{}
}

// removeSubscription clears any active subscription for uri on this server.
func (s *Server) removeSubscription(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribed, uri)
}

// isSubscribed reports whether uri currently has an active subscription.
func (s *Server) isSubscribed(uri string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.subscribed[uri]
	return ok
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

// Counts returns the number of tools, resources, and prompts the server
// currently exposes. The values are zero until the server connects and
// advertises its capabilities, so they double as a quick liveness signal for
// the /mcp listing.
func (s *Server) Counts() (toolCount, resourceCount, promptCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tools), len(s.resources), len(s.prompts)
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
	cfg          *config.Config
	perms        *permission.Checker
	bus          *pubsub.Topic[Event]
	mu           sync.RWMutex
	servers      []*Server
	sampler      Sampler
	elicit       ElicitationHandler
	roots        rootsStore
	onResUpdated func(ResourceUpdate)
	onToolProg   func(ToolProgress)
}

// SetResourceUpdateHandler installs the callback invoked when a subscribed
// resource changes on a server. The handler receives the server name and URI
// of the updated resource. Passing nil disables delivery. It may be called at
// any time; updates for URIs without an active subscription are never
// delivered, even when a handler is set.
func (c *Client) SetResourceUpdateHandler(fn func(ResourceUpdate)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onResUpdated = fn
}

func (c *Client) resourceUpdateHandler() func(ResourceUpdate) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onResUpdated
}

// SetToolProgressHandler installs the callback invoked when a server reports
// progress on an in-flight tool call via a notifications/progress notification.
// The handler receives the originating server's name, the progress token the
// client attached to the CallTool request, and the reported progress so a UI
// can show advancement. Passing nil disables delivery. It may be called at any
// time; it is shared by every configured server.
func (c *Client) SetToolProgressHandler(fn func(ToolProgress)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onToolProg = fn
}

func (c *Client) toolProgressHandler() func(ToolProgress) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onToolProg
}

// SetSampler installs the callback that handles server-requested LLM
// completions (sampling/createMessage). It must be called before Start so the
// sampling capability is advertised when each server connects. Passing nil
// disables sampling. The sampler is shared by every configured server.
func (c *Client) SetSampler(fn Sampler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sampler = fn
}

func (c *Client) currentSampler() Sampler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sampler
}

// SetElicitationHandler installs the callback that collects structured input
// from the user when a server requests it mid-tool-call (elicitation/create).
// It must be called before Start so the elicitation capability is advertised
// when each server connects. Passing nil disables elicitation. The handler is
// shared by every configured server.
func (c *Client) SetElicitationHandler(fn ElicitationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.elicit = fn
}

func (c *Client) currentElicitationHandler() ElicitationHandler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.elicit
}

// SetRoots replaces the filesystem roots the client advertises to MCP servers
// (typically the session's workspace directories) and notifies every connected
// server that the list changed, prompting it to re-issue roots/list. It may be
// called at any time; the roots are shared by every configured server. The
// roots-changed notification reaches only conns connected at call time; a server
// that connects later receives the current roots when it issues roots/list. To
// have the roots capability advertised at init, call SetRoots before Start.
func (c *Client) SetRoots(roots []Root) {
	c.roots.set(roots)
	for _, server := range c.Servers() {
		_, conn := server.snapshot()
		if conn == nil {
			continue
		}
		if recv, ok := conn.(rootsReceiver); ok {
			recv.sendRootsListChanged(rootsListChangedNotification())
		}
	}
}

// currentRoots returns a snapshot of the advertised roots.
func (c *Client) currentRoots() []Root {
	return c.roots.snapshot()
}

// connectConfig returns the server's config with the client's current sampler
// and roots state injected, so the connector can advertise and handle both.
func (c *Client) connectConfig(server *Server) ServerConfig {
	cfg := server.cfg
	cfg.Sampler = c.currentSampler()
	cfg.Elicit = c.currentElicitationHandler()
	cfg.RootsEnabled = len(c.currentRoots()) > 0
	return cfg
}

// installSampler hands the sampling handler to a conn that can receive one. The
// production *mcpclient.Client has the handler wired at construction and does
// not implement samplingReceiver, so this is a no-op for it. A conn that does
// implement it (such as a test double) captures the handler and can drive a
// server's sampling/createMessage request through it.
func (c *Client) installSampler(conn remoteClient) {
	sampler := c.currentSampler()
	if sampler == nil {
		return
	}
	if recv, ok := conn.(samplingReceiver); ok {
		recv.setSamplingHandler(&samplingHandler{sample: sampler})
	}
}

// installElicitation hands the elicitation handler to a conn that can receive
// one. The production *mcpclient.Client has the handler wired at construction
// via WithElicitationHandler and does not implement elicitationReceiver, so this
// is a no-op for it. A conn that does implement it (such as a test double)
// captures the handler and can drive a server's elicitation/create request
// through it. It is the elicitation counterpart to installSampler.
func (c *Client) installElicitation(conn remoteClient) {
	handler := c.currentElicitationHandler()
	if handler == nil {
		return
	}
	if recv, ok := conn.(elicitationReceiver); ok {
		recv.setElicitationHandler(&elicitationHandler{elicit: handler})
	}
}

// installRoots hands a roots handler to a conn that can receive one, so a
// server's roots/list request is answered with the client's current roots. The
// handler reads the roots live via the client's store, so a later SetRoots is
// reflected without re-installing. The production *mcpclient.Client exposes no
// hook to field roots/list and does not implement rootsReceiver, so this is a
// no-op for it; a conn that does implement it (such as a test double) captures
// the handler and can drive a server's roots/list request through it.
func (c *Client) installRoots(conn remoteClient) {
	recv, ok := conn.(rootsReceiver)
	if !ok {
		return
	}
	recv.setRootsHandler(&rootsHandler{provide: c.currentRoots})
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

// Subscribe registers interest in change notifications for the resource named
// by uri. After it returns, a notifications/resources/updated notification from
// the owning server for this URI is delivered to the handler installed via
// SetResourceUpdateHandler. The server is resolved from the URI's "server://"
// prefix, mirroring ReadResource.
func (c *Client) Subscribe(ctx context.Context, uri string) error {
	server, conn, err := c.resourceServer("subscribing to", uri)
	if err != nil {
		return err
	}

	// Record the subscription before issuing the request so an update that
	// arrives the instant the server acknowledges is gated in, not dropped.
	server.addSubscription(uri)

	ctx, cancel := withServerTimeout(ctx, server.cfg.Timeout)
	defer cancel()

	if err := conn.Subscribe(ctx, mcpsdk.SubscribeRequest{
		Params: mcpsdk.SubscribeParams{URI: uri},
	}); err != nil {
		server.removeSubscription(uri)
		return fmt.Errorf("subscribing to mcp resource %q: %w", uri, err)
	}
	return nil
}

// Unsubscribe cancels a subscription previously registered with Subscribe.
// After it returns, updates for this URI are no longer delivered, even if the
// server keeps sending them: delivery is gated on the client-side subscription
// set, which this clears first.
func (c *Client) Unsubscribe(ctx context.Context, uri string) error {
	server, conn, err := c.resourceServer("unsubscribing from", uri)
	if err != nil {
		return err
	}

	// Stop delivery first: once removed from the set, any further updates the
	// server sends for this URI are dropped by the notification handler.
	server.removeSubscription(uri)

	ctx, cancel := withServerTimeout(ctx, server.cfg.Timeout)
	defer cancel()

	if err := conn.Unsubscribe(ctx, mcpsdk.UnsubscribeRequest{
		Params: mcpsdk.UnsubscribeParams{URI: uri},
	}); err != nil {
		return fmt.Errorf("unsubscribing from mcp resource %q: %w", uri, err)
	}
	return nil
}

// resourceServer resolves the connected server that owns the resource named by
// uri, using its "server://" prefix. action labels the operation for error
// messages (for example "subscribing to").
func (c *Client) resourceServer(action, uri string) (*Server, remoteClient, error) {
	serverName, _, ok := strings.Cut(uri, "://")
	if !ok || serverName == "" {
		return nil, nil, fmt.Errorf("%s mcp resource %q: missing server URI prefix", action, uri)
	}
	server := c.serverByName(serverName)
	if server == nil {
		return nil, nil, fmt.Errorf("%s mcp resource %q: unknown server %q", action, uri, serverName)
	}
	state, conn := server.snapshot()
	if state != StateConnected || conn == nil {
		return nil, nil, fmt.Errorf("%s mcp resource %q: %w", action, uri, ErrToolUnavailable)
	}
	return server, conn, nil
}

// Prompts returns a snapshot of prompt templates across every server.
func (c *Client) Prompts() []Prompt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []Prompt
	for _, server := range c.servers {
		server.mu.RLock()
		out = append(out, server.prompts...)
		server.mu.RUnlock()
	}
	return out
}

// GetPrompt renders a named prompt template on a server with the given
// arguments and returns its messages.
func (c *Client) GetPrompt(ctx context.Context, serverName, name string, args map[string]string) ([]PromptMessage, error) {
	server := c.serverByName(serverName)
	if server == nil {
		return nil, fmt.Errorf("getting mcp prompt %q: unknown server %q", name, serverName)
	}
	state, conn := server.snapshot()
	if state != StateConnected || conn == nil {
		return nil, fmt.Errorf("getting mcp prompt %q: %w", name, ErrToolUnavailable)
	}

	ctx, cancel := withServerTimeout(ctx, server.cfg.Timeout)
	defer cancel()

	result, err := conn.GetPrompt(ctx, mcpsdk.GetPromptRequest{
		Params: mcpsdk.GetPromptParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting mcp prompt %q: %w", name, err)
	}

	messages := make([]PromptMessage, 0, len(result.Messages))
	for _, msg := range result.Messages {
		messages = append(messages, PromptMessage{
			Role:    string(msg.Role),
			Content: contentText([]mcpsdk.Content{msg.Content}),
		})
	}
	return messages, nil
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

	conn, err := newRemote(ctx, c.connectConfig(server))
	if err != nil {
		server.logger.WarnContext(ctx, "MCP server connection failed", "err", err)
		c.publish(ctx, Event{Server: server.Name(), State: StateFailed, Err: err})
		server.setState(StateFailed, nil)
		return
	}

	c.installSampler(conn)
	c.installElicitation(conn)
	c.installRoots(conn)
	conn.OnConnectionLost(func(err error) {
		c.handleDisconnect(server, err)
	})
	c.installNotificationHandler(server, conn)
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
		conn, err := newRemote(ctx, c.connectConfig(server))
		if err == nil {
			c.installSampler(conn)
			c.installElicitation(conn)
			c.installRoots(conn)
			conn.OnConnectionLost(func(err error) {
				c.handleDisconnect(server, err)
			})
			c.installNotificationHandler(server, conn)
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

// installNotificationHandler routes the conn's server-pushed notifications to
// the client. It handles notifications/resources/updated by delivering the
// updated URI to the resource-update handler (but only when the client holds an
// active subscription for that URI; updates for unsubscribed URIs are dropped),
// and notifications/progress by delivering the reported progress to the
// tool-progress handler. Other notification methods are ignored. The handler
// runs on the conn's read goroutine, so it touches the subscription set under
// the server lock.
func (c *Client) installNotificationHandler(server *Server, conn remoteClient) {
	conn.OnNotification(func(notification mcpsdk.JSONRPCNotification) {
		switch notification.Method {
		case mcpsdk.MethodNotificationResourceUpdated:
			c.handleResourceUpdated(server, notification)
		case methodNotificationProgress:
			c.handleToolProgress(server, notification)
		}
	})
}

// handleResourceUpdated delivers a notifications/resources/updated notification
// to the resource-update handler, gated on an active client-side subscription
// so an Unsubscribe stops delivery even if the server keeps sending updates.
func (c *Client) handleResourceUpdated(server *Server, notification mcpsdk.JSONRPCNotification) {
	uri, ok := notification.Params.AdditionalFields["uri"].(string)
	if !ok || uri == "" {
		return
	}
	if !server.isSubscribed(uri) {
		return
	}
	handler := c.resourceUpdateHandler()
	if handler == nil {
		return
	}
	handler(ResourceUpdate{Server: server.Name(), URI: uri})
}

// handleToolProgress delivers a notifications/progress notification to the
// tool-progress handler. The progress token the client attached to the
// originating CallTool request rides in the notification's params, letting the
// handler correlate the update with the specific in-flight call. A progress
// update is dropped when it carries no token or no handler is installed; the
// numeric fields arrive as float64 off the wire, and an absent total stays zero.
func (c *Client) handleToolProgress(server *Server, notification mcpsdk.JSONRPCNotification) {
	fields := notification.Params.AdditionalFields
	token, ok := fields["progressToken"].(string)
	if !ok || token == "" {
		return
	}
	handler := c.toolProgressHandler()
	if handler == nil {
		return
	}
	progress, _ := fields["progress"].(float64)
	total, _ := fields["total"].(float64)
	message, _ := fields["message"].(string)
	handler(ToolProgress{
		Server:   server.Name(),
		Token:    token,
		Progress: progress,
		Total:    total,
		Message:  message,
	})
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
	// Prompts are an optional server capability; a server without them returns
	// "method not found", so a list failure is treated as "no prompts" rather
	// than a connect failure (mirroring resources, not tools).
	prompts, err := conn.ListPrompts(ctx, mcpsdk.ListPromptsRequest{})
	if err != nil {
		prompts = &mcpsdk.ListPromptsResult{}
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

	templates := make([]Prompt, 0, len(prompts.Prompts))
	for _, prompt := range prompts.Prompts {
		args := make([]PromptArgument, 0, len(prompt.Arguments))
		for _, arg := range prompt.Arguments {
			args = append(args, PromptArgument{
				Name:        arg.Name,
				Description: arg.Description,
				Required:    arg.Required,
			})
		}
		templates = append(templates, Prompt{
			Server:      server.Name(),
			Name:        prompt.Name,
			Description: prompt.Description,
			Arguments:   args,
		})
	}

	server.mu.Lock()
	server.tools = bridged
	server.resources = advertised
	server.prompts = templates
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

	// clientOpts advertise the sampling capability and route a server's
	// sampling/createMessage request to the injected sampler. They are empty when
	// no sampler is configured, leaving the connection behavior unchanged.
	var clientOpts []mcpclient.ClientOption
	if cfg.Sampler != nil {
		clientOpts = append(clientOpts, mcpclient.WithSamplingHandler(&samplingHandler{sample: cfg.Sampler}))
	}
	// Likewise advertise elicitation and route a server's elicitation/create
	// request to the injected handler when one is configured.
	if cfg.Elicit != nil {
		clientOpts = append(clientOpts, mcpclient.WithElicitationHandler(&elicitationHandler{elicit: cfg.Elicit}))
	}

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
		// The stdio transport is built explicitly so the sampling handler can be
		// attached as a client option; the convenience constructor exposes no
		// hook for it. mcpclient.Start skips starting an already-running stdio
		// transport, so it must be started here (this also spawns the child via
		// the captured command factory).
		stdio := mcptransport.NewStdioWithOptions(
			command, env, cfg.Command[1:],
			mcptransport.WithCommandFunc(cmdFunc),
		)
		if err = stdio.Start(ctx); err != nil {
			break
		}
		cli = mcpclient.NewClient(stdio, clientOpts...)
	case TransportHTTP:
		var httpTransport *mcptransport.StreamableHTTP
		httpTransport, err = mcptransport.NewStreamableHTTP(cfg.URL)
		if err == nil {
			cli = mcpclient.NewClient(httpTransport, clientOpts...)
		}
	case TransportSSE:
		var sse *mcptransport.SSE
		sse, err = mcptransport.NewSSE(cfg.URL)
		if err == nil {
			cli = mcpclient.NewClient(sse, clientOpts...)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("creating mcp client for %q: %w", cfg.Name, err)
	}
	if cli == nil {
		return nil, fmt.Errorf("creating mcp client for %q: unsupported transport %q", cfg.Name, cfg.Transport)
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
			Capabilities: clientCapabilities(cfg),
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

// clientCapabilities builds the capabilities the client advertises at init.
// The roots capability (with list-changed support) is declared when the config
// enables it, telling the server it may issue roots/list and expect a
// notifications/roots/list_changed when the roots change. The sampling
// capability is advertised separately by the SDK client when a sampling handler
// is wired at construction, so it is not set here.
func clientCapabilities(cfg ServerConfig) mcpsdk.ClientCapabilities {
	var caps mcpsdk.ClientCapabilities
	if cfg.RootsEnabled {
		caps.Roots = &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true}
	}
	return caps
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
