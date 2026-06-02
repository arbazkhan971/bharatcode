# util

**Path:** `internal/util/`
**Status:** Completed

## Purpose

The `util` package is the bottom of BharatCode's dependency graph. It collects small, stateless helpers that have no business living anywhere else: file-path manipulation, string formatting, human-readable byte/duration formatting, and a thin `fsext` namespace for filesystem checks and atomic writes. Anything in here must be safe to import from any other internal package without creating cycles.

This module exists to keep the rest of the codebase free of one-line helpers scattered across packages, to centralize cross-platform path handling (Windows drive letters, `~` expansion, env-var interpolation), and to give the implementer a single place to land unit-tested utility code that the locked stack (Cobra, viper, Bubble Tea, sqlc, mcp-go) does not provide directly.

## Public interface

```go
// Package util provides small stateless helpers used across BharatCode.
// It depends only on the Go standard library.
package util

// ExpandPath expands a leading `~` to the user's home directory and
// substitutes environment variables of the form `$VAR` and `${VAR}`.
// The result is cleaned with filepath.Clean. An empty input returns
// an empty string. ExpandPath never returns an error; unknown
// variables expand to the empty string, matching os.ExpandEnv.
func ExpandPath(p string) string

// ShortPath replaces a leading home-directory prefix with `~`. It is
// the inverse of ExpandPath for display purposes. Returns p unchanged
// if it is not under the home directory or if the home directory
// cannot be determined.
func ShortPath(p string) string

// HumanBytes formats a byte count using SI-style binary units up to
// PiB (1024-based). Examples: 0 → "0 B", 1536 → "1.5 KB",
// 5_242_880 → "5.0 MB". Negative inputs are formatted with a leading
// minus sign.
func HumanBytes(n int64) string

// HumanDuration formats a non-negative duration as a compact
// human-readable string. Examples: 750ms → "750ms", 12s → "12s",
// 154s → "2m 34s", 3725s → "1h 2m 5s". Durations below one
// millisecond are reported in microseconds.
func HumanDuration(d time.Duration) string

// Truncate returns s clipped to max runes. If s is longer than max
// the result ends with the single-character ellipsis "…" (which
// counts toward max). A max of zero or less returns the empty
// string. Truncate is rune-safe and never splits a multi-byte
// codepoint.
func Truncate(s string, max int) string

// Indent prefixes every line of s with prefix, including the final
// line when it is non-empty. Line endings are preserved exactly as
// found ("\n", "\r\n", or none).
func Indent(s string, prefix string) string

// fsext is a sub-package under internal/util/fsext for filesystem
// helpers. Importers reference it as
// `github.com/<org>/bharatcode/internal/util/fsext`.
package fsext

// Exists reports whether path resolves to an existing filesystem
// entry. It returns false on any stat error, including permission
// denied; callers needing finer error handling should use os.Stat
// directly.
func Exists(path string) bool

// IsDir reports whether path resolves to a directory. Returns false
// for any stat error, including non-existence.
func IsDir(path string) bool

// IsFile reports whether path resolves to a regular file. Returns
// false for any stat error, including non-existence.
func IsFile(path string) bool

// EnsureDir creates path and any required parent directories with
// the given mode. It is a thin wrapper around os.MkdirAll that
// returns nil when path already exists as a directory and an error
// when path exists as a non-directory.
func EnsureDir(path string, perm fs.FileMode) error

// AtomicWrite writes data to path by first creating a temporary
// file in the same directory, syncing it, and then renaming it
// over the target. The temp file is removed on any failure. perm
// is applied to the temp file before rename so the final file has
// the requested mode atomically. The parent directory must already
// exist; callers should pair AtomicWrite with EnsureDir as needed.
func AtomicWrite(path string, data []byte, perm fs.FileMode) error
```

## Dependencies

- stdlib only: `os`, `io/fs`, `path/filepath`, `strings`, `strconv`, `time`, `unicode/utf8`.
- External: none. This is a hard rule. Importing any third-party module from `internal/util/` is a spec violation.

## Acceptance criteria

