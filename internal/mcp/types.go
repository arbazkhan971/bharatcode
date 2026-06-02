// Package mcp implements the Model Context Protocol client used by BharatCode.
package mcp

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Transport names the wire protocol used to reach an MCP server.
type Transport string

const (
	// TransportStdio connects to a subprocess over stdin and stdout.
	TransportStdio Transport = "stdio"
	// TransportHTTP connects to a streamable HTTP endpoint.
	TransportHTTP Transport = "http"
	// TransportSSE connects to a server-sent-events endpoint.
	TransportSSE Transport = "sse"
)

const serverNamePattern = `[a-z][a-z0-9_]{0,31}`

var serverNameRE = regexp.MustCompile(`^` + serverNamePattern + `$`)

// ServerConfig describes a single MCP server.
type ServerConfig struct {
	Name      string            `json:"name"`
	Transport Transport         `json:"transport"`
	Command   []string          `json:"command,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Timeout   time.Duration     `json:"timeout,omitempty"`

	// Sampler, when non-nil, handles server-requested LLM completions
	// (sampling/createMessage). It is injected at connect time rather than
	// loaded from config, so it is excluded from JSON.
	Sampler Sampler `json:"-"`

	// Elicit, when non-nil, handles server-requested structured user input
	// (elicitation/create). Like Sampler, it is injected at connect time rather
	// than loaded from config, so it is excluded from JSON.
	Elicit ElicitationHandler `json:"-"`

	// RootsEnabled, when true, advertises the roots capability (with
	// list-changed support) at init so the server may issue roots/list. Like
	// Sampler, it reflects client state injected at connect time rather than
	// loaded from config, so it is excluded from JSON.
	RootsEnabled bool `json:"-"`
}

// Resource is a server-advertised resource the agent may read by URI.
type Resource struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mime_type"`
}

// Prompt is a server-advertised prompt template the agent may render by name.
type Prompt struct {
	Server      string           `json:"server"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes one argument a prompt template accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// PromptMessage is one rendered message returned by GetPrompt.
type PromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResourceUpdate identifies a subscribed resource the server reported as
// changed via a notifications/resources/updated notification. The agent
// typically re-reads the resource by URI in response.
type ResourceUpdate struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

// methodNotificationProgress is the JSON-RPC method for out-of-band progress
// updates a server sends during a long-running request (a tool call here). The
// mcp-go SDK at this version exposes no constant for it, so it is defined here.
const methodNotificationProgress = "notifications/progress"

// ToolProgress reports a server's progress on an in-flight tool call, delivered
// via a notifications/progress notification. Token is the progress token the
// client attached to the originating CallTool request, so a UI can correlate an
// update with the specific call (multiple calls may be in flight at once).
// Progress increases over the life of the call; Total is the expected final
// value when the server knows it (zero when unknown), so a percentage is
// Progress/Total only when Total is positive. Message is an optional
// human-readable status.
type ToolProgress struct {
	Server   string  `json:"server"`
	Token    string  `json:"token"`
	Progress float64 `json:"progress"`
	Total    float64 `json:"total,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// State reports the connection state of a single MCP server.
type State int

const (
	// StateDisconnected means the server is not currently connected.
	StateDisconnected State = iota
	// StateConnecting means the client is dialing or re-dialing the server.
	StateConnecting
	// StateConnected means the server is connected and tools are available.
	StateConnected
	// StateFailed means reconnect attempts have been exhausted.
	StateFailed
)

// Event is published whenever a server state or tool list changes.
type Event struct {
	Server    string
	State     State
	Err       error
	ToolNames []string
}

// ErrToolUnavailable marks a tool whose backing MCP server is unusable.
var ErrToolUnavailable = errors.New("mcp tool unavailable")

// ValidateServerConfig checks one MCP server definition.
func ValidateServerConfig(cfg ServerConfig) error {
	if !serverNameRE.MatchString(cfg.Name) {
		return fmt.Errorf("mcp server %q has invalid name: must match %s", cfg.Name, serverNamePattern)
	}

	switch cfg.Transport {
	case TransportStdio:
		if len(cfg.Command) == 0 || cfg.Command[0] == "" {
			return fmt.Errorf("mcp server %q stdio transport requires command", cfg.Name)
		}
	case TransportHTTP, TransportSSE:
		if cfg.URL == "" {
			return fmt.Errorf("mcp server %q %s transport requires url", cfg.Name, cfg.Transport)
		}
	default:
		return fmt.Errorf("mcp server %q has invalid transport %q", cfg.Name, cfg.Transport)
	}

	return nil
}
