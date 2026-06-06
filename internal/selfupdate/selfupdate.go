// Package selfupdate checks whether a newer BharatCode build is available
// upstream and reports how to obtain it. It performs a single, best-effort
// HTTP call against the GitHub API for the repository's default branch and
// compares the returned commit against the commit the running binary was
// built from.
//
// The package is deliberately split into a network step (LatestCommit) and a
// pure comparison step (Compare) so the decision logic can be unit-tested with
// fixtures and no network access. Honouring the project's offline/sovereignty
// posture, callers must not invoke the network step when offline mode is on.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIURL is the GitHub API endpoint returning the latest commit on the
// repository's default branch. The response's "sha" field is the upstream
// commit the running binary is compared against.
const DefaultAPIURL = "https://api.github.com/repos/arbazkhan971/bharatcode/commits/main"

// checkTimeout bounds the update probe so a slow or unreachable network never
// delays a command for more than a moment. The probe is best-effort: any
// failure is reported to the caller and otherwise ignored.
const checkTimeout = 4 * time.Second

// Status is the outcome of an update check.
type Status struct {
	// Current is the short commit the running binary was built from.
	Current string
	// Latest is the short commit currently on the upstream default branch.
	Latest string
	// UpdateAvailable is true when Latest differs from Current and both are
	// known (non-empty and not the zero placeholder).
	UpdateAvailable bool
}

// commitResponse is the subset of the GitHub commits API document consumed
// here. Unknown fields are ignored.
type commitResponse struct {
	SHA string `json:"sha"`
}

// LatestCommit fetches the upstream default-branch commit SHA over HTTP. It is
// the only networked function in this package; split out so Compare can be
// tested with no network. The returned SHA is the full hash as GitHub reports
// it; callers typically Short() it before display or comparison.
func LatestCommit(ctx context.Context, apiURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating update-check request: %w", err)
	}
	// The GitHub API expects this Accept header and a User-Agent.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bharatcode-selfupdate")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for updates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update check returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading update-check response: %w", err)
	}
	var parsed commitResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decoding update-check response: %w", err)
	}
	if strings.TrimSpace(parsed.SHA) == "" {
		return "", fmt.Errorf("update check returned no commit sha")
	}
	return parsed.SHA, nil
}

// Short normalises a commit hash to the seven-character short form used in
// version output. Hashes shorter than seven characters are returned unchanged.
func Short(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// Compare decides whether an update is available from a built-from commit and
// an upstream commit, without any network access. It treats the empty string
// and the all-zero placeholder ("0000000") as "unknown" and never reports an
// update against an unknown value, so a binary built without commit injection
// does not nag. Comparison is on the short form so a full upstream SHA matches
// a short built-in one.
func Compare(currentCommit, latestCommit string) Status {
	cur := Short(currentCommit)
	latest := Short(latestCommit)
	known := cur != "" && cur != "0000000" && latest != ""
	return Status{
		Current:         cur,
		Latest:          latest,
		UpdateAvailable: known && cur != latest,
	}
}

// Check performs a full update check: it fetches the upstream commit and
// compares it against currentCommit. It must not be called in offline mode.
// Any network error is returned; callers treat a failed check as "no update
// information" rather than a fatal error.
func Check(ctx context.Context, apiURL, currentCommit string) (Status, error) {
	latest, err := LatestCommit(ctx, apiURL)
	if err != nil {
		return Status{Current: Short(currentCommit)}, err
	}
	return Compare(currentCommit, latest), nil
}

// CheckWithTimeout is Check with the package's default best-effort timeout
// applied to the supplied context.
func CheckWithTimeout(ctx context.Context, apiURL, currentCommit string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return Check(ctx, apiURL, currentCommit)
}

// Advice returns a one-line, user-facing message describing the update state,
// or the empty string when no update is available. The message names the
// short commits and the canonical way to update a source install.
func (s Status) Advice() string {
	if !s.UpdateAvailable {
		return ""
	}
	return fmt.Sprintf(
		"A newer BharatCode is available (%s -> %s). Update with: git pull && go build ./...",
		s.Current, s.Latest,
	)
}
