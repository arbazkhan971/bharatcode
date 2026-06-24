package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// fakePromptProvider is a stand-in for *Client implementing promptProvider.
type fakePromptProvider struct {
	prompts   []Prompt
	messages  []PromptMessage
	err       error
	gotServer string
	gotName   string
	gotArgs   map[string]string
	callCount int
}

func (f *fakePromptProvider) Prompts() []Prompt { return f.prompts }

func (f *fakePromptProvider) GetPrompt(_ context.Context, server, name string, args map[string]string) ([]PromptMessage, error) {
	f.callCount++
	f.gotServer = server
	f.gotName = name
	f.gotArgs = args
	return f.messages, f.err
}

func runPromptsTool(t *testing.T, p promptProvider, args string) (string, bool) {
	t.Helper()
	tool := newPromptsTool(p)
	res, err := tool.Run(context.Background(), json.RawMessage(args))
	require.NoError(t, err)
	return res.Content, res.IsError
}

func TestPromptsTool_NameAndSchema(t *testing.T) {
	tool := newPromptsTool(&fakePromptProvider{})
	require.Equal(t, promptsToolName, tool.Name())
	require.NotEmpty(t, tool.Description())

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tool.Schema(), &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "name")
	require.Contains(t, props, "server")
	require.Contains(t, props, "arguments")
}

func TestPromptsTool_ListSortsByNameAndShowsArgs(t *testing.T) {
	p := &fakePromptProvider{prompts: []Prompt{
		{Server: "review", Name: "checklist", Description: "review steps", Arguments: []PromptArgument{
			{Name: "lang", Required: true},
			{Name: "depth"},
		}},
		{Server: "docs", Name: "api", Description: "doc scaffold"},
	}}
	content, isErr := runPromptsTool(t, p, ``)
	require.False(t, isErr)

	lines := strings.Split(content, "\n")
	require.Len(t, lines, 3) // header + two prompts
	require.Contains(t, lines[0], "2 MCP prompt(s) available")
	// "api" sorts before "checklist".
	require.True(t, strings.Index(content, "api") < strings.Index(content, "checklist"))
	// Required args carry a trailing "*", optional ones do not.
	require.Contains(t, content, "(args: lang*, depth)")
	require.Contains(t, content, "api — doc scaffold")
}

func TestPromptsTool_ListEmpty(t *testing.T) {
	content, isErr := runPromptsTool(t, &fakePromptProvider{}, `{}`)
	require.False(t, isErr)
	require.Contains(t, content, "No MCP prompts")
}

func TestPromptsTool_RenderReturnsLineNumberedMessages(t *testing.T) {
	p := &fakePromptProvider{
		prompts:  []Prompt{{Server: "review", Name: "checklist"}},
		messages: []PromptMessage{{Role: "user", Content: "first\nsecond"}},
	}
	content, isErr := runPromptsTool(t, p, `{"name":"checklist"}`)
	require.False(t, isErr)
	require.Equal(t, "review", p.gotServer)
	require.Equal(t, "checklist", p.gotName)
	require.Contains(t, content, `prompt "checklist" on review (1 message(s))`)
	require.Contains(t, content, "[user]")
	require.Contains(t, content, "1 | first")
	require.Contains(t, content, "2 | second")
}

func TestPromptsTool_RenderPassesArguments(t *testing.T) {
	p := &fakePromptProvider{
		prompts:  []Prompt{{Server: "review", Name: "checklist", Arguments: []PromptArgument{{Name: "lang", Required: true}}}},
		messages: []PromptMessage{{Role: "assistant", Content: "ok"}},
	}
	content, isErr := runPromptsTool(t, p, `{"name":"checklist","arguments":{"lang":"go"}}`)
	require.False(t, isErr)
	require.Equal(t, map[string]string{"lang": "go"}, p.gotArgs)
	require.Contains(t, content, "[assistant]")
}

func TestPromptsTool_MissingRequiredArgIsRejectedBeforeServerCall(t *testing.T) {
	p := &fakePromptProvider{
		prompts: []Prompt{{Server: "review", Name: "checklist", Arguments: []PromptArgument{
			{Name: "lang", Required: true},
			{Name: "scope", Required: true},
		}}},
	}
	content, isErr := runPromptsTool(t, p, `{"name":"checklist","arguments":{"lang":"go"}}`)
	require.True(t, isErr)
	require.Contains(t, content, "missing required argument(s): scope")
	require.Zero(t, p.callCount, "server must not be called when a required arg is missing")
}

