#!/usr/bin/env bash
#
# release-preflight.sh — the one gate every BharatCode release must clear.
#
# Tagging a release used to depend on remembering which checks to run and in
# which order. This script encodes that order once so a release never ships on
# memory or ad-hoc steps: it runs every pre-tag check, in sequence, and fails
# loudly at the first one that breaks. A green run here is the contract that the
# tree is clean, the binary builds and behaves, and the npm/version metadata
# lines up with the latest tag.
#
# Usage:
#   scripts/release-preflight.sh
#
# Takes no required arguments — it builds its own temp binary so a stale or
# missing ./bharatcode cannot mask a problem (or hide one). Set
# PREFLIGHT_BIN=/path/to/bharatcode to reuse an existing build instead.
#
# Checks, in order:
#   1. Clean tracked tree — no uncommitted changes to tracked files (T11).
#   2. No stray untracked files outside a known artifact allowlist (T11).
#   3. go test ./... — the full offline unit suite is green.
#   4. go build ./... — every package compiles.
#   5. Temp binary build — the release binary links and runs `version`.
#   6. Codex-parity eval subset — the offline parity suite passes (no network).
#   7. UX smoke — scripts/ux-smoke.sh against the temp binary (offline checks).
#   8. TUI acceptance (optional) — scripts/tui-acceptance.sh when
#      PREFLIGHT_TUI=1; structural by default, content-graded only with a
#      provider. Off by default so the gate stays fast and offline.
#   9. npm shim check — node --check npm/bin/bharatcode.js parses clean.
#  10. Version/tag consistency — npm/package.json version matches the latest
#      git tag (vX.Y.Z).
#
# Opt-in / live extras (off by default, so the gate is offline and fast):
#   PREFLIGHT_TUI=1                 also run the PTY/TUI acceptance suite.
#   UX_SMOKE_ALSO_LIVE=1           let ux-smoke run its live-provider checks too.
#   PREFLIGHT_ALLOW_UNTRACKED="a b" extra space-separated untracked paths to
#                                  tolerate (appended to the built-in allowlist).
#
# Self-test (the dirty-worktree guard, checks 1 and 2): the guard reads only the
# working tree, so you can exercise it without a release. From a clean tree:
#   touch RANDOM_STRAY_FILE && scripts/release-preflight.sh   # fails check 2
#   rm RANDOM_STRAY_FILE
#   echo x >> go.mod && scripts/release-preflight.sh          # fails check 1
#   git checkout go.mod
# Each run aborts at the guard before building anything, and prints the exact
# offending paths, so the guard is verifiable in seconds.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}" || exit 2

# Temp binary build location, unless the caller supplied one to reuse.
PREFLIGHT_BIN="${PREFLIGHT_BIN:-}"
BUILT_BIN=""

PASS=0
FAIL=0