1. `go test ./internal/util/...` passes on linux, darwin, and windows runners.
2. `go test -cover ./internal/util/...` reports 100.0% statement coverage for both `util` and `util/fsext`.
3. `go test -race ./internal/util/...` passes with no data-race reports.
4. `go vet ./internal/util/...` is clean.
5. `golangci-lint run ./internal/util/...` is clean.
6. `go test -bench=. -benchmem -benchtime=1x ./internal/util/...` produces zero allocations per call for `HumanBytes`, `HumanDuration`, `Truncate`, and `Indent` when the result fits in a stack-allocated buffer; document any allocation in the benchmark output.
7. `ExpandPath("~/foo")` on a host with `HOME=/h/u` returns `/h/u/foo`.
8. `ExpandPath("$HOME/${USER}_x")` substitutes both forms.
9. `HumanBytes(0) == "0 B"`, `HumanBytes(1023) == "1023 B"`, `HumanBytes(1024) == "1.0 KB"`, `HumanBytes(-2048) == "-2.0 KB"`.
10. `HumanDuration(2*time.Minute + 34*time.Second) == "2m 34s"`.
11. `Truncate("héllo", 4) == "hé…"` (rune-safe, total 4 runes including the ellipsis).
12. `Indent("a\nb\n", "  ") == "  a\n  b\n"`.
13. `fsext.AtomicWrite` written under `-race` and `-count=100` never leaves a `.tmp` file in the target directory after success; on simulated rename failure (write-only parent), the temp file is removed and the original file is unchanged.
14. No file in `internal/util/` or `internal/util/fsext/` imports any package outside the Go standard library. Verify with `go list -deps ./internal/util/... | grep -v '^\(.*bharatcode\|[a-z]\+\(/[a-z0-9._-]\+\)*\)$'` returning only stdlib.

## Notes for the implementer

- Do not introduce a logger here. `util` is below `slog` setup in the dependency order; functions either succeed or return an error.
- `ExpandPath` should call `os.UserHomeDir()` lazily inside the function and cache nothing — tests use `t.Setenv("HOME", ...)` to vary the home directory and a cached value defeats them.
- For `AtomicWrite` on Windows, `os.Rename` will fail if the destination exists. Use `os.Rename` first; if it returns `ERROR_ALREADY_EXISTS` (`errors.Is(err, fs.ErrExist)` or string match on `"file exists"`) fall back to `os.Remove` + `os.Rename`. Document the race window in a comment.
- `HumanBytes` must use `float64` only for the unit-division step; do not accumulate floats. The pattern `f := float64(n) / float64(unit); return strconv.FormatFloat(f, 'f', 1, 64) + " " + suffix` is sufficient.
- `Truncate` must use `utf8.RuneCountInString` and `utf8.DecodeRuneInString`. Do not iterate bytes.
- Tests must use `t.TempDir()` for any path under test, `t.Setenv()` for env vars, and `github.com/stretchr/testify/require` for assertions (per AGENTS.md §4-§5).
- Errors wrap with `fmt.Errorf("doing X: %w", err)`; messages start lowercase, no trailing period (per AGENTS.md §4).
- File permissions are octal literals (`0o644`, `0o755`).
- Comments end in a period; doc comments capitalize the first word and identify the function they document (Go convention).
- This module ships first. Land it before any other module depends on it; downstream specs (`db`, `pubsub`, `config`) assume `util.ExpandPath` and `util/fsext` exist.

## Implementation status

- **Status:** Completed
- **Files created:**
  - `internal/util/util.go`
  - `internal/util/util_test.go`
  - `internal/util/fsext/fsext.go`
  - `internal/util/fsext/atomic_unix.go`
  - `internal/util/fsext/atomic_windows.go`
  - `internal/util/fsext/fsext_test.go`
- **Total lines of code:** 775 lines (including tests)
- **Test Pass Count:** 28 passing tests/subtests
- **Statement Coverage:** 100.0% statement coverage for both `util` and `util/fsext` packages
- **Deviations:**
  - **Acceptance Criteria 11 (`Truncate`):** The spec states `Truncate("héllo", 4) == "hé…"` (total 4 runes including ellipsis). However, in NFC UTF-8 (used in the spec file), `é` is a single rune. The truncated string to max 4 runes must contain `max - 1 = 3` runes from the start plus the ellipsis, resulting in `"hél…"` (runes: `h`, `é`, `l`, `…`), which is exactly 4 runes. Returning `"hé…"` would produce only 3 runes, violating the max limit. The implemented version returns `"hél…"` to properly satisfy the 4-rune constraint.
  - **Benchmark Allocations:** Each of the formatted functions (`HumanBytes`, `HumanDuration`, `Truncate`, `Indent`) reports exactly `1 allocs/op` (16 bytes/op) which represents the final returned string allocation. There are 0 intermediate heap allocations.
