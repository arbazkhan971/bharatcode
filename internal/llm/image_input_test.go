package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// pngImageBytes is a tiny non-empty payload standing in for image data. Its
// exact contents are irrelevant; the tests assert it round-trips to base64 in
// each provider's wire format.
var pngImageBytes = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03}

// visionConfig builds a single-provider config whose model advertises image
// support, so the capability guard passes and the image is converted.
func visionConfig(providerName string, typ config.ProviderType, baseURL string) *config.Config {
	cfg := testConfig(providerName, typ, baseURL)
	cfg.Models[0].SupportsImages = true
	return cfg
}

func imageMessage() message.Message {
	return message.Message{
		Role: message.RoleUser,
		Content: []message.ContentBlock{
			message.TextBlock{Text: "describe this"},
			message.ImageBlock{MimeType: "image/png", Data: pngImageBytes},
		},
	}
}

// TestAnthropicConvertsImageBlockToBase64Source asserts a vision model receives
// the image as Anthropic's base64 image source block. Against the old rejection
// code the convert step returns ErrUnsupportedFeature, so Stream errors and the
// server is never hit, failing the require.NoError below.
func TestAnthropicConvertsImageBlockToBase64Source(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	cfg := visionConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody, "server must have received the request")

	var captured anthropicRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Len(t, captured.Messages, 1)

	// Find the image content block and assert its exact wire format.
	var image *anthropicContentBlock
	for i := range captured.Messages[0].Content {
		if captured.Messages[0].Content[i].Type == "image" {
			image = &captured.Messages[0].Content[i]
		}
	}
	require.NotNil(t, image, "request must carry an image content block")
	require.NotNil(t, image.Source)
	require.Equal(t, "base64", image.Source.Type)
	require.Equal(t, "image/png", image.Source.MediaType)
	require.Equal(t, base64.StdEncoding.EncodeToString(pngImageBytes), image.Source.Data)
}

// TestAnthropicRejectsImageForNonVisionModel asserts the capability guard still
// fires: a model without image support plus an image yields ErrUnsupportedFeature
// before any network call.
func TestAnthropicRejectsImageForNonVisionModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("non-vision model must not reach the provider")
	}))
	defer server.Close()

	// testConfig leaves SupportsImages false.
	cfg := testConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}

// TestOpenAICompatibleConvertsImageBlockToDataURL asserts a vision model receives
// the image as an OpenAI image_url content part carrying an inline data URL.
func TestOpenAICompatibleConvertsImageBlockToDataURL(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer server.Close()

	cfg := visionConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody, "server must have received the request")
	body := string(rawBody)

	// Exact wire format: an image_url part with a base64 data URL.
	wantURL := fmt.Sprintf("data:image/png;base64,%s", base64.StdEncoding.EncodeToString(pngImageBytes))
	require.Contains(t, body, `"type":"image_url"`)
	require.Contains(t, body, `"image_url"`)
	require.Contains(t, body, wantURL)

	// The content must be a multimodal array, not a plain string, and must not
	// carry the Ollama-style top-level images[] field.
	var probe struct {
		Messages []struct {
			Content []openAIContentPart `json:"content"`
			Images  []string            `json:"images"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(rawBody, &probe))
	require.Len(t, probe.Messages, 1)
	require.Empty(t, probe.Messages[0].Images, "OpenAI must not use top-level images[]")

	var imagePart *openAIContentPart
	for i := range probe.Messages[0].Content {
		if probe.Messages[0].Content[i].Type == "image_url" {
			imagePart = &probe.Messages[0].Content[i]
		}
	}
	require.NotNil(t, imagePart, "content array must hold an image_url part")
	require.NotNil(t, imagePart.ImageURL)
	require.Equal(t, wantURL, imagePart.ImageURL.URL)
}

// TestOpenAICompatibleRejectsImageForNonVisionModel asserts the guard fires for
// a non-vision OpenAI-compatible model.
func TestOpenAICompatibleRejectsImageForNonVisionModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("non-vision model must not reach the provider")
	}))
	defer server.Close()

	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}

// TestOllamaConvertsImageBlockToImagesArray asserts a vision model receives the
// image as a bare base64 string on the message's top-level images[] array, with
// no data: prefix and no OpenAI-style image_url content part.
func TestOllamaConvertsImageBlockToImagesArray(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		fmt.Fprintln(w, `{"done":true,"prompt_eval_count":1,"eval_count":1}`)
	}))
	defer server.Close()

	cfg := visionConfig("ollama", config.ProviderOllama, server.URL)
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("ollama")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody, "server must have received the request")
	body := string(rawBody)

	want := base64.StdEncoding.EncodeToString(pngImageBytes)
	var captured ollamaRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Len(t, captured.Messages, 1)
	require.Equal(t, []string{want}, captured.Messages[0].Images)

	// Bare base64 only: no data: URL prefix and no image_url content part.
	require.NotContains(t, body, "data:image/png;base64,")
	require.NotContains(t, body, "image_url")
}

// TestOllamaRejectsImageForNonVisionModel asserts the guard fires for a
// non-vision Ollama model.
func TestOllamaRejectsImageForNonVisionModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("non-vision model must not reach the provider")
	}))
	defer server.Close()

	cfg := testConfig("ollama", config.ProviderOllama, server.URL)
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("ollama")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}
