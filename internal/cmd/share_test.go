package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// installShareApp wires newApp to an App backed by a real in-memory session
// repo seeded with one fixture session and a small transcript. It returns the
// seeded session ID and a restore func. No network or external process is used.
func installShareApp(t *testing.T) (string, func()) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "share.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	repo := session.NewRepo(database)
	ctx := context.Background()

	sess := &session.Session{
		ID:          "share-sess-1",
		ProjectPath: "/tmp/project",
		Title:       "Refactor the parser",
		Model:       "deepseek-chat",
		Agent:       "coder",
	}
	require.NoError(t, repo.Create(ctx, sess))

	when := time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC)
	transcript := []message.Message{
		{
			Role:      message.RoleUser,
			CreatedAt: when,
			Content: []message.ContentBlock{
				message.TextBlock{Text: "Please refactor the tokenizer."},
			},
		},
		{
			Role:      message.RoleAssistant,
			CreatedAt: when.Add(time.Second),
			Content: []message.ContentBlock{
				message.TextBlock{Text: "Refactoring the tokenizer now."},
				message.ToolUseBlock{ID: "call-1", Name: "edit_file", Input: json.RawMessage(`{"path":"lex.go"}`)},
			},
		},
	}
	for _, msg := range transcript {
		require.NoError(t, repo.AppendMessage(ctx, sess.ID, msg))
	}

	fake := &app.App{DB: database, Sessions: repo}
	old := newApp
	newApp = func(ctx context.Context, opts app.Options) (*app.App, error) {
		_ = ctx
		_ = opts
		return fake, nil
	}
	return sess.ID, func() { newApp = old }
}

// stubGistCreator swaps gistCreator for a recording stub and returns a pointer
// to the captured request plus a restore func.
func stubGistCreator(t *testing.T, url string, err error) (*gistRequest, func()) {
	t.Helper()
	var captured gistRequest
	old := gistCreator
	gistCreator = func(ctx context.Context, req gistRequest) (string, error) {
		_ = ctx
		captured = req
		return url, err
	}
	return &captured, func() { gistCreator = old }
}

func TestShareUploadsTranscriptAndPrintsURL(t *testing.T) {
	sessID, restoreApp := installShareApp(t)
	defer restoreApp()
	captured, restoreCreator := stubGistCreator(t, "https://gist.github.com/abc123", nil)
	defer restoreCreator()

	stdout, stderr, err := executeRoot(t, "share", sessID)
	require.NoError(t, err)
	require.Empty(t, stderr)

	// The returned URL is printed verbatim on stdout.
	require.Equal(t, "https://gist.github.com/abc123\n", stdout)

	// The uploader received the rendered Markdown transcript, not raw structs.
	require.Contains(t, captured.Content, "# Refactor the parser")
	require.Contains(t, captured.Content, "## User")
	require.Contains(t, captured.Content, "Please refactor the tokenizer.")
	require.Contains(t, captured.Content, "**Tool call: edit_file**")
	require.Contains(t, captured.Content, `{"path":"lex.go"}`)

	// And sensible gist metadata.
	require.Equal(t, "bharatcode-session.md", captured.Filename)
	require.Contains(t, captured.Description, "Refactor the parser")
	require.False(t, captured.Public, "default share should create a secret gist")
}

func TestSharePublicFlagSetsPublicGist(t *testing.T) {
	sessID, restoreApp := installShareApp(t)
	defer restoreApp()
	captured, restoreCreator := stubGistCreator(t, "https://gist.github.com/pub", nil)
	defer restoreCreator()

	stdout, _, err := executeRoot(t, "share", sessID, "--public")
	require.NoError(t, err)
	require.Equal(t, "https://gist.github.com/pub\n", stdout)
	require.True(t, captured.Public)
}

func TestShareNoArgUsesLatestSession(t *testing.T) {
	_, restoreApp := installShareApp(t)
	defer restoreApp()
	captured, restoreCreator := stubGistCreator(t, "https://gist.github.com/latest", nil)
	defer restoreCreator()

	// --project-dir matches the seeded session's ProjectPath so Latest finds it.
	stdout, stderr, err := executeRoot(t, "--project-dir", "/tmp/project", "share")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "https://gist.github.com/latest\n", stdout)
	require.Contains(t, captured.Content, "Please refactor the tokenizer.")
}

func TestShareUnknownSession(t *testing.T) {
	_, restoreApp := installShareApp(t)
	defer restoreApp()
	_, restoreCreator := stubGistCreator(t, "unused", nil)
	defer restoreCreator()

	stdout, stderr, err := executeRoot(t, "share", "does-not-exist")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "session does-not-exist not found")
}

func TestShareUploaderUnavailablePrintsGuidance(t *testing.T) {
	sessID, restoreApp := installShareApp(t)
	defer restoreApp()
	_, restoreCreator := stubGistCreator(t, "", ErrGistUploaderUnavailable)
	defer restoreCreator()

	stdout, stderr, err := executeRoot(t, "share", sessID)
	require.Error(t, err)
	require.Empty(t, stdout)
	// Graceful, actionable messaging rather than a raw wrapped error.
	require.Contains(t, stderr, "no gist uploader available")
	require.Contains(t, stderr, "cli.github.com")
	require.Contains(t, stderr, "GH_TOKEN")
	// It must be a single "Error: ..." line, matching the rest of the command
	// tree (no duplicate guidance from both a manual print and printError).
	require.Equal(t, 1, strings.Count(stderr, "no gist uploader available"),
		"guidance must appear exactly once, got:\n%s", stderr)
	require.True(t, strings.HasPrefix(stderr, "Error: "), "stderr:\n%s", stderr)
}

func TestCreateGistUnavailableWhenNoCLINoToken(t *testing.T) {
	oldLook := gistLookPath
	oldToken := githubToken
	defer func() {
		gistLookPath = oldLook
		githubToken = oldToken
	}()
	gistLookPath = func(string) (string, bool) { return "", false }
	githubToken = func() string { return "" }

	_, err := createGist(context.Background(), gistRequest{Filename: "x.md", Content: "hi"})
	require.ErrorIs(t, err, ErrGistUploaderUnavailable)
}

func TestCreateGistViaAPIPostsTranscriptAndReturnsURL(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://gist.github.com/server-id"}`))
	}))
	defer server.Close()

	// Point the API client at the test server by rewriting the request URL.
	client := server.Client()
	client.Transport = rewriteHost{base: server.URL, rt: client.Transport}

	url, err := createGistViaAPI(context.Background(), client, "tok-123", gistRequest{
		Filename:    "bharatcode-session.md",
		Content:     "# transcript body",
		Description: "BharatCode session: demo",
		Public:      true,
	})
	require.NoError(t, err)
	require.Equal(t, "https://gist.github.com/server-id", url)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/gists", gotPath)
	require.Equal(t, "Bearer tok-123", gotAuth)
	require.Equal(t, true, gotBody["public"])

	files, ok := gotBody["files"].(map[string]any)
	require.True(t, ok)
	file, ok := files["bharatcode-session.md"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "# transcript body", file["content"])
}

func TestCreateGistViaAPIErrorsOnNonCreatedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := server.Client()
	client.Transport = rewriteHost{base: server.URL, rt: client.Transport}

	_, err := createGistViaAPI(context.Background(), client, "bad-token", gistRequest{Filename: "x.md", Content: "y"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// rewriteHost redirects requests bound for api.github.com to the test server,
// so the uploader's hardcoded URL is exercised end to end without real network.
type rewriteHost struct {
	base string
	rt   http.RoundTripper
}

func (h rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := req.URL.Parse(h.base)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	rt := h.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}
