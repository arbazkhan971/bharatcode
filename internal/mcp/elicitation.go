package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ElicitationRequest is a server's request for structured input from the user,
// issued mid-tool-call via elicitation/create. It mirrors the MCP elicitation
// parameters in package-local types so callers (a UI handler) need not depend
// on the MCP SDK. Message is the human-readable prompt explaining what is being
// asked and why; Schema is the JSON Schema (as raw JSON) the user's response is
// expected to conform to, so the UI can render and validate the requested form.
type ElicitationRequest struct {
	Message string          `json:"message"`
	Schema  json.RawMessage `json:"schema,omitempty"`
}

// ElicitationAction is how the user responded to an elicitation request: they
// either accepted and supplied data, explicitly declined, or cancelled without
// choosing. It mirrors the MCP elicitation response action.
type ElicitationAction string

const (
	// ElicitationAccept means the user provided the requested information; the
	// response Content holds it.
	ElicitationAccept ElicitationAction = "accept"
	// ElicitationDecline means the user explicitly declined to provide the
	// information.
	ElicitationDecline ElicitationAction = "decline"
	// ElicitationCancel means the user cancelled without making a choice.
	ElicitationCancel ElicitationAction = "cancel"
)

// ElicitationResponse is the user's answer to an ElicitationRequest, produced by
// the injected handler and returned to the server. Action records whether the
// user accepted, declined, or cancelled; Content carries the structured data
// (conforming to the request's Schema) when the user accepted, and is nil
// otherwise.
type ElicitationResponse struct {
	Action  ElicitationAction `json:"action"`
	Content map[string]any    `json:"content,omitempty"`
}

// ElicitationHandler collects structured input from the user on behalf of a
// server that issued an elicitation/create request. It is injected by the
// caller (a UI) so the mcp package stays decoupled from any concrete prompting
// mechanism. It receives the server's prompt and schema and returns the user's
// response.
type ElicitationHandler func(ctx context.Context, req ElicitationRequest) (ElicitationResponse, error)

// elicitationHandler adapts an ElicitationHandler to the MCP SDK's
// client.ElicitationHandler interface. It is the client-side request-handling
// path for a server's elicitation/create request: it translates the SDK request
// into a package-local ElicitationRequest, invokes the handler, and translates
// the ElicitationResponse back into the SDK result the server receives. It is
// the elicitation counterpart to samplingHandler.
type elicitationHandler struct {
	elicit ElicitationHandler
}

var _ mcpclient.ElicitationHandler = (*elicitationHandler)(nil)

// Elicit handles a server's elicitation/create request by invoking the injected
// handler and returning the user's response to the server. The requested schema
// arrives as a decoded JSON value (the wire shape) and is re-marshaled to raw
// JSON so the handler sees the schema as the server sent it.
func (h *elicitationHandler) Elicit(ctx context.Context, request mcpsdk.ElicitationRequest) (*mcpsdk.ElicitationResult, error) {
	if h.elicit == nil {
		return nil, fmt.Errorf("mcp elicitation: no handler configured")
	}

	req := ElicitationRequest{Message: request.Params.Message}
	if request.Params.RequestedSchema != nil {
		schema, err := json.Marshal(request.Params.RequestedSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp elicitation: marshaling requested schema: %w", err)
		}
		req.Schema = schema
	}

	resp, err := h.elicit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp elicitation: %w", err)
	}

	action := resp.Action
	if action == "" {
		action = ElicitationCancel
	}
	result := &mcpsdk.ElicitationResult{
		ElicitationResponse: mcpsdk.ElicitationResponse{
			Action: mcpsdk.ElicitationResponseAction(action),
		},
	}
	// Content is only meaningful on accept; the SDK omits it otherwise.
	if action == ElicitationAccept && resp.Content != nil {
		result.Content = resp.Content
	}
	return result, nil
}

// elicitationReceiver is an optional interface a remote client may implement to
// receive the elicitation handler after connecting. The production
// *mcpclient.Client has its handler wired at construction via
// WithElicitationHandler and does not implement this; it exists so a conn (such
// as a test double) can capture the handler that fields a server's
// elicitation/create request and drive a request through it. It is the
// elicitation counterpart to samplingReceiver.
type elicitationReceiver interface {
	setElicitationHandler(mcpclient.ElicitationHandler)
}
