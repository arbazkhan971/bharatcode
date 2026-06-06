package selfupdate

// This file implements the self-applying half of the update package: rather
// than only reporting that a newer build exists (see selfupdate.go), Apply
// downloads the matching release archive, verifies it against the release's
// published SHA-256 checksums, extracts the bharatcode binary, and atomically
// swaps it in over the currently-running executable.
//
// Every network boundary is injectable (the release API base and the download
// host are fields on ApplyOptions) so the whole pipeline — resolve tag,
// download archive, fetch checksums, verify, extract, replace — can be tested
// end to end with httptest serving in-memory fixture archives and no real
// network access.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultReleaseAPIURL is the GitHub API endpoint returning the metadata for
// the repository's latest published release. Its "tag_name" field is the tag
// (e.g. "v0.2.0") whose assets Apply downloads.
const DefaultReleaseAPIURL = "https://api.github.com/repos/arbazkhan971/bharatcode/releases/latest"

// DefaultDownloadBaseURL is the prefix under which release assets live. The
// full asset URL is DefaultDownloadBaseURL + "/" + tag + "/" + assetName.
const DefaultDownloadBaseURL = "https://github.com/arbazkhan971/bharatcode/releases/download"

// checksumsAsset is the name of the per-release manifest GoReleaser publishes,
// one "<sha256>  <asset>" line per artifact. Apply downloads it to verify the
// archive before trusting its contents.
const checksumsAsset = "checksums.txt"

// binaryName is the executable inside every release archive.
const binaryName = "bharatcode"

// applyTimeout bounds the whole self-replace (download + verify + extract). A
// release archive is a few megabytes, so this is generous; it exists only so a
// stuck transfer cannot hang the process indefinitely.
const applyTimeout = 60 * time.Second

// maxArchiveBytes caps how much we read from the network and how large any
// single extracted file may be, defending the extractor against a hostile or
// corrupt archive that would otherwise exhaust memory or disk.
const maxArchiveBytes = 200 << 20 // 200 MiB

// releaseResponse is the subset of the GitHub releases API document consumed
// here. Unknown fields are ignored.
type releaseResponse struct {
	TagName string `json:"tag_name"`
}

// ApplyOptions configures a single Apply call. Every field has a usable zero
// value: an empty ExePath resolves to os.Executable(), empty URLs resolve to
// the GitHub defaults, and a nil Progress discards progress output. The URL
// fields exist so tests can point the whole pipeline at an httptest server.
type ApplyOptions struct {
	// ExePath is the on-disk executable to replace. Empty means the currently
	// running binary (os.Executable()).
	ExePath string
	// ReleaseAPIURL returns the latest release metadata. Empty uses
	// DefaultReleaseAPIURL.
	ReleaseAPIURL string
	// DownloadBaseURL is the prefix the archive and checksums are fetched from.
	// Empty uses DefaultDownloadBaseURL. The final URLs are
	// DownloadBaseURL/<tag>/<asset>.
	DownloadBaseURL string
	// GOOS and GOARCH select which release asset to download. Empty fields fall
	// back to the running binary's runtime.GOOS / runtime.GOARCH; they are
	// fields so the asset-selection path is testable across platforms.
	GOOS   string
	GOARCH string
	// Progress receives human-readable status lines. Nil discards them.
	Progress io.Writer
	// HTTPClient performs the API and download requests. Nil uses
	// http.DefaultClient.
	HTTPClient *http.Client
}

