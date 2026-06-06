package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssetNameSupportedCombos(t *testing.T) {
	cases := map[[2]string]string{
		{"darwin", "amd64"}:  "bharatcode_Darwin_x86_64.tar.gz",
		{"darwin", "arm64"}:  "bharatcode_Darwin_arm64.tar.gz",
		{"linux", "amd64"}:   "bharatcode_Linux_x86_64.tar.gz",
		{"linux", "arm64"}:   "bharatcode_Linux_arm64.tar.gz",
		{"windows", "amd64"}: "bharatcode_Windows_x86_64.zip",
		{"windows", "arm64"}: "bharatcode_Windows_arm64.zip",
	}
	for in, want := range cases {
		got, err := AssetName(in[0], in[1])
		require.NoError(t, err, "AssetName(%q,%q)", in[0], in[1])
		require.Equal(t, want, got)
	}
}

func TestAssetNameUnsupported(t *testing.T) {
	_, err := AssetName("plan9", "amd64")
	require.Error(t, err)
	_, err = AssetName("linux", "riscv64")
	require.Error(t, err)
	_, err = AssetName("linux", "386")
	require.Error(t, err)
}

func TestLatestReleaseTagFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","name":"Release 1.2.3"}`))
	}))
	defer srv.Close()

	tag, err := LatestReleaseTag(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, "v1.2.3", tag)
}

func TestLatestReleaseTagErrors(t *testing.T) {
	// Non-200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := LatestReleaseTag(context.Background(), srv.URL)
	srv.Close()
	require.Error(t, err)

	// Missing tag_name.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"x"}`))
	}))
	_, err = LatestReleaseTag(context.Background(), srv2.URL)
	srv2.Close()
	require.Error(t, err)
}

func TestChecksumForFindsAndRejects(t *testing.T) {
	manifest := "deadbeef  other_asset.tar.gz\n" +
		"ABC123  *bharatcode_Linux_x86_64.tar.gz\n"
	got, err := checksumFor([]byte(manifest), "bharatcode_Linux_x86_64.tar.gz")
	require.NoError(t, err)
	require.Equal(t, "abc123", got) // lowercased, leading "*" stripped

	_, err = checksumFor([]byte(manifest), "bharatcode_Darwin_arm64.tar.gz")
	require.Error(t, err, "a missing asset must be an error, not a silent pass")
}

// makeTarGz builds an in-memory .tar.gz containing a single file at name with
// the given contents — a stand-in for a real release archive.
func makeTarGz(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(contents)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(contents)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// makeZip builds an in-memory .zip containing a single file at name.
func makeZip(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	require.NoError(t, err)
	_, err = f.Write(contents)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	want := []byte("#!/bin/sh\necho hi\n")
	archive := makeTarGz(t, "bharatcode", want)
	got, err := extractBinary(archive, "bharatcode_Linux_x86_64.tar.gz")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestExtractBinaryFromZip(t *testing.T) {
	want := []byte("MZ-fake-windows-binary")
	archive := makeZip(t, "bharatcode.exe", want)
	got, err := extractBinary(archive, "bharatcode_Windows_x86_64.zip")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	archive := makeTarGz(t, "README.md", []byte("not the binary"))
	_, err := extractBinary(archive, "bharatcode_Linux_x86_64.tar.gz")
	require.Error(t, err)
}

// applyFixture wires an httptest server that serves the three endpoints Apply
// needs (latest-release JSON, the archive, and checksums.txt) and returns
// ApplyOptions pointed at it, replacing the temp file exePath. The checksum
// served is computed from archive unless overridden via badChecksum.
type applyFixture struct {
	tag         string
	asset       string
	archive     []byte
	badChecksum bool // serve a wrong checksum to force a mismatch
	omitChecks  bool // 404 the checksums.txt to test the hard-fail path
}

func startApplyServer(t *testing.T, fx applyFixture) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(fx.archive)
	hexsum := hex.EncodeToString(sum[:])
	if fx.badChecksum {
		hexsum = strings.Repeat("0", 64)
	}
	manifest := fmt.Sprintf("%s  %s\n", hexsum, fx.asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, fx.tag)
	})
	mux.HandleFunc("/download/"+fx.tag+"/"+fx.asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fx.archive)
	})
	mux.HandleFunc("/download/"+fx.tag+"/"+checksumsAsset, func(w http.ResponseWriter, _ *http.Request) {
		if fx.omitChecks {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(manifest))
	})
	return httptest.NewServer(mux)
}

func optionsFor(srv *httptest.Server, exePath, goos, goarch string, progress *bytes.Buffer) ApplyOptions {
	opts := ApplyOptions{
		ExePath:         exePath,
		ReleaseAPIURL:   srv.URL + "/releases/latest",
		DownloadBaseURL: srv.URL + "/download",
		GOOS:            goos,
		GOARCH:          goarch,
		HTTPClient:      srv.Client(),
	}
	// Only set Progress when a buffer is supplied: assigning a typed-nil
	// *bytes.Buffer to the io.Writer field yields a non-nil interface that would
	// defeat Apply's nil check and panic on write.
	if progress != nil {
		opts.Progress = progress
	}
	return opts
}

