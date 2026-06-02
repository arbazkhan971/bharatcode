package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// writeCodexAuth writes a minimal auth.json with the given token + account id
// and returns its path.
func writeCodexAuth(t *testing.T, token, accountID string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	body := map[string]any{
		"auth_mode": "chatgpt",
		"tokens":    map[string]string{"access_token": token, "account_id": accountID},
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestCodexOAuthSendsAuthHeadersAndCodexBody(t *testing.T) {
	authPath := writeCodexAuth(t, "tok-abc", "acct-123")

	var gotAuth, gotAccount, gotOriginator string
	var gotBody responsesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		gotOriginator = r.Header.Get("originator")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi from codex\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
	}))
	defer srv.Close()

	// Point the provider at the stub by overriding the package endpoint via a
	// provider whose Stream we drive against the test server URL.
	p := &codexOAuthProvider{
		name:     "codex-oauth",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   srv.Client(),
		authPath: authPath,
		endpoint: srv.URL + "/responses",
	}

	ch, err := p.Stream(context.Background(), Request{
		Model:    "gpt-5.1-codex",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hello"}}}},
	})
	require.NoError(t, err)

	var text string
	for ev := range ch {
		if d, ok := ev.(DeltaTextEvent); ok {
			text += d.Text
		}
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}

	require.Equal(t, "Bearer tok-abc", gotAuth, "must send the access token from auth.json")
	require.Equal(t, "acct-123", gotAccount, "must send the account id header")
	require.Equal(t, codexOriginator, gotOriginator)
	require.NotNil(t, gotBody.Store)
	require.False(t, *gotBody.Store, "Codex backend requires store=false")
	require.Equal(t, "hi from codex", text, "assistant text must reach the caller")
}

func TestCodexOAuthMissingAuthFileReturnsCodexAuthError(t *testing.T) {
	p := &codexOAuthProvider{
		name:     "codex-oauth",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   http.DefaultClient,
		authPath: filepath.Join(t.TempDir(), "does-not-exist.json"),
	}
	_, err := p.Stream(context.Background(), Request{Model: "gpt-5.1-codex"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCodexAuth), "missing auth must map to ErrCodexAuth")
}

func TestCodexOAuth401MapsToCodexAuthError(t *testing.T) {
	authPath := writeCodexAuth(t, "expired", "acct-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &codexOAuthProvider{
		name:     "codex-oauth",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   srv.Client(),
		authPath: authPath,
		endpoint: srv.URL + "/responses",
	}
	_, err := p.Stream(context.Background(), Request{Model: "gpt-5.1-codex"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCodexAuth), "401 must advise re-running Codex via ErrCodexAuth")
}
