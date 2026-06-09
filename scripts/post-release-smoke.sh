#!/usr/bin/env bash
#
# post-release-smoke.sh — confirm a published release lines up across channels.
#
# After a tag is cut and the release pipeline finishes, the same version must be
# reachable from every place a user installs from. This script takes the release
# tag and asserts, for that one version, that:
#
#   1. The GitHub release exists and carries its expected assets (the per-OS/arch
#      archives plus checksums.txt) — `gh release view`.
#   2. npm publishes the matching version — `npm view bharatcode-cli version`.
#   3. Homebrew's tap installs and reports the matching version — `brew`.
#   4. The locally resolved `bharatcode` reports the matching version, so a fresh
#      `bharatcode version` shows what was just shipped.
#
# Usage:
#   scripts/post-release-smoke.sh <version>        # e.g. v0.2.7
#
# Versions are compared on their numeric core, so the GitHub tag form (v0.2.7)
# and the bare npm/brew form (0.2.7) match without the caller normalizing.
#
# NETWORK / TOOL DEPENDENCE. Every assert here reaches a remote registry through
# an optional tool (gh, npm, brew, and the installed binary). On a host missing
# the tool — or signed out, or offline — the corresponding check is SKIPPED with
# a clear note rather than failed, so `bash -n` and a dry run stay clean and the
# script never wedges a release that is otherwise fine. Set
# POSTREL_REQUIRE_ALL=1 to promote those skips to failures on a host that is
# expected to have every tool configured (e.g. the release runner).
set -uo pipefail

PROG="$(basename "${BASH_SOURCE[0]}")"

# Channel coordinates, kept beside the goreleaser/npm config they mirror.
GH_REPO="arbazkhan971/bharatcode"
NPM_PKG="bharatcode-cli"
BREW_FORMULA="arbazkhan971/homebrew-tap/bharatcode"
BIN_NAME="bharatcode"

PASS=0
FAIL=0
SKIP=0

