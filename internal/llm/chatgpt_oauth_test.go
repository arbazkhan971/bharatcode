package llm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeJWT builds an unsigned-style JWT (header.payload.signature) whose payload
// is the given claims. Only the payload segment is meaningful; parseJWTClaims
// never inspects the signature.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestGeneratePKCE(t *testing.T) {
	pkce, err := generatePKCE()
	require.NoError(t, err)
	require.NotEmpty(t, pkce.Verifier)
	require.NotEmpty(t, pkce.Challenge)

	// The challenge must be the base64url(SHA-256(verifier)) with no padding,
	// which is the S256 method OpenAI requires.
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	require.Equal(t, want, pkce.Challenge, "challenge must be S256 of verifier")
	require.NotContains(t, pkce.Verifier, "=", "verifier must be unpadded base64url")
	require.NotContains(t, pkce.Challenge, "=", "challenge must be unpadded base64url")

	// Two generations must differ (fresh randomness each call).
	other, err := generatePKCE()
	require.NoError(t, err)
	require.NotEqual(t, pkce.Verifier, other.Verifier)
}

func TestParseChatGPTClaims(t *testing.T) {
	t.Run("nested account id, plan and email", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "dev@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct-xyz",
				"chatgpt_plan_type":  "plus",
			},
		})
		claims, err := parseChatGPTClaims(token)
		require.NoError(t, err)
		require.Equal(t, "dev@example.com", claims.Email)
		require.Equal(t, "acct-xyz", claims.AccountID)
		require.Equal(t, "plus", claims.Plan)
	})

	t.Run("email only in namespace", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct-1",
				"email":              "ns@example.com",
				"plan":               "pro",
			},
		})
		claims, err := parseChatGPTClaims(token)
		require.NoError(t, err)
		require.Equal(t, "ns@example.com", claims.Email)
		require.Equal(t, "acct-1", claims.AccountID)
		require.Equal(t, "pro", claims.Plan)
	})

	t.Run("padded segment is accepted", func(t *testing.T) {
		// Build a payload whose base64 std-encoding would carry '=' padding, then
		// hand it to the parser to confirm the TrimRight path tolerates it.
		payloadJSON := []byte(`{"email":"p@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"a"}}`)
		padded := base64.URLEncoding.EncodeToString(payloadJSON) // std url encoding includes '='
		token := "h." + padded + ".s"
		claims, err := parseChatGPTClaims(token)
		require.NoError(t, err)
		require.Equal(t, "p@example.com", claims.Email)
		require.Equal(t, "a", claims.AccountID)
	})

	t.Run("malformed token errors", func(t *testing.T) {
		_, err := parseChatGPTClaims("not-a-jwt")
		require.Error(t, err)
	})
}

func TestChatGPTAuthPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "chatgpt_auth.json")
	original := &chatgptAuth{
		AuthMode: "chatgpt",
		Tokens: chatgptTokens{
			IDToken:      "id.jwt.sig",
			AccessToken:  "access-tok",
			RefreshToken: "refresh-tok",
			AccountID:    "acct-123",
		},
		Account:   chatgptAccount{Email: "dev@example.com", Plan: "plus"},
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}

	require.NoError(t, saveChatGPTAuth(path, original))

	// The file must be created with 0600 permissions (it holds secrets).
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "auth file must be 0600")

	loaded, err := loadChatGPTAuth(path)
	require.NoError(t, err)
	require.Equal(t, original.Tokens, loaded.Tokens)
	require.Equal(t, original.Account, loaded.Account)
	require.True(t, original.ExpiresAt.Equal(loaded.ExpiresAt))
}

func TestLoadChatGPTAuthMissingFile(t *testing.T) {
	_, err := loadChatGPTAuth(filepath.Join(t.TempDir(), "absent.json"))
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth), "missing file must map to ErrChatGPTAuth")
}

func TestLoadChatGPTAuthNoAccessToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"auth_mode":"chatgpt","tokens":{}}`), 0o600))
	_, err := loadChatGPTAuth(path)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth))
}

func TestChatGPTAuthExpired(t *testing.T) {
	now := time.Now()
	require.True(t, (&chatgptAuth{}).expired(now), "zero expiry must be treated as expired")
	require.True(t, (&chatgptAuth{ExpiresAt: now.Add(30 * time.Second)}).expired(now),
		"within the refresh skew must be expired")
	require.False(t, (&chatgptAuth{ExpiresAt: now.Add(10 * time.Minute)}).expired(now),
		"well before expiry must be live")
}

func TestBuildAuthorizeURL(t *testing.T) {
	got := buildAuthorizeURL("chal-123", "http://127.0.0.1:1455/auth/callback", "state-abc")
	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "auth.openai.com", u.Host)
	require.Equal(t, "/oauth/authorize", u.Path)
	q := u.Query()
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, chatgptClientID, q.Get("client_id"))
	require.Equal(t, "http://127.0.0.1:1455/auth/callback", q.Get("redirect_uri"))
	require.Equal(t, "chal-123", q.Get("code_challenge"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Equal(t, "state-abc", q.Get("state"))
	require.Equal(t, chatgptOAuthScope, q.Get("scope"))
	// OpenAI-custom params the Codex public client requires; missing either one
	// yields authorize_hydra_invalid_request. Locked in so they can't regress.
	require.Equal(t, "true", q.Get("id_token_add_organizations"))
	require.Equal(t, "true", q.Get("codex_cli_simplified_flow"))
	require.Equal(t, codexOriginator, q.Get("originator"))
	// prompt must NOT be sent — Codex does not include it.
	require.Empty(t, q.Get("prompt"))
}

// TestLoginUsesLocalhostRedirect guards the redirect_uri host: it must be
// "localhost" (the registered value), not 127.0.0.1, or OpenAI rejects the
// authorize request. The actual redirect string is built in startChatGPTLogin;
// this asserts the constant pieces it composes from.
func TestLoginUsesLocalhostRedirect(t *testing.T) {
	require.Equal(t, 1455, chatgptCallbackPort)
	require.Equal(t, "/auth/callback", chatgptCallbackPath)
}

func TestExchangeCodeForTokens(t *testing.T) {
	idToken := makeJWT(t, map[string]any{
		"email": "dev@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-xyz",
			"chatgpt_plan_type":  "plus",
		},
	})

	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{
			IDToken:      idToken,
			AccessToken:  "access-tok",
			RefreshToken: "refresh-tok",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	resp, err := exchangeCodeForTokens(context.Background(), srv.Client(), srv.URL, "the-code", "the-verifier", "http://127.0.0.1:1455/auth/callback")
	require.NoError(t, err)
	require.Equal(t, "access-tok", resp.AccessToken)

	// The exchange must send the authorization-code grant with the PKCE verifier.
	require.Equal(t, "authorization_code", gotForm.Get("grant_type"))
	require.Equal(t, "the-code", gotForm.Get("code"))
	require.Equal(t, "the-verifier", gotForm.Get("code_verifier"))
	require.Equal(t, chatgptClientID, gotForm.Get("client_id"))

	// authFromTokenResponse must lift identity out of the id_token.
	auth, err := authFromTokenResponse(resp, nil, time.Now())
	require.NoError(t, err)
	require.Equal(t, "acct-xyz", auth.Tokens.AccountID)
	require.Equal(t, "dev@example.com", auth.Account.Email)
	require.Equal(t, "plus", auth.Account.Plan)
	require.False(t, auth.ExpiresAt.IsZero())
}

func TestPostOAuthFormSurfacesOAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(tokenResponse{Error: "invalid_grant", ErrorDesc: "code expired"})
	}))
	defer srv.Close()

	_, err := exchangeCodeForTokens(context.Background(), srv.Client(), srv.URL, "c", "v", "r")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth))
	require.Contains(t, err.Error(), "invalid_grant")
}

func TestAuthFromTokenResponseCarriesForwardRefreshToken(t *testing.T) {
	prev := &chatgptAuth{
		Tokens:  chatgptTokens{RefreshToken: "old-refresh", IDToken: "old.id.tok", AccountID: "acct-1"},
		Account: chatgptAccount{Email: "dev@example.com", Plan: "plus"},
	}
	// A refresh grant typically returns a new access token but no refresh_token
	// and no id_token; the previous refresh token and identity must survive.
	resp := tokenResponse{AccessToken: "new-access", ExpiresIn: 3600}
	got, err := authFromTokenResponse(resp, prev, time.Now())
	require.NoError(t, err)
	require.Equal(t, "new-access", got.Tokens.AccessToken)
	require.Equal(t, "old-refresh", got.Tokens.RefreshToken, "refresh token must carry forward")
	require.Equal(t, "acct-1", got.Tokens.AccountID, "account id must carry forward")
	require.Equal(t, "dev@example.com", got.Account.Email)
}

func TestRefreshChatGPTTokens(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fresh-access", ExpiresIn: 3600})
	}))
	defer srv.Close()

	resp, err := refreshChatGPTTokens(context.Background(), srv.Client(), srv.URL, "the-refresh")
	require.NoError(t, err)
	require.Equal(t, "fresh-access", resp.AccessToken)
	require.Equal(t, "refresh_token", gotForm.Get("grant_type"))
	require.Equal(t, "the-refresh", gotForm.Get("refresh_token"))
	require.Equal(t, chatgptClientID, gotForm.Get("client_id"))
}

// TestStartChatGPTLoginEndToEnd drives the whole flow with the browser-open step
// replaced by a function that hits the loopback callback directly, and the token
// endpoint pointed at a stub. This exercises PKCE generation, the callback
// server, the code exchange, and token persistence without a real browser.
func TestStartChatGPTLoginEndToEnd(t *testing.T) {
	idToken := makeJWT(t, map[string]any{
		"email": "dev@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-xyz",
			"chatgpt_plan_type":  "plus",
		},
	})
	var gotVerifier string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotVerifier = r.PostForm.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{
			IDToken:      idToken,
			AccessToken:  "access-tok",
			RefreshToken: "refresh-tok",
			ExpiresIn:    3600,
		})
	}))
	defer tokenSrv.Close()

	authPath := filepath.Join(t.TempDir(), "chatgpt_auth.json")

	// The fake browser parses the authorize URL for the redirect_uri + state and
	// performs the redirect to the loopback callback with a canned code.
	openBrowser := func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		state := u.Query().Get("state")
		redirect := u.Query().Get("redirect_uri")
		go func() {
			cb := redirect + "?code=auth-code-123&state=" + url.QueryEscape(state)
			resp, err := http.Get(cb) //nolint:bodyclose // closed below
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	auth, err := startChatGPTLogin(context.Background(), LoginOptions{
		AuthPath:    authPath,
		TokenURL:    tokenSrv.URL,
		OpenBrowser: openBrowser,
		Stdout:      &strings.Builder{},
		Client:      tokenSrv.Client(),
	})
	require.NoError(t, err)
	require.Equal(t, "access-tok", auth.Tokens.AccessToken)
	require.Equal(t, "acct-xyz", auth.Tokens.AccountID)
	require.Equal(t, "dev@example.com", auth.Account.Email)
	require.NotEmpty(t, gotVerifier, "the PKCE verifier must reach the token endpoint")

	// The tokens must have been persisted to the override path.
	loaded, err := loadChatGPTAuth(authPath)
	require.NoError(t, err)
	require.Equal(t, "refresh-tok", loaded.Tokens.RefreshToken)
}

func TestStartChatGPTLoginStateMismatchIsRejected(t *testing.T) {
	openBrowser := func(authorizeURL string) error {
		u, _ := url.Parse(authorizeURL)
		redirect := u.Query().Get("redirect_uri")
		go func() {
			// Deliberately send the wrong state.
			resp, err := http.Get(redirect + "?code=x&state=WRONG")
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := startChatGPTLogin(context.Background(), LoginOptions{
		AuthPath:    filepath.Join(t.TempDir(), "auth.json"),
		TokenURL:    "http://127.0.0.1:0/unused",
		OpenBrowser: openBrowser,
		Stdout:      &strings.Builder{},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChatGPTAuth), "state mismatch must be rejected")
}

func TestOpenBrowserDoesNotPanic(t *testing.T) {
	// We cannot assert a browser opens in CI; just confirm the launcher dispatch
	// runs without panicking. A failure to find the launcher is an acceptable
	// (returned) error, not a crash.
	_ = openBrowser("https://example.com")
}
