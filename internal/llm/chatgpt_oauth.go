package llm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// "Sign in with ChatGPT" — EXPERIMENTAL, personal single-account use only.
//
// This implements the OAuth 2.0 Authorization Code + PKCE flow that OpenAI's
// Codex CLI uses to let a user drive the Codex backend with their own ChatGPT
// subscription instead of a metered API key. Unlike codexOAuthProvider (which is
// read-only and borrows the token the Codex CLI already stored on disk), this
// path performs the login itself — opening a browser, running a localhost
// callback server, exchanging the authorization code, and refreshing the token —
// and stores the resulting tokens under BharatCode's own config dir.
//
// It depends on undocumented OpenAI endpoints (auth.openai.com OAuth and the
// chatgpt.com Codex backend) that are outside OpenAI's published terms for
// third-party clients and can change or break without notice. It is intended
// only for a single developer reusing their own ChatGPT account; it does NOT
// support account pooling or multi-user sharing.

const (
	// chatgptClientID is Codex's public OAuth client id. PKCE makes a public
	// client safe without a client secret.
	chatgptClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// chatgptAuthorizeURL and chatgptTokenURL are OpenAI's OAuth endpoints.
	chatgptAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	chatgptTokenURL     = "https://auth.openai.com/oauth/token"

	// chatgptOAuthScope is the scope set Codex requests; offline_access is what
	// yields a refresh_token.
	chatgptOAuthScope = "openid profile email offline_access"

	// chatgptCallbackPort is the fixed loopback port the redirect_uri targets.
	// It is fixed (not ephemeral) because it must match a redirect URI the OAuth
	// client has pre-registered, exactly as the Codex CLI does.
	chatgptCallbackPort = 1455
	// chatgptCallbackPath is the path component of the registered redirect URI.
	chatgptCallbackPath = "/auth/callback"

	// chatgptAuthClaimNamespace is the namespace OpenAI nests its custom claims
	// under inside the id_token / access_token JWT (chatgpt_account_id, plan).
	chatgptAuthClaimNamespace = "https://api.openai.com/auth"

	// chatgptBackendURL is the ChatGPT-plan Codex backend the access token is
	// accepted against. The provider appends /responses.
	chatgptBackendURL = "https://chatgpt.com/backend-api/codex"

	// chatgptRefreshSkew refreshes the access token this long before its real
	// expiry so an in-flight request does not race the deadline.
	chatgptRefreshSkew = 60 * time.Second
)

// ErrChatGPTAuth indicates the stored "Sign in with ChatGPT" credentials are
// missing or could not be refreshed. Callers should advise the user to run
// 'bharatcode auth chatgpt' to (re)authenticate.
var ErrChatGPTAuth = fmt.Errorf("chatgpt credentials unavailable: %w", ErrAuth)

// pkcePair is a generated PKCE verifier and its S256-derived challenge.
type pkcePair struct {
	Verifier  string
	Challenge string
}

// generatePKCE returns a fresh PKCE verifier/challenge pair. The verifier is 32
// bytes of randomness encoded as base64url (the RFC 7636 high-entropy form);
// the challenge is the base64url-encoded SHA-256 of the verifier, the S256
// method OpenAI requires.
func generatePKCE() (pkcePair, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return pkcePair{}, fmt.Errorf("generating pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return pkcePair{Verifier: verifier, Challenge: challenge}, nil
}

// randomState returns an opaque, URL-safe value used as the OAuth state
// parameter to bind the authorize request to its callback and defend against
// CSRF on the loopback redirect.
func randomState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// chatgptClaims holds the fields BharatCode reads out of the OAuth JWTs. They
// arrive both as top-level standard claims (email) and nested under the
// chatgptAuthClaimNamespace object (chatgpt_account_id, plan/chatgpt_plan_type).
type chatgptClaims struct {
	Email     string
	AccountID string
	Plan      string
}

// parseJWTClaims decodes the claims (payload) segment of a JWT without verifying
// its signature. Reading claims for routing/identity needs no verification — the
// token's authority is established by the TLS token exchange that issued it, not
// by us re-checking the signature — so this deliberately base64url-decodes the
// middle segment only. It is never used to make a trust decision about an
// inbound token.
func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed jwt: expected 3 segments, got %d", len(parts))
	}
	// JWT uses base64url without padding; RawURLEncoding matches. Some issuers
	// emit padded segments, so trim any '=' first to accept both forms.
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("decoding jwt claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parsing jwt claims: %w", err)
	}
	return claims, nil
}

