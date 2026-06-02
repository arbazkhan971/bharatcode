package message

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactText_RedactsSecrets(t *testing.T) {
	t.Parallel()

	pem := "-----BEGIN RSA PRIVATE KEY-----\n" +
		"MIIEowIBAAKCAQEA1234567890abcdef\n" +
		"-----END RSA PRIVATE KEY-----"

	cases := []struct {
		name string
		in   string
		// leak is a substring of the secret that must NOT survive redaction.
		leak string
	}{
		{
			name: "openai key",
			in:   "here is my key sk-abcDEF1234567890ghIJKLmnop please rotate it",
			leak: "sk-abcDEF1234567890ghIJKLmnop",
		},
		{
			name: "aws access key id",
			in:   "aws_access_key_id is AKIAIOSFODNN7EXAMPLE in the file",
			leak: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name: "bearer token",
			in:   "curl -H 'Authorization: Bearer xyzABC123tokenvalue' https://api",
			leak: "xyzABC123tokenvalue",
		},
		{
			name: "api key assignment",
			in:   "API_KEY=sup3r-s3cret-value-here\nnext line",
			leak: "sup3r-s3cret-value-here",
		},
		{
			name: "lowercase secret assignment with colon",
			in:   "client_secret: hunter2hunter2hunter2",
			leak: "hunter2hunter2hunter2",
		},
		{
			name: "prefixed key assignment",
			in:   "AWS_SECRET=abcdefgABCDEFG12345 exported",
			leak: "abcdefgABCDEFG12345",
		},
		{
			name: "github token",
			in:   "token ghp_ABCdef1234567890ABCdef1234567890abcd committed by mistake",
			leak: "ghp_ABCdef1234567890ABCdef1234567890abcd",
		},
		{
			name: "pem private key block",
			in:   "config:\n" + pem + "\nend",
			leak: "MIIEowIBAAKCAQEA1234567890abcdef",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RedactText(tc.in)
			assert.NotContains(t, got, tc.leak, "secret material survived redaction")
			assert.Contains(t, got, RedactPlaceholder, "expected placeholder in output")
		})
	}
}

func TestRedactText_PreservesOrdinaryContent(t *testing.T) {
	t.Parallel()

	// None of these match a known secret shape and must be returned verbatim,
	// guarding against over-greedy patterns (especially KEY=value).
	cases := []string{
		"The quick brown fox jumps over the lazy dog.",
		"MAX_RETRIES=5",
		"count = 5",
		"PATH=/usr/local/bin:/usr/bin",
		"TOKEN_BUDGET_LABEL=monthly",
		"HTTP_TIMEOUT=30",
		"MAX_TOKENS=4096",
		"for i := 0; i < n; i++ { total += i }",
		"commit sha is deadbeefcafebabe1234567890abcdef",
		"see https://example.com/docs for details",
		"sk-",
		"He gave a stark warning about the risk.",
	}

	for _, in := range cases {
		got := RedactText(in)
		assert.Equal(t, in, got, "ordinary content was altered: %q", in)
		assert.NotContains(t, got, RedactPlaceholder)
	}
}

func TestRedactText_RedactsPEMBody(t *testing.T) {
	t.Parallel()

	pem := "-----BEGIN PRIVATE KEY-----\n" +
		"SECRETKEYMATERIALLINE1\nSECRETKEYMATERIALLINE2\n" +
		"-----END PRIVATE KEY-----"

	got := RedactText(pem)
	assert.NotContains(t, got, "BEGIN PRIVATE KEY", "PEM header survived")
	assert.NotContains(t, got, "SECRETKEYMATERIALLINE1", "PEM body survived")
	assert.NotContains(t, got, "SECRETKEYMATERIALLINE2", "PEM body survived")
	assert.Equal(t, RedactPlaceholder, got)
}

func TestMessageRedacted_ScrubsTextualBlocks(t *testing.T) {
	t.Parallel()

	orig := Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      RoleAssistant,
		Content: []ContentBlock{
			TextBlock{Text: "use API_KEY=topsecretvalue123 to authenticate"},
			ThinkingBlock{Text: "I should not echo sk-abcDEF1234567890ghIJKLmnop"},
			ToolResultBlock{
				ToolUseID: "t1",
				Content:   "env dump: AKIAIOSFODNN7EXAMPLE and AWS_SECRET=abcdefgABCDEFG12345",
			},
			ToolUseBlock{ID: "t1", Name: "shell", Input: []byte(`{"cmd":"env"}`)},
			ImageBlock{MimeType: "image/png", Data: []byte{0x89, 0x50}},
		},
	}

	got := orig.Redacted()

	// Textual blocks are scrubbed.
	tb, ok := got.Content[0].(TextBlock)
	require.True(t, ok)
	assert.NotContains(t, tb.Text, "topsecretvalue123")
	assert.Contains(t, tb.Text, RedactPlaceholder)

	th, ok := got.Content[1].(ThinkingBlock)
	require.True(t, ok)
	assert.NotContains(t, th.Text, "sk-abcDEF1234567890ghIJKLmnop")

	tr, ok := got.Content[2].(ToolResultBlock)
	require.True(t, ok)
	assert.NotContains(t, tr.Content, "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, tr.Content, "abcdefgABCDEFG12345")
	assert.Equal(t, "t1", tr.ToolUseID, "non-text fields preserved")

	// ToolUseBlock.Input is left untouched (raw JSON not rewritten).
	tu, ok := got.Content[3].(ToolUseBlock)
	require.True(t, ok)
	assert.Equal(t, `{"cmd":"env"}`, string(tu.Input))

	// ImageBlock passed through unchanged.
	img, ok := got.Content[4].(ImageBlock)
	require.True(t, ok)
	assert.Equal(t, []byte{0x89, 0x50}, img.Data)
}

func TestMessageRedacted_DoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	const secret = "use API_KEY=topsecretvalue123 now"
	orig := Message{
		ID:   "m1",
		Role: RoleUser,
		Content: []ContentBlock{
			TextBlock{Text: secret},
		},
	}

	_ = orig.Redacted()

	tb, ok := orig.Content[0].(TextBlock)
	require.True(t, ok)
	assert.Equal(t, secret, tb.Text, "Redacted must not mutate the receiver")
}

func TestMessageRedacted_NilContent(t *testing.T) {
	t.Parallel()

	orig := Message{ID: "m1", Role: RoleUser}
	got := orig.Redacted()
	assert.Nil(t, got.Content)
	assert.Equal(t, "m1", got.ID)
}

func TestRedactText_MultipleSecretsOneString(t *testing.T) {
	t.Parallel()

	in := "key sk-abcDEF1234567890ghIJKLmnop and id AKIAIOSFODNN7EXAMPLE"
	got := RedactText(in)
	assert.NotContains(t, got, "sk-abcDEF1234567890ghIJKLmnop")
	assert.NotContains(t, got, "AKIAIOSFODNN7EXAMPLE")
	assert.Equal(t, 2, strings.Count(got, RedactPlaceholder))
}