// AssetName maps a Go target (GOOS/GOARCH) to the GoReleaser asset filename
// published for that platform. It is pure and total over the supported set;
// any unsupported combination returns an error rather than guessing. The
// archive extension follows GoReleaser's convention: .zip on Windows, .tar.gz
// everywhere else.
func AssetName(goos, goarch string) (string, error) {
	// GoReleaser titlecases the OS and uses Go-uname-style arch names.
	osPart, ok := map[string]string{
		"darwin":  "Darwin",
		"linux":   "Linux",
		"windows": "Windows",
	}[goos]
	if !ok {
		return "", fmt.Errorf("unsupported OS %q for self-update", goos)
	}
	archPart, ok := map[string]string{
		"amd64": "x86_64",
		"arm64": "arm64",
	}[goarch]
	if !ok {
		return "", fmt.Errorf("unsupported architecture %q for self-update", goarch)
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("bharatcode_%s_%s.%s", osPart, archPart, ext), nil
}

// LatestReleaseTag fetches the tag_name of the repository's latest published
// release over HTTP. Like LatestCommit it is the package's only network entry
// point for this path, split out so the rest of Apply can be exercised with
// fixtures. Must not be called in offline mode.
func LatestReleaseTag(ctx context.Context, apiURL string) (string, error) {
	return latestReleaseTag(ctx, http.DefaultClient, apiURL)
}

func latestReleaseTag(ctx context.Context, client *http.Client, apiURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating release-check request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bharatcode-selfupdate")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release check returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading release-check response: %w", err)
	}
	var parsed releaseResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decoding release-check response: %w", err)
	}
	if strings.TrimSpace(parsed.TagName) == "" {
		return "", fmt.Errorf("release check returned no tag_name")
	}
	return parsed.TagName, nil
}

// Apply downloads the latest release's archive for the running platform,
// verifies its SHA-256 against the release's checksums.txt, extracts the
// bharatcode binary, and atomically replaces opts.ExePath with it.
//
// Security posture: the checksum step is mandatory. If checksums.txt cannot be
// fetched, Apply fails with a clear error rather than installing unverified
// bytes; a checksum mismatch is a hard failure that leaves the existing binary
// untouched. Verification happens entirely before the on-disk swap, so a failed
// or interrupted update never leaves a partially written executable in place.
func Apply(ctx context.Context, opts ApplyOptions) error {
	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	releaseAPIURL := opts.ReleaseAPIURL
	if releaseAPIURL == "" {
		releaseAPIURL = DefaultReleaseAPIURL
	}
	downloadBase := opts.DownloadBaseURL
	if downloadBase == "" {
		downloadBase = DefaultDownloadBaseURL
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	progress := opts.Progress
	if progress == nil {
		progress = io.Discard
	}

	exePath := opts.ExePath
	if exePath == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locating current executable: %w", err)
		}
		exePath = p
	}
	// Resolve symlinks so the atomic rename targets the real file (and the temp
	// file lands on the same filesystem as it), not a link that may point
	// elsewhere.
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	asset, err := AssetName(goos, goarch)
	if err != nil {
		return err
	}

	tag, err := latestReleaseTag(ctx, client, releaseAPIURL)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(progress, "Downloading bharatcode %s (%s)...\n", tag, asset)

	archiveURL := downloadBase + "/" + tag + "/" + asset
	archive, err := download(ctx, client, archiveURL)
	if err != nil {
		return fmt.Errorf("downloading release archive: %w", err)
	}

	// Verify before trusting a single byte of the archive's contents. A missing
	// manifest is a hard stop, not a silent skip.
	checksumsURL := downloadBase + "/" + tag + "/" + checksumsAsset
	sums, err := download(ctx, client, checksumsURL)
	if err != nil {
		return fmt.Errorf("downloading %s for verification: %w", checksumsAsset, err)
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	got := sha256.Sum256(archive)
	gotHex := hex.EncodeToString(got[:])
	if !strings.EqualFold(gotHex, want) {
		return fmt.Errorf("checksum mismatch for %s: archive sha256 %s does not match published %s; refusing to install", asset, gotHex, want)
	}
	_, _ = fmt.Fprintln(progress, "Checksum verified.")

	binary, err := extractBinary(archive, asset)
	if err != nil {
		return fmt.Errorf("extracting %s from archive: %w", binaryName, err)
	}

	if err := replaceExecutable(exePath, binary); err != nil {
		return fmt.Errorf("installing new binary: %w", err)
	}
	_, _ = fmt.Fprintf(progress, "Updated %s to %s.\n", filepath.Base(exePath), tag)
	return nil
}