pass() { printf '  [PASS] %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL=$((FAIL + 1)); }
# skip notes a check we could not run. Under POSTREL_REQUIRE_ALL=1 the inability
# to run a check is itself a failure.
skip() {
	if [[ "${POSTREL_REQUIRE_ALL:-0}" == "1" ]]; then
		fail "$1 (skip promoted to failure by POSTREL_REQUIRE_ALL=1)"
	else
		printf '  [SKIP] %s\n' "$1"
		SKIP=$((SKIP + 1))
	fi
}
section() { printf '\n== %s ==\n' "$1"; }

usage() {
	printf 'usage: %s <version>   e.g. %s v0.2.7\n' "${PROG}" "${PROG}" >&2
	exit 2
}

[[ $# -eq 1 ]] || usage
RAW_VERSION="$1"
[[ -n "${RAW_VERSION}" ]] || usage

# core strips a leading "v" so the GitHub tag form (v0.2.7) and the bare
# npm/brew form (0.2.7) compare equal.
core() { printf '%s' "${1#v}"; }

WANT_TAG="${RAW_VERSION}"            # GitHub release tag, as given.
WANT_CORE="$(core "${RAW_VERSION}")" # numeric core for cross-channel compares.

printf 'post-release smoke for %s (tag %s, core %s)\n' "${NPM_PKG}" "${WANT_TAG}" "${WANT_CORE}"

# --- 1. GitHub release + assets ---------------------------------------------
section "github release"
if ! command -v gh >/dev/null 2>&1; then
	skip "github: 'gh' not on PATH — cannot verify release ${WANT_TAG} or its assets"
else
	# A single view gives us both existence and the asset list. `--json assets`
	# keeps us off the human-formatted output, which changes between gh versions.
	assets_json="$(gh release view "${WANT_TAG}" --repo "${GH_REPO}" --json assets 2>/dev/null)"
	if [[ -z "${assets_json}" ]]; then
		skip "github: release ${WANT_TAG} not found or 'gh' not authenticated — cannot verify assets"
	else
		pass "github: release ${WANT_TAG} exists"
		# The assets we expect goreleaser to attach: every OS/arch archive plus
		# the checksums file. Names mirror .goreleaser.yaml's name_template.
		expected_assets=(
			"${BIN_NAME}_Darwin_x86_64.tar.gz"
			"${BIN_NAME}_Darwin_arm64.tar.gz"
			"${BIN_NAME}_Linux_x86_64.tar.gz"
			"${BIN_NAME}_Linux_arm64.tar.gz"
			"${BIN_NAME}_Windows_x86_64.zip"
			"${BIN_NAME}_Windows_arm64.zip"
			"checksums.txt"
		)
		# Pull the attached asset names. Prefer jq; fall back to a grep over the
		# JSON so a host without jq still gets a real check.
		if command -v jq >/dev/null 2>&1; then
			asset_names="$(printf '%s' "${assets_json}" | jq -r '.assets[].name')"
		else
			asset_names="$(printf '%s' "${assets_json}" |
				grep -oE '"name":"[^"]+"' | sed -E 's/"name":"([^"]+)"/\1/')"
		fi
		for want in "${expected_assets[@]}"; do
			if printf '%s\n' "${asset_names}" | grep -qxF "${want}"; then
				pass "github: asset present: ${want}"
			else
				fail "github: asset missing: ${want}"
			fi
		done
	fi
fi

# --- 2. npm version ----------------------------------------------------------
section "npm version"
if ! command -v npm >/dev/null 2>&1; then
	skip "npm: 'npm' not on PATH — cannot verify ${NPM_PKG} version"
else
	npm_version="$(npm view "${NPM_PKG}" version 2>/dev/null | tr -d '[:space:]')"
	if [[ -z "${npm_version}" ]]; then
		skip "npm: could not read ${NPM_PKG} version (offline or unpublished) — cannot compare"
	elif [[ "$(core "${npm_version}")" == "${WANT_CORE}" ]]; then
		pass "npm: ${NPM_PKG}@${npm_version} matches ${WANT_TAG}"
	else
		fail "npm: ${NPM_PKG}@${npm_version} != expected ${WANT_CORE}"
	fi
fi

# --- 3. Homebrew install + version ------------------------------------------
section "homebrew"
if ! command -v brew >/dev/null 2>&1; then
	skip "brew: 'brew' not on PATH — cannot install/verify ${BREW_FORMULA}"
else
	# Install (or upgrade to) the tapped formula so the reported version reflects
	# a real install, then read it back. Both steps are best-effort: a brew that
	# cannot reach the tap should skip, not fail the whole release smoke.
	if ! brew install "${BREW_FORMULA}" >/dev/null 2>&1; then
		# Already-installed or transient install issues shouldn't mask a correct
		# version; fall through and let the version read decide.
		printf '  note: brew install reported a non-zero status; reading installed version anyway\n'
	fi
	# `brew list --versions <name>` prints "name 0.2.7"; take the last field.
	brew_line="$(brew list --versions "${BIN_NAME}" 2>/dev/null)"
	brew_version="$(printf '%s' "${brew_line}" | awk '{print $NF}')"
	if [[ -z "${brew_version}" ]]; then
		skip "brew: ${BREW_FORMULA} not installed / tap unreachable — cannot compare version"
	elif [[ "$(core "${brew_version}")" == "${WANT_CORE}" ]]; then
		pass "brew: ${BIN_NAME} ${brew_version} matches ${WANT_TAG}"
	else
		fail "brew: ${BIN_NAME} ${brew_version} != expected ${WANT_CORE}"
	fi
fi

# --- 4. Installed binary version --------------------------------------------
section "installed binary"
# Resolve whatever `bharatcode` is on PATH — that is what a user just installed.
if ! command -v "${BIN_NAME}" >/dev/null 2>&1; then
	skip "binary: '${BIN_NAME}' not on PATH — cannot verify reported version"
else
	# `bharatcode version` prints: "bharatcode v0.2.7 (<commit>)". Take the
	# second field, which is the version token.
	bin_out="$("${BIN_NAME}" version 2>/dev/null)"
	bin_version="$(printf '%s' "${bin_out}" | awk '{print $2}')"
	if [[ -z "${bin_version}" ]]; then
		skip "binary: '${BIN_NAME} version' produced no version token — cannot compare"
	elif [[ "$(core "${bin_version}")" == "${WANT_CORE}" ]]; then
		pass "binary: ${BIN_NAME} reports ${bin_version}, matches ${WANT_TAG}"
	else
		fail "binary: ${BIN_NAME} reports ${bin_version} != expected ${WANT_CORE}"
	fi
fi

# --- Result -----------------------------------------------------------------
section "result"
printf '  %d passed, %d failed, %d skipped\n' "${PASS}" "${FAIL}" "${SKIP}"
if [[ ${FAIL} -gt 0 ]]; then
	exit 1
fi
exit 0