// parseChatGPTClaims extracts the email, ChatGPT account id, and plan from an
// OAuth JWT (the id_token carries all three). The account id and plan live in a
// nested object under chatgptAuthClaimNamespace; email is a top-level standard
// claim but is also mirrored into the namespaced object, so both locations are
// checked. Missing fields yield empty strings rather than an error, since only
// the account id is strictly required downstream and the caller decides how to
// react to its absence.
func parseChatGPTClaims(idToken string) (chatgptClaims, error) {
	claims, err := parseJWTClaims(idToken)
	if err != nil {
		return chatgptClaims{}, err
	}
	out := chatgptClaims{}
	if email, ok := claims["email"].(string); ok {
		out.Email = email
	}
	if ns, ok := claims[chatgptAuthClaimNamespace].(map[string]any); ok {
		if v, ok := ns["chatgpt_account_id"].(string); ok {
			out.AccountID = v
		}
		// The plan/tier key has varied across Codex revisions; accept the few
		// spellings seen in the wild so the stored label is populated when present.
		for _, key := range []string{"chatgpt_plan_type", "chatgpt_plan", "plan"} {
			if v, ok := ns[key].(string); ok && v != "" {
				out.Plan = v
				break
			}
		}
		if out.Email == "" {
			if v, ok := ns["email"].(string); ok {
				out.Email = v
			}
		}
	}
	return out, nil
}

