package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// imageTool stands in for the view tool reading an image file: it returns a
// text placeholder in Content and the real bytes in Metadata, exactly as the
// built-in view tool does for image paths.
type imageTool struct {
	name string
	data []byte
	mime string
	// err, when true, marks the result an error to prove error results never
	// forward their image.
	err bool
}

func (t *imageTool) Name() string            { return t.name }
func (t *imageTool) Description() string     { return "returns an image" }
func (t *imageTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *imageTool) Run(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{
		Content: "image file: pic." + "png",
		IsError: t.err,
		Metadata: map[string]any{
			tools.MetadataImage:    base64.StdEncoding.EncodeToString(t.data),
			tools.MetadataMimeType: t.mime,
		},
	}, nil
}

// imageBlocksOf returns every ImageBlock carried by msgs, in order.
func imageBlocksOf(msgs []message.Message) []message.ImageBlock {
	var out []message.ImageBlock
	for _, m := range msgs {
		for _, b := range m.Content {
			if img, ok := b.(message.ImageBlock); ok {
				out = append(out, img)
			}
		}
	}
	return out
}

func visionProvider(scripts [][]llm.Event, supportsImages bool) *scriptProvider {
	return &scriptProvider{
		scripts: scripts,
		models: []llm.Model{{
			ID:             "fake-model",
			Provider:       "fake",
			ContextWindow:  8192,
			SupportsTools:  true,
			SupportsImages: supportsImages,
		}},
	}
}

func imageToolScript() [][]llm.Event {
	return [][]llm.Event{
		{
			toolCall("call-1", "view", `{"path":"pic.png"}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "I can see it."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}
}

func runImageLoop(t *testing.T, provider *scriptProvider, tool tools.Tool) []message.Message {
	t.Helper()
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(tool)

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "test prompt",
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("look at pic.png")))

	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	return messages
}

func TestToolImageForwardedToVisionModel(t *testing.T) {
	want := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	tool := &imageTool{name: "view", data: want, mime: "image/png"}

	messages := runImageLoop(t, visionProvider(imageToolScript(), true), tool)

	imgs := imageBlocksOf(messages)
	require.Len(t, imgs, 1, "the viewed image must be forwarded once as an image block")
	require.Equal(t, "image/png", imgs[0].MimeType)
	require.Equal(t, want, imgs[0].Data, "the forwarded bytes must match what the tool read")

	// The image rides in a user message immediately after the tool result, and
	// that message must also explain where the image came from so the model has
	// context for the pixels.
	var imageMsgText string
	for _, m := range messages {
		for _, b := range m.Content {
			if _, ok := b.(message.ImageBlock); ok {
				imageMsgText = textOf(m)
				require.Equal(t, message.RoleUser, m.Role)
			}
		}
	}
	require.Contains(t, imageMsgText, "view")
}

func TestToolImageWithheldFromTextOnlyModel(t *testing.T) {
	tool := &imageTool{name: "view", data: []byte{0x01, 0x02, 0x03}, mime: "image/png"}

	messages := runImageLoop(t, visionProvider(imageToolScript(), false), tool)

	require.Empty(t, imageBlocksOf(messages),
		"a model without image support must not receive an image block")
}

func TestToolImageNotForwardedOnError(t *testing.T) {
	tool := &imageTool{name: "view", data: []byte{0x01, 0x02, 0x03}, mime: "image/png", err: true}

	messages := runImageLoop(t, visionProvider(imageToolScript(), true), tool)

	require.Empty(t, imageBlocksOf(messages),
		"an error result must not forward its image even on a vision model")
}

func TestMaybeAppendToolImageSkipsMalformedMetadata(t *testing.T) {
	provider := visionProvider(nil, true)
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    newFakeRegistry(),
		Sessions: testRepo(t),
	})

	cases := []struct {
		name string
		meta map[string]any
	}{
		{"nil metadata", nil},
		{"missing image key", map[string]any{"path": "pic.png"}},
		{"empty image string", map[string]any{tools.MetadataImage: ""}},
		{"non-string image", map[string]any{tools.MetadataImage: 42}},
		{"invalid base64", map[string]any{tools.MetadataImage: "not-base64!!!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var history []message.Message
			err := loop.maybeAppendToolImage(context.Background(), "sess",
				pendingToolCall{Name: "view"}, tools.Result{Metadata: tc.meta}, &history)
			require.NoError(t, err)
			require.Empty(t, history, "malformed metadata must append nothing")
		})
	}
}

func TestToolImageDefaultsMimeWhenMissing(t *testing.T) {
	tool := &imageTool{name: "view", data: []byte{0xff, 0xd8, 0xff}, mime: ""}

	messages := runImageLoop(t, visionProvider(imageToolScript(), true), tool)

	imgs := imageBlocksOf(messages)
	require.Len(t, imgs, 1)
	require.Equal(t, "image/png", imgs[0].MimeType, "a missing MIME type falls back to image/png")
}