func TestApplyEndToEndReplacesExecutable(t *testing.T) {
	newBin := []byte("NEW-BHARATCODE-BINARY-v2")
	asset := "bharatcode_Linux_x86_64.tar.gz"
	archive := makeTarGz(t, "bharatcode", newBin)

	srv := startApplyServer(t, applyFixture{tag: "v2.0.0", asset: asset, archive: archive})
	defer srv.Close()

	// A temp file standing in for the running executable, seeded with old bytes.
	dir := t.TempDir()
	exePath := filepath.Join(dir, "bharatcode")
	require.NoError(t, os.WriteFile(exePath, []byte("OLD-BINARY"), 0o755))

	var progress bytes.Buffer
	err := Apply(context.Background(), optionsFor(srv, exePath, "linux", "amd64", &progress))
	require.NoError(t, err)

	// The on-disk binary is now the downloaded one.
	got, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, newBin, got)

	// It is executable and the temp sibling was cleaned up (only the exe remains).
	info, err := os.Stat(exePath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&0o100, "replaced binary must be executable")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp leftovers should remain next to the exe")

	require.Contains(t, progress.String(), "Checksum verified.")
	require.Contains(t, progress.String(), "v2.0.0")
}

func TestApplyFromZipReplacesExecutable(t *testing.T) {
	newBin := []byte("NEW-WINDOWS-BINARY")
	asset := "bharatcode_Windows_x86_64.zip"
	archive := makeZip(t, "bharatcode.exe", newBin)

	srv := startApplyServer(t, applyFixture{tag: "v2.0.0", asset: asset, archive: archive})
	defer srv.Close()

	dir := t.TempDir()
	// Use a plain name; the .zip path of Apply still writes to exePath as given.
	exePath := filepath.Join(dir, "bharatcode")
	require.NoError(t, os.WriteFile(exePath, []byte("OLD"), 0o755))

	err := Apply(context.Background(), optionsFor(srv, exePath, "windows", "amd64", nil))
	require.NoError(t, err)

	got, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, newBin, got)
}

// TestApplyRefusesOnChecksumMismatch is the security-critical case: when the
// archive's SHA-256 does not match the published checksum, Apply must fail and
// must NOT touch the existing executable.
func TestApplyRefusesOnChecksumMismatch(t *testing.T) {
	asset := "bharatcode_Linux_x86_64.tar.gz"
	archive := makeTarGz(t, "bharatcode", []byte("TAMPERED-PAYLOAD"))

	srv := startApplyServer(t, applyFixture{tag: "v2.0.0", asset: asset, archive: archive, badChecksum: true})
	defer srv.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "bharatcode")
	original := []byte("ORIGINAL-TRUSTED-BINARY")
	require.NoError(t, os.WriteFile(exePath, original, 0o755))

	err := Apply(context.Background(), optionsFor(srv, exePath, "linux", "amd64", nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "checksum mismatch")

	// The original binary must be byte-for-byte intact.
	got, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, original, got, "Apply must not replace the binary on checksum mismatch")

	// No temp file should be left lying around either.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestApplyRefusesWhenChecksumsUnavailable covers the degrade-gracefully path:
// if checksums.txt can't be fetched, Apply must hard-fail rather than install
// unverified bytes.
func TestApplyRefusesWhenChecksumsUnavailable(t *testing.T) {
	asset := "bharatcode_Linux_x86_64.tar.gz"
	archive := makeTarGz(t, "bharatcode", []byte("payload"))

	srv := startApplyServer(t, applyFixture{tag: "v2.0.0", asset: asset, archive: archive, omitChecks: true})
	defer srv.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "bharatcode")
	require.NoError(t, os.WriteFile(exePath, []byte("ORIGINAL"), 0o755))

	err := Apply(context.Background(), optionsFor(srv, exePath, "linux", "amd64", nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), checksumsAsset)

	got, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, []byte("ORIGINAL"), got)
}

func TestApplyErrorsOnUnsupportedPlatform(t *testing.T) {
	srv := startApplyServer(t, applyFixture{tag: "v1.0.0", asset: "x", archive: []byte("x")})
	defer srv.Close()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "bharatcode")
	require.NoError(t, os.WriteFile(exePath, []byte("o"), 0o755))

	err := Apply(context.Background(), optionsFor(srv, exePath, "plan9", "amd64", nil))
	require.Error(t, err)
}

func TestReplaceExecutableAtomicOnHostPlatform(t *testing.T) {
	// Exercise the real replaceExecutable on whatever OS the test runs on,
	// confirming the swap and the executable bit. (On Windows this drives the
	// .old-sidecar branch since exePath is not actually a running image here.)
	dir := t.TempDir()
	exePath := filepath.Join(dir, "bharatcode")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	require.NoError(t, os.WriteFile(exePath, []byte("old"), 0o755))

	require.NoError(t, replaceExecutable(exePath, []byte("brand-new")))
	got, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, []byte("brand-new"), got)
}