// chatgptAuth is the persisted credential blob for a ChatGPT subscription
// login. It is written to the BharatCode config dir (see chatgptAuthPath) as a
// JSON file with 0600 permissions.
//
// Storage choice: the credentials are kept in a file under ~/.config/bharatcode
// rather than the OS keyring. The spec permits either; the file is chosen
// because it must hold a structured, refreshable token set (access + refresh +
// id + obtained-at + expiry), it has to round-trip a write on every refresh, and
// it works uniformly across platforms without depending on an OS keyring being
// wired up. The shape deliberately mirrors the Codex CLI's own ~/.codex/auth.json
// so the two are conceptually interchangeable.
type chatgptAuth struct {
	AuthMode    string         `json:"auth_mode"`
	Tokens      chatgptTokens  `json:"tokens"`
	LastRefresh time.Time      `json:"last_refresh"`
	ObtainedAt  time.Time      `json:"obtained_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Account     chatgptAccount `json:"account"`
}

// chatgptTokens holds the raw OAuth tokens.
type chatgptTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// chatgptAccount carries the human-readable identity parsed from the id_token,
// stored alongside the tokens so 'auth' status output need not re-parse a JWT.
type chatgptAccount struct {
	Email string `json:"email,omitempty"`
	Plan  string `json:"plan,omitempty"`
}

// expired reports whether the access token is at or past its refresh deadline
// (real expiry minus a safety skew). A zero ExpiresAt is treated as expired so a
// token of unknown age is refreshed rather than used blindly.
func (a *chatgptAuth) expired(now time.Time) bool {
	if a.ExpiresAt.IsZero() {
		return true
	}
	return !now.Before(a.ExpiresAt.Add(-chatgptRefreshSkew))
}

// chatgptAuthPath returns the path of the persisted credential file, alongside
// the global config.json. override (used by tests and by the optional
// BHARATCODE_CHATGPT_AUTH env var) wins when set.
func chatgptAuthPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("BHARATCODE_CHATGPT_AUTH"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "bharatcode", "chatgpt_auth.json"), nil
	}
	return filepath.Join(home, ".config", "bharatcode", "chatgpt_auth.json"), nil
}

// loadChatGPTAuth reads and decodes the persisted credentials. A missing file
// maps to ErrChatGPTAuth with guidance to run the login flow.
func loadChatGPTAuth(path string) (*chatgptAuth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("not signed in (run 'bharatcode auth chatgpt'): %w", ErrChatGPTAuth)
		}
		return nil, fmt.Errorf("reading chatgpt auth %s: %w", path, ErrChatGPTAuth)
	}
	var auth chatgptAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parsing chatgpt auth: %w", ErrChatGPTAuth)
	}
	if auth.Tokens.AccessToken == "" {
		return nil, fmt.Errorf("no access token in chatgpt auth (run 'bharatcode auth chatgpt'): %w", ErrChatGPTAuth)
	}
	return &auth, nil
}

// saveChatGPTAuth writes the credentials atomically with 0600 permissions,
// creating the parent directory if needed.
func saveChatGPTAuth(path string, auth *chatgptAuth) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating chatgpt auth dir: %w", err)
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding chatgpt auth: %w", err)
	}
	data = append(data, '\n')
	// Write to a temp file in the same dir then rename, so a crash mid-write
	// never leaves a truncated credential file. 0600: tokens are secrets.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing chatgpt auth: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("finalizing chatgpt auth: %w", err)
	}
	return nil
}

// tokenResponse is the JSON body returned by the OAuth token endpoint for both
// the authorization-code exchange and the refresh grant.
type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// authFromTokenResponse converts an OAuth token response into a persistable
// chatgptAuth, parsing the id_token for identity and computing the absolute
// expiry from expires_in. previous carries forward fields the response may omit
// (notably the refresh_token, which a refresh grant does not always re-issue).
func authFromTokenResponse(resp tokenResponse, previous *chatgptAuth, now time.Time) (*chatgptAuth, error) {
	auth := &chatgptAuth{AuthMode: "chatgpt", ObtainedAt: now, LastRefresh: now}
	if previous != nil {
		// Inherit the prior refresh token and identity, then overlay anything the
		// new response supplies. A refresh response routinely returns no
		// refresh_token and no id_token, so without this the user would silently
		// lose the ability to refresh again.
		auth.Tokens.RefreshToken = previous.Tokens.RefreshToken
		auth.Tokens.IDToken = previous.Tokens.IDToken
		auth.Tokens.AccountID = previous.Tokens.AccountID
		auth.Account = previous.Account
		auth.ObtainedAt = previous.ObtainedAt
	}
	auth.Tokens.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		auth.Tokens.RefreshToken = resp.RefreshToken
	}
	if resp.IDToken != "" {
		auth.Tokens.IDToken = resp.IDToken
		claims, err := parseChatGPTClaims(resp.IDToken)
		if err != nil {
			return nil, fmt.Errorf("reading chatgpt identity: %w", err)
		}
		if claims.AccountID != "" {
			auth.Tokens.AccountID = claims.AccountID
		}
		auth.Account = chatgptAccount{Email: claims.Email, Plan: claims.Plan}
	}
	if resp.ExpiresIn > 0 {
		auth.ExpiresAt = now.Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return auth, nil
}

// exchangeCodeForTokens performs the OAuth authorization-code exchange (with the
// PKCE verifier) against the token endpoint and returns the parsed response.
func exchangeCodeForTokens(ctx context.Context, client *http.Client, tokenURL, code, verifier, redirectURI string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {chatgptClientID},
		"code_verifier": {verifier},
	}
	return postOAuthForm(ctx, client, tokenURL, form)
}

// refreshChatGPTTokens performs the OAuth refresh grant against the token
// endpoint and returns the parsed response.
func refreshChatGPTTokens(ctx context.Context, client *http.Client, tokenURL, refreshToken string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {chatgptClientID},
		"scope":         {chatgptOAuthScope},
	}
	return postOAuthForm(ctx, client, tokenURL, form)
}

// postOAuthForm POSTs an application/x-www-form-urlencoded body to an OAuth
// endpoint and decodes the JSON token response, mapping an OAuth error body or a
// non-2xx status onto ErrChatGPTAuth.
func postOAuthForm(ctx context.Context, client *http.Client, tokenURL string, form url.Values) (tokenResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	var out tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return tokenResponse{}, fmt.Errorf("decoding token response (status %d): %w", resp.StatusCode, ErrChatGPTAuth)
	}
	if out.Error != "" {
		msg := out.Error
		if out.ErrorDesc != "" {
			msg = out.Error + ": " + out.ErrorDesc
		}
		return tokenResponse{}, fmt.Errorf("oauth token error: %s: %w", msg, ErrChatGPTAuth)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("oauth token endpoint status %d: %w", resp.StatusCode, ErrChatGPTAuth)
	}
	if out.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("oauth token response missing access_token: %w", ErrChatGPTAuth)
	}
	return out, nil
}

// buildAuthorizeURL constructs the OAuth authorize URL carrying the PKCE
// challenge, redirect URI, scope, and state.
func buildAuthorizeURL(challenge, redirectURI, state string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {chatgptClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {chatgptOAuthScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		// id_token claims drive routing; ask the consent screen to mint the
		// account id into them, mirroring the Codex CLI's authorize request.
		"prompt": {"login"},
	}
	return chatgptAuthorizeURL + "?" + q.Encode()
}

// callbackResult carries the outcome of the loopback redirect: the
// authorization code (on success) or an error (on user denial / mismatch).
type callbackResult struct {
	code string
	err  error
}

// LoginOptions configures startChatGPTLogin. All fields are optional; the zero
// value runs the real interactive flow.
type LoginOptions struct {
	// AuthPath overrides where credentials are written (defaults to the config
	// dir). Used by tests.
	AuthPath string
	// TokenURL overrides the OAuth token endpoint (defaults to chatgptTokenURL).
	// Used by tests to point at a stub server.
	TokenURL string
	// OpenBrowser overrides how the authorize URL is opened. The default opens
	// the system browser; tests inject a function that drives the callback.
	OpenBrowser func(authorizeURL string) error
	// Stdout receives human-facing progress lines. Defaults to os.Stdout.
	Stdout interface{ Write([]byte) (int, error) }
	// Client overrides the HTTP client for the token exchange. Defaults to a
	// client with a sane timeout.
	Client *http.Client
}

// startChatGPTLogin runs the full "Sign in with ChatGPT" flow: it generates
// PKCE material, starts a loopback callback server on 127.0.0.1:1455, opens the
// authorize URL in the browser, waits for the redirect, exchanges the code for
// tokens, and persists them. It returns the stored credentials on success.
//
// EXPERIMENTAL: depends on undocumented OpenAI OAuth endpoints; personal
// single-account use only.
func startChatGPTLogin(ctx context.Context, opts LoginOptions) (*chatgptAuth, error) {
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format, args...)
	}

	pkce, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", chatgptCallbackPort, chatgptCallbackPath)

	// Bind the loopback listener before opening the browser so the redirect
	// cannot arrive before the server is ready. A bind failure (port in use) is
	// reported up front rather than as a mysterious browser hang.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", chatgptCallbackPort))
	if err != nil {
		return nil, fmt.Errorf("starting callback server on port %d (is another login in progress?): %w", chatgptCallbackPort, err)
	}

	resultCh := make(chan callbackResult, 1)
	var once sync.Once
	deliver := func(r callbackResult) { once.Do(func() { resultCh <- r }) }

	mux := http.NewServeMux()
	mux.HandleFunc(chatgptCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := q.Get("error"); errStr != "" {
			desc := q.Get("error_description")
			writeCallbackPage(w, false, desc)
			deliver(callbackResult{err: fmt.Errorf("authorization denied: %s %s: %w", errStr, desc, ErrChatGPTAuth)})
			return
		}
		if got := q.Get("state"); got != state {
			writeCallbackPage(w, false, "state mismatch")
			deliver(callbackResult{err: fmt.Errorf("oauth state mismatch: %w", ErrChatGPTAuth)})
			return
		}
		code := q.Get("code")
		if code == "" {
			writeCallbackPage(w, false, "no authorization code")
			deliver(callbackResult{err: fmt.Errorf("no authorization code in callback: %w", ErrChatGPTAuth)})
			return
		}
		writeCallbackPage(w, true, "")
		deliver(callbackResult{code: code})
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	// Always shut the server down on return so the port is freed even on error.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	authorizeURL := buildAuthorizeURL(pkce.Challenge, redirectURI, state)

	open := opts.OpenBrowser
	if open == nil {
		open = openBrowser
	}
	logf("Opening your browser to sign in with ChatGPT...\n")
	if err := open(authorizeURL); err != nil {
		// A headless box cannot open a browser; print the URL so the user can
		// open it on another device, rather than failing outright.
		logf("Could not open a browser automatically. Open this URL to continue:\n\n%s\n\n", authorizeURL)
	}

	// Wait for the redirect, a context cancel, or a generous timeout so a user
	// who abandons the flow does not leave the port bound forever.
	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		client := opts.Client
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Second}
		}
		tokenURL := opts.TokenURL
		if tokenURL == "" {
			tokenURL = chatgptTokenURL
		}
		logf("Exchanging authorization code for tokens...\n")
		resp, err := exchangeCodeForTokens(ctx, client, tokenURL, res.code, pkce.Verifier, redirectURI)
		if err != nil {
			return nil, err
		}
		auth, err := authFromTokenResponse(resp, nil, time.Now())
		if err != nil {
			return nil, err
		}
		path, err := chatgptAuthPath(opts.AuthPath)
		if err != nil {
			return nil, err
		}
		if err := saveChatGPTAuth(path, auth); err != nil {
			return nil, err
		}
		if auth.Account.Email != "" {
			logf("Signed in as %s", auth.Account.Email)
			if auth.Account.Plan != "" {
				logf(" (%s)", auth.Account.Plan)
			}
			logf("\n")
		} else {
			logf("Signed in.\n")
		}
		return auth, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("sign-in cancelled: %w", ctx.Err())
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for browser sign-in: %w", ErrChatGPTAuth)
	}
}

// writeCallbackPage renders the minimal HTML the browser shows after the
// redirect, telling the user they can close the tab and return to the terminal.
func writeCallbackPage(w http.ResponseWriter, ok bool, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		body := "<html><body style=\"font-family:sans-serif;text-align:center;padding-top:3rem\">" +
			"<h2>Sign-in failed</h2><p>" + htmlEscape(detail) + "</p>" +
			"<p>Return to your terminal and try again.</p></body></html>"
		_, _ = w.Write([]byte(body))
		return
	}
	body := "<html><body style=\"font-family:sans-serif;text-align:center;padding-top:3rem\">" +
		"<h2>Signed in to BharatCode</h2><p>You can close this tab and return to your terminal.</p></body></html>"
	_, _ = w.Write([]byte(body))
}

// htmlEscape minimally escapes the small, trusted detail strings shown on the
// callback page (they originate from OpenAI's error_description, not arbitrary
// user input, but escaping keeps the page well-formed regardless).
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// ChatGPTIdentity is the human-facing identity of a stored "Sign in with
// ChatGPT" session, returned by ChatGPTStatus for status/whoami output.
type ChatGPTIdentity struct {
	Email     string
	Plan      string
	AccountID string
	ExpiresAt time.Time
	Expired   bool
}

// LoginChatGPT runs the experimental "Sign in with ChatGPT" OAuth (PKCE) flow
// and persists the resulting tokens. It is the cmd/TUI entry point; the heavy
// lifting lives in startChatGPTLogin. On success it returns the signed-in
// identity (email/plan) for a confirmation message.
//
// EXPERIMENTAL: depends on undocumented OpenAI OAuth endpoints; personal
// single-account use only.
func LoginChatGPT(ctx context.Context, stdout interface{ Write([]byte) (int, error) }) (ChatGPTIdentity, error) {
	auth, err := startChatGPTLogin(ctx, LoginOptions{Stdout: stdout})
	if err != nil {
		return ChatGPTIdentity{}, err
	}
	return ChatGPTIdentity{
		Email:     auth.Account.Email,
		Plan:      auth.Account.Plan,
		AccountID: auth.Tokens.AccountID,
		ExpiresAt: auth.ExpiresAt,
		Expired:   auth.expired(time.Now()),
	}, nil
}

// ChatGPTStatus reports the currently stored "Sign in with ChatGPT" identity, or
// an error wrapping ErrChatGPTAuth when no session is stored.
func ChatGPTStatus() (ChatGPTIdentity, error) {
	path, err := chatgptAuthPath("")
	if err != nil {
		return ChatGPTIdentity{}, fmt.Errorf("locating chatgpt auth: %w", ErrChatGPTAuth)
	}
	auth, err := loadChatGPTAuth(path)
	if err != nil {
		return ChatGPTIdentity{}, err
	}
	return ChatGPTIdentity{
		Email:     auth.Account.Email,
		Plan:      auth.Account.Plan,
		AccountID: auth.Tokens.AccountID,
		ExpiresAt: auth.ExpiresAt,
		Expired:   auth.expired(time.Now()),
	}, nil
}

// LogoutChatGPT removes the stored "Sign in with ChatGPT" credentials. A missing
// file is not an error (logout is idempotent).
func LogoutChatGPT() error {
	path, err := chatgptAuthPath("")
	if err != nil {
		return fmt.Errorf("locating chatgpt auth: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing chatgpt auth: %w", err)
	}
	return nil
}

// openBrowser opens url in the user's default browser, selecting the launcher by
// OS. It returns an error when no launcher is available (e.g. a headless box),
// which the caller handles by printing the URL instead.
func openBrowser(rawURL string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{rawURL}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		cmd = "xdg-open"
		args = []string{rawURL}
	}
	return exec.Command(cmd, args...).Start()
}