// download fetches url and returns its body, bounding the read so a hostile or
// truncated server cannot stream unbounded data into memory.
func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", "bharatcode-selfupdate")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading body of %s: %w", url, err)
	}
	if len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("response from %s exceeds %d byte limit", url, maxArchiveBytes)
	}
	return data, nil
}

// checksumFor finds the SHA-256 hex digest for asset in a GoReleaser
// checksums.txt body. Each line is "<hex>  <filename>"; the filename may carry
// a leading "*" (binary mode) or path components, so only the base name is
// compared. A missing entry is an error so verification never silently passes.
func checksumFor(checksums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filepath.Base(name) == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s in %s; refusing to install unverified binary", asset, checksumsAsset)
}

// extractBinary returns the bytes of the bharatcode binary inside archive,
// dispatching on the asset's extension: .zip uses archive/zip, everything else
// is treated as gzip-compressed tar. Both readers are from the standard library
// — the package takes no external archive dependency.
func extractBinary(archive []byte, asset string) ([]byte, error) {
	if strings.HasSuffix(asset, ".zip") {
		return extractFromZip(archive)
	}
	return extractFromTarGz(archive)
}

// wantedBinary reports whether a path inside an archive is the bharatcode
// executable we want to extract. GoReleaser places it at the archive root as
// "bharatcode" (or "bharatcode.exe" on Windows), but we match on the base name
// so a leading directory does not defeat the lookup.
func wantedBinary(name string) bool {
	base := filepath.Base(filepath.FromSlash(name))
	return base == binaryName || base == binaryName+".exe"
}

func extractFromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, fmt.Errorf("opening gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !wantedBinary(hdr.Name) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxArchiveBytes+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s from tar: %w", binaryName, err)
		}
		if int64(len(data)) > maxArchiveBytes {
			return nil, fmt.Errorf("%s in archive exceeds %d byte limit", binaryName, maxArchiveBytes)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%s not found in tar.gz archive", binaryName)
}

func extractFromZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(strings.NewReader(string(archive)), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("opening zip archive: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !wantedBinary(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s in zip: %w", f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxArchiveBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s from zip: %w", binaryName, err)
		}
		if int64(len(data)) > maxArchiveBytes {
			return nil, fmt.Errorf("%s in archive exceeds %d byte limit", binaryName, maxArchiveBytes)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%s not found in zip archive", binaryName)
}

// replaceExecutable installs binary at exePath atomically. It writes the new
// bytes to a temp file in the SAME directory as exePath — so the subsequent
// os.Rename is a same-filesystem move, which is atomic on POSIX — chmods it
// executable, then renames it over the target.
//
// Windows caveat: a running .exe is locked, and os.Rename cannot overwrite it.
// There we first move the running executable aside to a "<exe>.old" sidecar
// (renaming a running image is permitted) and then move the new binary into
// place. The .old file cannot be deleted while the old process runs, so it is
// left for the next launch / OS cleanup. The replacement still takes effect on
// the next start.
func replaceExecutable(exePath string, binary []byte) error {
	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".bharatcode-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file next to executable: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the successful rename.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("setting executable permission: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Move the locked, running exe aside so the destination name is free.
		old := exePath + ".old"
		_ = os.Remove(old) // a stale sidecar from a prior update would block the rename
		if err := os.Rename(exePath, old); err != nil {
			return fmt.Errorf("moving running executable aside: %w", err)
		}
		if err := os.Rename(tmpName, exePath); err != nil {
			// Try to restore the original so we don't leave the user without a binary.
			_ = os.Rename(old, exePath)
			return fmt.Errorf("moving new executable into place: %w", err)
		}
		cleanup = false
		return nil
	}

	if err := os.Rename(tmpName, exePath); err != nil {
		return fmt.Errorf("atomically replacing executable: %w", err)
	}
	cleanup = false
	return nil
}
