package mcp

import (
	"context"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// methodNotificationRootsListChanged is the JSON-RPC method the client sends to
// tell a server its advertised roots changed, prompting the server to re-issue
// roots/list. The mcp-go SDK at this version exposes no constant for it (it only
// defines the message type), so it is defined here.
const methodNotificationRootsListChanged = "notifications/roots/list_changed"

// Root is a filesystem location (typically a workspace directory) the client
// advertises to MCP servers so they can scope their operations to it. It mirrors
// the MCP roots/list entry in a package-local type so callers need not depend on
// the MCP SDK. URI identifies the root and must use the file:// scheme; Name is
// an optional human-readable label.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// rootsProvider supplies the roots a server's roots/list request is answered
// with. It is the indirection that lets the handler always return the client's
// current roots, even after SetRoots replaces them, without re-installing the
// handler on the conn.
type rootsProvider func() []Root

// rootsHandler adapts a rootsProvider to the client-side handling of a server's
// roots/list request: it translates the client's current roots into the SDK
// ListRootsResult the server receives. It is the roots counterpart to
// samplingHandler, and is captured by a conn that implements rootsReceiver so a
// server-issued roots/list can be driven through it.
type rootsHandler struct {
	provide rootsProvider
}

// ListRoots handles a server's roots/list request by returning the client's
// current roots. The request carries no parameters; the result is the roots the
// provider yields at call time, so a SetRoots that ran after the handler was
// installed is reflected here.
func (h *rootsHandler) ListRoots(_ context.Context, _ mcpsdk.ListRootsRequest) (*mcpsdk.ListRootsResult, error) {
	roots := h.provide()
	out := make([]mcpsdk.Root, 0, len(roots))
	for _, root := range roots {
		out = append(out, mcpsdk.Root{URI: root.URI, Name: root.Name})
	}
	return &mcpsdk.ListRootsResult{Roots: out}, nil
}

// rootsReceiver is an optional interface a remote client may implement to
// receive the roots handler and the roots-changed signal after connecting. The
// production *mcpclient.Client advertises the roots capability at construction
// but has no public hook to field a server's roots/list request, so it does not
// implement this; it exists so a conn (such as a test double) can capture the
// handler that answers roots/list and observe the client's
// notifications/roots/list_changed signal.
type rootsReceiver interface {
	setRootsHandler(*rootsHandler)
	// sendRootsListChanged delivers the client's roots-changed notification to
	// the server, prompting it to re-issue roots/list. The notification's method
	// is methodNotificationRootsListChanged.
	sendRootsListChanged(mcpsdk.RootsListChangedNotification)
}

// rootsListChangedNotification builds the notification the client sends a server
// when its advertised roots change.
func rootsListChangedNotification() mcpsdk.RootsListChangedNotification {
	return mcpsdk.RootsListChangedNotification{
		Notification: mcpsdk.Notification{Method: methodNotificationRootsListChanged},
	}
}

// rootsStore holds the client's advertised roots behind a mutex shared with the
// rootsProvider closure, so SetRoots and a concurrent roots/list handler see a
// consistent snapshot.
type rootsStore struct {
	mu    sync.RWMutex
	roots []Root
}

// set replaces the stored roots with a defensive copy.
func (s *rootsStore) set(roots []Root) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots = append([]Root(nil), roots...)
}

// snapshot returns a copy of the stored roots safe to hand to a caller.
func (s *rootsStore) snapshot() []Root {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Root(nil), s.roots...)
}
