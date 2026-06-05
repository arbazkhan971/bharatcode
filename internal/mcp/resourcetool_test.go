package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeResourceProvider is a stand-in for *Client implementing resourceProvider.
type fakeResourceProvider struct {
	resources []Resource
	data      []byte
	mimeType  string
	err       error
	gotURI    string
}

func (f *fakeResourceProvider) Resources() []Resource { return f.resources }

func (f *fakeResourceProvider) ReadResource(_ context.Context, uri string) ([]byte, string, error) {
	f.gotURI = uri
	return f.data, f.mimeType, f.err
}

func runResourcesTool(t *testing.T, p resourceProvider, args string) (string, bool) {
	t.Helper()
	tool := newResourcesTool(p)
	res, err := tool.Run(context.Background(), json.RawMessage(args))
	require.NoError(t, err)
	return res.Content, res.IsError
}

func TestResourcesTool_NameAndSchema(t *testing.T) {
	tool := newResourcesTool(&fakeResourceProvider{})
	require.Equal(t, resourcesToolName, tool.Name())
	require.NotEmpty(t, tool.Description())

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tool.Schema(), &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "uri")
}

func TestResourcesTool_ListSortsByURI(t *testing.T) {
	p := &fakeResourceProvider{resources: []Resource{
		{Server: "docs", URI: "docs://b", Name: "Beta", Description: "second", MimeType: "text/plain"},
		{Server: "docs", URI: "docs://a", Name: "Alpha"},
	}}
	content, isErr := runResourcesTool(t, p, ``)
	require.False(t, isErr)

	lines := strings.Split(content, "\n")
	require.Len(t, lines, 3) // header + two resources
	require.Contains(t, lines[0], "2 MCP resource(s) available")
	// docs://a sorts before docs://b.
	require.True(t, strings.Index(content, "docs://a") < strings.Index(content, "docs://b"))
	require.Contains(t, content, "Beta — second [text/plain]")
}

func TestResourcesTool_ListEmpty(t *testing.T) {
	content, isErr := runResourcesTool(t, &fakeResourceProvider{}, `{}`)
	require.False(t, isErr)
	require.Contains(t, content, "No MCP resources")
}

func TestResourcesTool_ReadTextIsLineNumbered(t *testing.T) {
	p := &fakeResourceProvider{data: []byte("first\nsecond\nthird"), mimeType: "text/markdown"}
	content, isErr := runResourcesTool(t, p, `{"uri":"docs://guide"}`)
	require.False(t, isErr)
	require.Equal(t, "docs://guide", p.gotURI)
	require.Contains(t, content, "docs://guide [text/markdown]")
	require.Contains(t, content, "1 | first")
	require.Contains(t, content, "2 | second")
	require.Contains(t, content, "3 | third")
}

func TestResourcesTool_ReadTruncatesLargeText(t *testing.T) {
	big := strings.Repeat("x", maxResourceBytes+500)
	p := &fakeResourceProvider{data: []byte(big), mimeType: "text/plain"}
	content, isErr := runResourcesTool(t, p, `{"uri":"docs://big"}`)
	require.False(t, isErr)
	require.Contains(t, content, "truncated at")
	require.Less(t, len(content), len(big)+200)
}

func TestResourcesTool_ReadBinaryIsSummarized(t *testing.T) {
	// Invalid UTF-8 bytes stand in for a binary payload.
	p := &fakeResourceProvider{data: []byte{0xff, 0xfe, 0x00, 0x01}, mimeType: "image/png"}
	content, isErr := runResourcesTool(t, p, `{"uri":"docs://logo"}`)
	require.False(t, isErr)
	require.Contains(t, content, "binary resource image/png")
	require.Contains(t, content, "4 bytes")
}

func TestResourcesTool_ReadEmpty(t *testing.T) {
	p := &fakeResourceProvider{data: nil}
	content, isErr := runResourcesTool(t, p, `{"uri":"docs://empty"}`)
	require.False(t, isErr)
	require.Contains(t, content, "is empty")
}

func TestResourcesTool_ReadErrorIsReported(t *testing.T) {
	p := &fakeResourceProvider{err: errors.New("unknown server \"nope\"")}
	content, isErr := runResourcesTool(t, p, `{"uri":"nope://x"}`)
	require.True(t, isErr)
	require.Contains(t, content, "unknown server")
}

func TestResourcesTool_InvalidArgs(t *testing.T) {
	content, isErr := runResourcesTool(t, &fakeResourceProvider{}, `{"uri":`)
	require.True(t, isErr)
	require.Contains(t, content, "invalid mcp_resources arguments")
}

func TestResourcesTool_NilProvider(t *testing.T) {
	content, isErr := runResourcesTool(t, nil, `{}`)
	require.True(t, isErr)
	require.Contains(t, content, "no MCP client configured")
}

// TestResourcesToolFor confirms the *Client satisfies resourceProvider and the
// nil-safe accessor returns a usable tool in both the present and absent cases.
func TestResourcesToolFor(t *testing.T) {
	require.Equal(t, resourcesToolName, ResourcesToolFor(&Client{}).Name())

	// With no client the tool is still constructed and reports unavailability
	// rather than panicking, so it can be registered unconditionally.
	tool := ResourcesToolFor(nil)
	require.Equal(t, resourcesToolName, tool.Name())
	res, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "no MCP client configured")
}
