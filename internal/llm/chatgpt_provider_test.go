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
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// writeChatGPTAuth writes a credential file with the given token, account id,
// and expiry, returning its path.
func writeChatGPTAuth(t *testing.T, accessToken, refreshToken, accountID string, expiresAt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chatgpt_auth.json")
	auth := &chatgptAuth{
		AuthMode: "chatgpt",
		Tokens: chatgptTokens{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			AccountID:    accountID,
		},
		ExpiresAt: expiresAt,
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestChatGPTProviderSendsAuthHeadersAndCodexBody(t *testing.T) {
	authPath := writeChatGPTAuth(t, "tok-abc", "refresh-1", "acct-123", time.Now().Add(time.Hour))

	var gotAuth, gotAccount, gotOriginator string
	var gotBody responsesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("originator")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi from chatgpt\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
	}))
	defer srv.Close()

	p := &chatgptOAuthProvider{
		name:     "chatgpt",
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
	var usage Usage
	for ev := range ch {
		switch e := ev.(type) {
		case DeltaTextEvent:
			text += e.Text
		case EndEvent:
			usage = e.Usage
		case ErrorEvent:
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}

	require.Equal(t, "Bearer tok-abc", gotAuth, "must send the stored access token")
	require.Equal(t, "acct-123", gotAccount, "must send the chatgpt-account-id header")
	require.Equal(t, codexOriginator, gotOriginator)
	require.NotNil(t, gotBody.Store)
	require.False(t, *gotBody.Store, "ChatGPT backend requires store=false")
	require.Equal(t, "hi from chatgpt", text)
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 2, usage.OutputTokens)
}

func TestChatGPTProviderMissingAuthReturnsChatGPTAuthError(t *testing.T) {
	p := &chatgptOAuthProvider{
		name:     "chatgpt",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   http.DefaultClient,
		authPath: filepath.Join(t.TempDir(), "does-not-exist.json"),
	}
	_, err := p.Stream(context.Background(), Request{Model: "gpt-5.1-codex"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth))
}

func TestChatGPTProvider401MapsToChatGPTAuthError(t *testing.T) {
	authPath := writeChatGPTAuth(t, "live-but-rejected", "refresh-1", "acct-1", time.Now().Add(time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &chatgptOAuthProvider{
		name:     "chatgpt",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   srv.Client(),
		authPath: authPath,
		endpoint: srv.URL + "/responses",
	}
	_, err := p.Stream(context.Background(), Request{Model: "gpt-5.1-codex"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth), "401 must advise re-running auth via ErrChatGPTAuth")
}

func TestChatGPTProviderRejectsTools(t *testing.T) {
	authPath := writeChatGPTAuth(t, "tok", "refresh", "acct", time.Now().Add(time.Hour))
	p := &chatgptOAuthProvider{
		name:     "chatgpt",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   http.DefaultClient,
		authPath: authPath,
	}
	_, err := p.Stream(context.Background(), Request{
		Model: "gpt-5.1-codex",
		Tools: []Tool{{Name: "do_thing"}},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsupportedFeature))
}

// TestChatGPTProviderRefreshesExpiredToken confirms that an expired access token
// is transparently refreshed before the request, the refreshed token is sent to
// the backend, and the refreshed credentials are written back to disk.
func TestChatGPTProviderRefreshesExpiredToken(t *testing.T) {
	// Stored token is already expired but has a refresh token.
	authPath := writeChatGPTAuth(t, "stale-access", "the-refresh", "acct-9", time.Now().Add(-time.Hour))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
		require.Equal(t, "the-refresh", r.PostForm.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fresh-access", ExpiresIn: 3600})
	}))
	defer tokenSrv.Close()

	var gotAuth string
	backendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}))
	defer backendSrv.Close()

	p := &chatgptOAuthProvider{
		name:     "chatgpt",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   backendSrv.Client(),
		authPath: authPath,
		endpoint: backendSrv.URL + "/responses",
		tokenURL: tokenSrv.URL,
	}

	ch, err := p.Stream(context.Background(), Request{
		Model:    "gpt-5.1-codex",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	for ev := range ch {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}

	require.Equal(t, "Bearer fresh-access", gotAuth, "the backend must receive the refreshed token")

	// The refreshed token (and the carried-forward refresh token + account id)
	// must be persisted for the next run.
	loaded, err := loadChatGPTAuth(authPath)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", loaded.Tokens.AccessToken)
	require.Equal(t, "the-refresh", loaded.Tokens.RefreshToken)
	require.Equal(t, "acct-9", loaded.Tokens.AccountID)
	require.False(t, loaded.expired(time.Now()), "refreshed token must no longer be expired")
}

func TestChatGPTProviderExpiredTokenNoRefreshTokenErrors(t *testing.T) {
	// Expired with no refresh token: must surface ErrChatGPTAuth, not attempt a
	// (futile) refresh.
	authPath := writeChatGPTAuth(t, "stale", "", "acct", time.Now().Add(-time.Hour))
	p := &chatgptOAuthProvider{
		name:     "chatgpt",
		models:   []Model{{ID: "gpt-5.1-codex"}},
		client:   http.DefaultClient,
		authPath: authPath,
	}
	_, err := p.Stream(context.Background(), Request{Model: "gpt-5.1-codex"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth))
}