pass() { printf '  [PASS] %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL=$((FAIL + 1)); }
section() { printf '\n== %s ==\n' "$1"; }

# die prints a final failure summary and exits non-zero. Used for the guard and
# any check whose failure makes later checks meaningless (e.g. a broken build).
die() {
	printf '\n== preflight aborted ==\n'
	printf '  %s\n' "$1" >&2
	exit 1
}

# cleanup removes the temp binary we built (if any). Registered once up front; a
# no-op when the caller reused PREFLIGHT_BIN or the build never ran.
cleanup() {
	[[ -n "${BUILT_BIN}" && -f "${BUILT_BIN}" ]] && rm -f "${BUILT_BIN}"
}
trap cleanup EXIT

printf 'BharatCode release preflight\n'
printf 'repo: %s\n' "${REPO_ROOT}"

# --- Check 1+2: dirty-worktree guard (T11) ----------------------------------
# A release must not silently bundle uncommitted edits or unknown stray files.
# Run this FIRST and abort hard: every later check builds artifacts, so we want
# to fail before muddying the very tree we are inspecting.
#
# Two halves:
#   1. Tracked files must be unchanged — `git diff --quiet` (working tree) and
#      `git diff --cached --quiet` (index) both clean.
#   2. Untracked files must all fall inside a known allowlist. Anything else is
#      an unknown artifact that could leak into a tag or be silently lost.
#
# The allowlist is intentionally narrow: build outputs, agent/editor state, and
# the local preview/screenshot captures already ignored by .gitignore (untracked
# ignored files are not reported by --exclude-standard, so this list only has to
# cover artifacts that are tracked-by-pattern-but-not-yet-committed and the few
# top-level captures contributors routinely leave around). Extend it for a
# one-off run via PREFLIGHT_ALLOW_UNTRACKED.
section "dirty-worktree guard (clean tracked tree + no stray files)"

if ! git diff --quiet 2>/dev/null; then
	printf '  [FAIL] tracked files have uncommitted changes:\n'
	git --no-pager diff --name-only | sed 's/^/    M /'
	die "commit, stash, or revert the changes above before releasing"
fi
if ! git diff --cached --quiet 2>/dev/null; then
	printf '  [FAIL] staged (but uncommitted) changes present:\n'
	git --no-pager diff --cached --name-only | sed 's/^/    S /'
	die "commit or unstage the changes above before releasing"
fi
pass "tracked tree is clean (no uncommitted or staged changes)"

# Built-in allowlist of untracked path prefixes/globs that are safe to ignore.
# These are local build outputs, agent state, and preview captures — never part
# of a release artifact. Patterns are matched with bash's [[ == ]] globbing.
ALLOW_PATTERNS=(
	'bharatcode'        # stray local build of the binary at the repo root
	'bharatcode.exe'
	'bharatcode-test'
	'bin/*'             # make build / GoReleaser staging
	'dist/*'            # GoReleaser output
	'.bharatcode/*'     # per-run agent state / eval artifacts
	'.claude/*'         # agent session state
	'.playwright-mcp/*' # local browser MCP state
	'*.png'             # local TUI / preview screenshots
	'*.jpeg'            # local site captures
	'*.out'             # coverage / test artifacts
)
# Fold any caller-supplied extra patterns in.
if [[ -n "${PREFLIGHT_ALLOW_UNTRACKED:-}" ]]; then
	# shellcheck disable=SC2206  # intentional word-splitting of the env list
	ALLOW_PATTERNS+=(${PREFLIGHT_ALLOW_UNTRACKED})
fi

allowed_path() {
	local path="$1" pat
	for pat in "${ALLOW_PATTERNS[@]}"; do
		# shellcheck disable=SC2053  # RHS is a glob pattern, not a literal
		if [[ "${path}" == ${pat} ]]; then
			return 0
		fi
	done
	return 1
}

# --exclude-standard already drops .gitignore'd files, so anything listed here
# is genuinely unknown to git. We still filter through the allowlist to tolerate
# the handful of tracked-by-pattern build outputs that can appear before a
# commit.
stray=()
while IFS= read -r path; do
	[[ -z "${path}" ]] && continue
	if ! allowed_path "${path}"; then
		stray+=("${path}")
	fi
done < <(git ls-files --others --exclude-standard)

if [[ ${#stray[@]} -gt 0 ]]; then
	printf '  [FAIL] unknown untracked files present (not in the allowlist):\n'
	printf '    ? %s\n' "${stray[@]}"
	die "remove, ignore (.gitignore), or commit the files above — a release must not include or ignore unknown artifacts"
fi
pass "no stray untracked files outside the allowlist"

# --- Check 3: go test ./... -------------------------------------------------
# The default suite is fast and offline (PTY/live tests are opt-in behind their
# own env vars), so a release always runs it.
section "go test ./..."
if go test ./... >/dev/null 2>&1; then
	pass "go test ./... is green"
else
	# Re-run without swallowing output so the failure is actionable.
	printf '  [FAIL] go test ./... failed; re-running to show output:\n'
	go test ./... 2>&1 | tail -n 40 | sed 's/^/    /'
	die "fix the failing tests above before releasing"
fi

# --- Check 4: go build ./... ------------------------------------------------
section "go build ./..."
if go build ./... >/dev/null 2>&1; then
	pass "go build ./... compiles every package"
else
	printf '  [FAIL] go build ./... failed; re-running to show output:\n'
	go build ./... 2>&1 | tail -n 40 | sed 's/^/    /'
	die "fix the build errors above before releasing"
fi

# --- Check 5: temp release binary builds and runs ---------------------------
# Build a throwaway binary the rest of the checks share. Using a fresh build
# (rather than a possibly-stale ./bharatcode) guarantees the smoke and eval
# checks grade the code that is about to ship.
section "build temp release binary"
if [[ -n "${PREFLIGHT_BIN}" ]]; then
	if [[ ! -x "${PREFLIGHT_BIN}" ]]; then
		die "PREFLIGHT_BIN is set but not executable: ${PREFLIGHT_BIN}"
	fi
	BIN="${PREFLIGHT_BIN}"
	pass "reusing caller-supplied binary: ${BIN}"
else
	BUILT_BIN="$(mktemp -t bharatcode-preflight.XXXXXX)"
	if go build -o "${BUILT_BIN}" . >/dev/null 2>&1 && "${BUILT_BIN}" version >/dev/null 2>&1; then
		BIN="${BUILT_BIN}"
		pass "built and ran temp binary: $("${BIN}" version 2>/dev/null | head -n1)"
	else
		printf '  [FAIL] could not build/run a release binary; output:\n'
		go build -o "${BUILT_BIN}" . 2>&1 | tail -n 20 | sed 's/^/    /'
		die "fix the binary build above before releasing"
	fi
fi

# --- Check 6: codex-parity eval subset (offline) ----------------------------
# Run the offline parity suite with the deterministic stub provider (no network,
# no keys). A regression that lets the agent claim "done" without verifying its
# work shows up here.
section "codex-parity eval subset (offline)"
if "${BIN}" eval --suite codex-parity >/dev/null 2>&1; then
	pass "codex-parity eval suite passed offline"
else
	printf '  [FAIL] codex-parity eval suite failed; output:\n'
	"${BIN}" eval --suite codex-parity 2>&1 | tail -n 30 | sed 's/^/    /'
	fail "codex-parity eval regressed (run: ${BIN} eval --suite codex-parity)"
fi

# --- Check 7: UX smoke (offline checks) -------------------------------------
# ux-smoke.sh runs the deterministic, offline UX guards (redraw flood, doctor
# ChatGPT line, clean exits) plus a local fake-provider run; its live checks
# stay opt-in via UX_SMOKE_ALSO_LIVE. A non-zero exit means a UX regression.
section "UX smoke (scripts/ux-smoke.sh)"
if [[ -x "${SCRIPT_DIR}/ux-smoke.sh" ]]; then
	if "${SCRIPT_DIR}/ux-smoke.sh" "${BIN}"; then
		pass "ux-smoke.sh passed"
	else
		fail "ux-smoke.sh reported failures (see output above)"
	fi
else
	fail "scripts/ux-smoke.sh missing or not executable"
fi

# --- Check 8: TUI acceptance (optional) -------------------------------------
# The PTY acceptance suite is slow and only content-grades with a live provider,
# so it is off by default; PREFLIGHT_TUI=1 opts in. Structurally it still runs
# the five cases and saves transcripts even offline.
section "TUI acceptance (optional)"
if [[ "${PREFLIGHT_TUI:-0}" == "1" ]]; then
	if [[ -x "${SCRIPT_DIR}/tui-acceptance.sh" ]]; then
		if "${SCRIPT_DIR}/tui-acceptance.sh" "${BIN}"; then
			pass "tui-acceptance.sh passed"
		else
			fail "tui-acceptance.sh reported failures (see output above)"
		fi
	else
		fail "PREFLIGHT_TUI=1 but scripts/tui-acceptance.sh missing or not executable"
	fi
else
	printf '  [SKIP] TUI acceptance off (set PREFLIGHT_TUI=1 to run the PTY suite)\n'
fi

# --- Check 9: npm shim parses -----------------------------------------------
# The published npm launcher must at least parse. node --check is a syntax-only
# pass that needs no install, so it runs in CI without network.
section "npm shim check (node --check npm/bin/bharatcode.js)"
SHIM="${REPO_ROOT}/npm/bin/bharatcode.js"
if ! command -v node >/dev/null 2>&1; then
	fail "node not found — cannot syntax-check the npm shim"
elif [[ ! -f "${SHIM}" ]]; then
	fail "npm shim missing: ${SHIM}"
elif node --check "${SHIM}" 2>/dev/null; then
	pass "npm shim parses clean"
else
	printf '  [FAIL] npm shim has a syntax error:\n'
	node --check "${SHIM}" 2>&1 | sed 's/^/    /'
	fail "fix the npm shim syntax error above"
fi

# --- Check 10: version/tag consistency --------------------------------------
# The npm package version must match the latest git tag (vX.Y.Z). A mismatch
# means the npm metadata and the release tag disagree about what is shipping.
section "version/tag consistency (npm/package.json vs latest tag)"
PKG_JSON="${REPO_ROOT}/npm/package.json"
LATEST_TAG="$(git tag --sort=-v:refname 2>/dev/null | head -n1)"
if [[ ! -f "${PKG_JSON}" ]]; then
	fail "npm/package.json missing: ${PKG_JSON}"
elif [[ -z "${LATEST_TAG}" ]]; then
	fail "no git tags found — cannot check version/tag consistency"
else
	# Extract the npm "version": "x.y.z" without assuming jq is installed.
	PKG_VERSION="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${PKG_JSON}" | head -n1)"
	TAG_VERSION="${LATEST_TAG#v}" # strip a leading "v" from vX.Y.Z
	if [[ -z "${PKG_VERSION}" ]]; then
		fail "could not read \"version\" from ${PKG_JSON}"
	elif [[ "${PKG_VERSION}" == "${TAG_VERSION}" ]]; then
		pass "npm version ${PKG_VERSION} matches latest tag ${LATEST_TAG}"
	else
		fail "npm version ${PKG_VERSION} != latest tag ${LATEST_TAG} (bump npm/package.json or tag to match)"
	fi
fi

# --- Result -----------------------------------------------------------------
section "result"
printf '  %d passed, %d failed\n' "${PASS}" "${FAIL}"
if [[ ${FAIL} -gt 0 ]]; then
	printf '  release preflight FAILED — do not tag.\n'
	exit 1
fi
printf '  release preflight PASSED — safe to tag.\n'
exit 0
