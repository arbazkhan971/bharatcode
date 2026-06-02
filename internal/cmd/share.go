package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

// ErrGistUploaderUnavailable signals that no gist upload mechanism could be
// found at runtime: neither the gh CLI on PATH nor a GitHub token in the
// environment. The share command turns it into a clear, actionable message.
var ErrGistUploaderUnavailable = errors.New(
	"no gist uploader available: install the gh CLI (https://cli.github.com) " +
		"or set GH_TOKEN/GITHUB_TOKEN to a token with the 'gist' scope",
)

// gistRequest carries everything the uploader needs to create a gist.
type gistRequest struct {
	// Filename is the name of the single file in the gist (e.g. "session.md").
	Filename string
	// Content is the rendered transcript to upload.
	Content string
	// Description is the gist description shown on GitHub.
	Description string
	// Public reports whether the gist should be publicly listed.
	Public bool
}

// gistCreator uploads a transcript as a GitHub gist and returns the gist URL.
// It is a package var so tests stub it and exercise the command offline with
// no real network. The default implementation prefers the gh CLI and falls
// back to the GitHub API with a token from the environment.
var gistCreator = createGist

// createGist is the production uploader. It uses the gh CLI when available,
// otherwise the GitHub REST API with a token from GH_TOKEN or GITHUB_TOKEN.
// When neither path is available it returns ErrGistUploaderUnavailable.
func createGist(ctx context.Context, req gistRequest) (string, error) {
	if path, ok := gistLookPath("gh"); ok {
		return createGistViaCLI(ctx, path, req)
	}
	if token := githubToken(); token != "" {
		return createGistViaAPI(ctx, githubAPIClient, token, req)
	}
	return "", ErrGistUploaderUnavailable
}

// gistLookPath resolves an executable on PATH. It is a package var so tests
// can simulate gh being present or absent without touching the real PATH.
var gistLookPath = func(name string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}

// githubToken reads a GitHub token from the environment, preferring GH_TOKEN.
var githubToken = func() string {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// githubAPIClient is the HTTP client used by the API uploader. It is a package
// var so the (unstubbed) API path could be pointed at a test server; the
// command tests stub gistCreator itself and never reach this.
var githubAPIClient = &http.Client{Timeout: 30 * time.Second}

// createGistViaCLI shells out to "gh gist create" and returns the URL the CLI
// prints to stdout. The content is fed on stdin so transcripts of any size and
// with any characters round-trip without quoting concerns.
func createGistViaCLI(ctx context.Context, ghPath string, req gistRequest) (string, error) {
	args := []string{"gist", "create", "--filename", req.Filename, "--desc", req.Description}
	if req.Public {
		args = append(args, "--public")
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, ghPath, args...)
	cmd.Stdin = strings.NewReader(req.Content)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("gh gist create: %w: %s", err, detail)
		}
		return "", fmt.Errorf("gh gist create: %w", err)
	}
	url := strings.TrimSpace(stdout.String())
	if url == "" {
		return "", fmt.Errorf("gh gist create: empty URL in output")
	}
	return url, nil
}

// createGistViaAPI posts to the GitHub gists endpoint with a bearer token and
// returns the html_url from the response.
func createGistViaAPI(ctx context.Context, client *http.Client, token string, req gistRequest) (string, error) {
	body, err := json.Marshal(map[string]any{
		"description": req.Description,
		"public":      req.Public,
		"files": map[string]any{
			req.Filename: map[string]string{"content": req.Content},
		},
	})
	if err != nil {
		return "", fmt.Errorf("encoding gist request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/gists", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building gist request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("creating gist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("creating gist: github returned %s", resp.Status)
	}

	var decoded struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decoding gist response: %w", err)
	}
	if decoded.HTMLURL == "" {
		return "", fmt.Errorf("creating gist: response missing html_url")
	}
	return decoded.HTMLURL, nil
}

func newShareCmd() *cobra.Command {
	var public bool
	cmd := &cobra.Command{
		Use:   "share [session-id]",
		Short: "Upload a session transcript to a GitHub gist",
		Long: "Render a session transcript as Markdown and upload it to a GitHub gist, " +
			"printing the resulting URL. Uses the gh CLI when available, otherwise the " +
			"GitHub API with a token from GH_TOKEN or GITHUB_TOKEN.\n\n" +
			"With no session-id, the most recent session for the project is shared.",
		Args:    cobra.MaximumNArgs(1),
		Example: "  bharatcode share\n  bharatcode share sess-1 --public",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := buildApp(cmd.Context(), getRootOptions(cmd))
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)

			sess, err := resolveShareSession(cmd, application.Sessions, args)
			if err != nil {
				return err
			}

			messages, err := application.Sessions.Messages(cmd.Context(), sess.ID)
			if err != nil {
				return fmt.Errorf("loading transcript: %w", err)
			}

			transcript, err := session.ExportMarkdown(sess, messages)
			if err != nil {
				return fmt.Errorf("rendering transcript: %w", err)
			}

			req := gistRequest{
				Filename:    "bharatcode-session.md",
				Content:     transcript,
				Description: shareDescription(sess),
				Public:      public,
			}
			url, err := gistCreator(cmd.Context(), req)
			if err != nil {
				// ErrGistUploaderUnavailable already carries clear, actionable
				// guidance; return it unwrapped so executeCommand prints a single
				// "Error: ..." line (matching the rest of the command tree) rather
				// than burying the hint behind an "uploading gist:" prefix.
				if errors.Is(err, ErrGistUploaderUnavailable) {
					return err
				}
				return fmt.Errorf("uploading gist: %w", err)
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), url)
			return nil
		},
	}
	cmd.Flags().BoolVar(&public, "public", false, "create a public gist (default is secret)")
	return cmd
}

// resolveShareSession loads the session named by args, or the project's most
// recent session when no id is given. It maps a missing session to a clear
// error rather than a wrapped repository error.
func resolveShareSession(cmd *cobra.Command, repo *session.Repo, args []string) (*session.Session, error) {
	if len(args) == 1 {
		sess, err := repo.Get(cmd.Context(), args[0])
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				return nil, fmt.Errorf("session %s not found", args[0])
			}
			return nil, fmt.Errorf("getting session: %w", err)
		}
		return sess, nil
	}

	projectPath := getRootOptions(cmd).projectDir
	if projectPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectPath = cwd
		}
	}
	sess, err := repo.Latest(cmd.Context(), projectPath)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, fmt.Errorf("no sessions to share for this project")
		}
		return nil, fmt.Errorf("finding latest session: %w", err)
	}
	return sess, nil
}

// shareDescription builds a concise gist description from the session.
func shareDescription(sess *session.Session) string {
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = sess.ID
	}
	return fmt.Sprintf("BharatCode session: %s", title)
}
