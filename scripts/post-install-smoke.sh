#!/usr/bin/env bash
#
# post-install-smoke.sh — confirm a freshly installed bharatcode binary runs.
#
# Where post-release-smoke.sh checks that a published version lines up across the
# release channels (GitHub/npm/brew), this script checks the other half: that the
# binary a user just installed actually starts and answers its core, offline,
# no-config commands. It is the first thing to run after `npm i -g`, `brew
# install`, or dropping a release archive on PATH — a fast tripwire that catches a
# binary that won't launch, a missing subcommand, or a broken version stamp before
# the user ever opens the TUI.
#
# Every check here is OFFLINE and CONFIG-FREE: it exercises only commands that
# need no provider, no network, and no API key (version, help, about, the command
# tree). That keeps the smoke run deterministic on a bare machine and in CI.
#
# Usage:
#   scripts/post-install-smoke.sh [bharatcode-binary]
#
#   bharatcode-binary  Path to the binary to probe. Defaults to `bharatcode`
#                      resolved on PATH, so a global install is checked as-is.
#
# Exit status is 0 when every check passes, 1 when any check fails. A check that
# cannot run on this host (for example a tool that is genuinely optional) is
# SKIPPED rather than failed, unless POSTINSTALL_REQUIRE_ALL=1 promotes skips to
# failures on a host expected to have everything.
set -uo pipefail

PROG="$(basename "${BASH_SOURCE[0]}")"
BIN="${1:-bharatcode}"

PASS=0
FAIL=0
SKIP=0

pass() { printf '  [PASS] %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() {
	if [[ "${POSTINSTALL_REQUIRE_ALL:-0}" == "1" ]]; then
		fail "$1 (skip promoted to failure by POSTINSTALL_REQUIRE_ALL=1)"
	else
		printf '  [SKIP] %s\n' "$1"
		SKIP=$((SKIP + 1))
	fi
}
section() { printf '\n== %s ==\n' "$1"; }

usage() {
	printf 'usage: %s [bharatcode-binary]\n' "${PROG}" >&2
	exit 2
}

case "${1:-}" in
	-h | --help) usage ;;
esac

# run_check NAME EXPECT -- CMD...
# Runs CMD, captures combined output, and asserts exit 0 and that EXPECT (a
# case-insensitive substring; empty means "any output") appears in the output.
run_check() {
	local name="$1" expect="$2"
	shift 2
	[[ "$1" == "--" ]] && shift

	local out status
	out="$("$@" 2>&1)"
	status=$?

	if [[ ${status} -ne 0 ]]; then
		fail "${name}: exited ${status}"
		printf '         %s\n' "${out%%$'\n'*}"
		return
	fi
	if [[ -n "${expect}" ]] && ! grep -qi -- "${expect}" <<<"${out}"; then
		fail "${name}: output missing expected text '${expect}'"
		printf '         %s\n' "${out%%$'\n'*}"
		return
	fi
	pass "${name}"
}

section "Resolve binary"
if ! command -v "${BIN}" >/dev/null 2>&1 && [[ ! -x "${BIN}" ]]; then
	fail "binary not found or not executable: ${BIN}"
	printf '\n== Summary ==\n  PASS=%d FAIL=%d SKIP=%d\n' "${PASS}" "${FAIL}" "${SKIP}"
	exit 1
fi
pass "found binary: ${BIN}"

section "Core commands"
# version must print a non-empty version string and exit 0.
run_check "version" "" -- "${BIN}" version
# The version line should not be the unstamped dev fallback on a real install.
if ver_out="$("${BIN}" version 2>&1)" && [[ -n "${ver_out}" ]]; then
	if grep -qiE 'dev|unknown|none' <<<"${ver_out}"; then
		skip "version is unstamped (${ver_out%%$'\n'*}) — expected on a source build, not a release"
	else
		pass "version is stamped: ${ver_out%%$'\n'*}"
	fi
fi

# help must list the command tree and exit 0.
run_check "help" "Usage" -- "${BIN}" --help

# about must identify BharatCode and exit 0.
run_check "about" "BharatCode" -- "${BIN}" about

section "Command tree"
# A few core subcommands must be present in help so a stripped or wrong binary is
# caught early.
help_out="$("${BIN}" --help 2>&1)"
for sub in run version about; do
	if grep -qE "(^|[[:space:]])${sub}([[:space:]]|$)" <<<"${help_out}"; then
		pass "subcommand present: ${sub}"
	else
		fail "subcommand missing from help: ${sub}"
	fi
done

printf '\n== Summary ==\n  PASS=%d FAIL=%d SKIP=%d\n' "${PASS}" "${FAIL}" "${SKIP}"
if [[ ${FAIL} -gt 0 ]]; then
	exit 1
fi
exit 0