func TestPromptsTool_AmbiguousNameRequiresServer(t *testing.T) {
	p := &fakePromptProvider{prompts: []Prompt{
		{Server: "alpha", Name: "review"},
		{Server: "beta", Name: "review"},
	}}
	content, isErr := runPromptsTool(t, p, `{"name":"review"}`)
	require.True(t, isErr)
	require.Contains(t, content, "ambiguous across servers")
	require.Contains(t, content, "alpha")
	require.Contains(t, content, "beta")
	require.Zero(t, p.callCount)
}

func TestPromptsTool_ServerDisambiguatesSharedName(t *testing.T) {
	p := &fakePromptProvider{
		prompts: []Prompt{
			{Server: "alpha", Name: "review"},
			{Server: "beta", Name: "review"},
		},
		messages: []PromptMessage{{Role: "user", Content: "hi"}},
	}
	content, isErr := runPromptsTool(t, p, `{"name":"review","server":"beta"}`)
	require.False(t, isErr)
	require.Equal(t, "beta", p.gotServer)
	require.Contains(t, content, `on beta`)
}

func TestPromptsTool_UnknownNameIsReported(t *testing.T) {
	p := &fakePromptProvider{prompts: []Prompt{{Server: "review", Name: "checklist"}}}
	content, isErr := runPromptsTool(t, p, `{"name":"nope"}`)
	require.True(t, isErr)
	require.Contains(t, content, "unknown mcp prompt")
	require.Zero(t, p.callCount)
}

func TestPromptsTool_UnlistedNameWithServerIsAttempted(t *testing.T) {
	// A name not in the advertised list but with an explicit server still tries
	// the render — the server is the source of truth.
	p := &fakePromptProvider{
		messages: []PromptMessage{{Role: "user", Content: "x"}},
	}
	content, isErr := runPromptsTool(t, p, `{"name":"dynamic","server":"live"}`)
	require.False(t, isErr)
	require.Equal(t, 1, p.callCount)
	require.Equal(t, "live", p.gotServer)
	require.Contains(t, content, `on live`)
}

func TestPromptsTool_RenderErrorIsReported(t *testing.T) {
	p := &fakePromptProvider{
		prompts: []Prompt{{Server: "review", Name: "checklist"}},
		err:     errors.New("server exploded"),
	}
	content, isErr := runPromptsTool(t, p, `{"name":"checklist"}`)
	require.True(t, isErr)
	require.Contains(t, content, "server exploded")
}

func TestPromptsTool_EmptyMessages(t *testing.T) {
	p := &fakePromptProvider{prompts: []Prompt{{Server: "review", Name: "checklist"}}}
	content, isErr := runPromptsTool(t, p, `{"name":"checklist"}`)
	require.False(t, isErr)
	require.Contains(t, content, "rendered no messages")
}

func TestPromptsTool_InvalidArgs(t *testing.T) {
	content, isErr := runPromptsTool(t, &fakePromptProvider{}, `{"name":`)
	require.True(t, isErr)
	require.Contains(t, content, "invalid mcp_prompts arguments")
}

func TestPromptsTool_TruncatesAtUTF8Boundary(t *testing.T) {
	p := &fakePromptProvider{
		prompts: []Prompt{{Server: "review", Name: "big"}},
		messages: []PromptMessage{{
			Role:    "user",
			Content: strings.Repeat("a", maxPromptBytes) + "🙂",
		}},
	}

	content, isErr := runPromptsTool(t, p, `{"name":"big"}`)

	require.False(t, isErr)
	require.True(t, utf8.ValidString(content))
	require.Contains(t, content, "truncated at")
}

func TestPromptsTool_NilProvider(t *testing.T) {
	content, isErr := runPromptsTool(t, nil, `{}`)
	require.True(t, isErr)
	require.Contains(t, content, "no MCP client configured")
}

// TestPromptsToolFor confirms the *Client satisfies promptProvider and the
// nil-safe accessor returns a usable tool in both the present and absent cases.
func TestPromptsToolFor(t *testing.T) {
	require.Equal(t, promptsToolName, PromptsToolFor(&Client{}).Name())

	tool := PromptsToolFor(nil)
	require.Equal(t, promptsToolName, tool.Name())
	res, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "no MCP client configured")
}
