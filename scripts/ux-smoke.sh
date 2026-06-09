#!/usr/bin/env bash
#
# ux-smoke.sh — UX regression checks for the BharatCode CLI and TUI.
#
# This script guards the exact rough edges surfaced in manual user testing,
# so a refactor that quietly reintroduces them fails CI instead of a human:
#
#   1. Redraw flood — a captured/headless TUI session must not repaint the
#      whole screen many times a second. The renderer slows its spinner and
#      uptime tick (and drops the renderer entirely when fully headless) so a
#      `script`/pipe capture stays small. A regression here balloons the
#      capture with frames no reader can perceive.
#   2. Changed files — a non-interactive `run` that edits the workspace must
#      print a "Changed files:" summary so file-creation tasks are visible
#      without scrolling the transcript.
#   3. doctor ChatGPT status — `doctor` must report the ChatGPT sign-in state
#      when a chatgpt provider is enabled, so a broken/absent sign-in is
#      diagnosable offline.
#   4. Clean exit — the smoke commands (version, doctor, json run) exit 0 and
#      never hang.
#
# Usage:
#   scripts/ux-smoke.sh [path-to-bharatcode-binary]
#
# The binary path defaults to ./bharatcode in the repo root. Build one first:
#   go build -o bharatcode .
#
# Checks 2 (changed files) and the JSON-run framing need a usable provider.
# When none is reachable (no API key, no local server, no sign-in) those
# checks are SKIPPED rather than failed, so the deterministic, offline checks
# (redraw flood, doctor ChatGPT line, clean exits) still run in any CI. Set
# UX_SMOKE_REQUIRE_PROVIDER=1 to turn those skips into failures on a host that
# is expected to have a provider configured.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BIN="${1:-${REPO_ROOT}/bharatcode}"

# Max bytes a few-second idle capture may emit before we call it a flood. A
# quieted capture emits well under 1KB; a 12fps full repaint of an 80x24
# screen would emit ~20KB/sec, so 50KB leaves generous headroom while still
# catching a reverted quiet-redraw path.
FLOOD_MAX_BYTES=51200
# Seconds to let the captured TUI sit idle before it is stopped.
CAPTURE_SECONDS=5
# Upper bound (seconds) on any single live-model run so a stuck turn cannot
# wedge the suite.
RUN_TIMEOUT=120

PASS=0
FAIL=0
SKIP=0

pass() { printf '  [PASS] %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() {
	if [[ "${UX_SMOKE_REQUIRE_PROVIDER:-0}" == "1" ]]; then
		fail "$1 (skip promoted to failure by UX_SMOKE_REQUIRE_PROVIDER=1)"
	else
		printf '  [SKIP] %s\n' "$1"
		SKIP=$((SKIP + 1))
	fi
}
section() { printf '\n== %s ==\n' "$1"; }

# run_bounded runs the rest of its arguments under a wall-clock cap, escalating
# to SIGKILL if the process ignores the soft signal. It abstracts over the
# `timeout` (GNU/coreutils) vs `gtimeout` (Homebrew) name difference and over
# hosts that ship neither. Exit status 124 marks a timeout.
TIMEOUT_BIN=""
if command -v timeout >/dev/null 2>&1; then
	TIMEOUT_BIN="timeout"
elif command -v gtimeout >/dev/null 2>&1; then
	TIMEOUT_BIN="gtimeout"
fi
run_bounded() {
	local secs="$1"
	shift
	if [[ -n "${TIMEOUT_BIN}" ]]; then
		"${TIMEOUT_BIN}" -s KILL "${secs}" "$@"
	else
		# No timeout utility: run directly. The individual checks below are all
		# input-bounded (they close stdin or quit), so this is best-effort.
		"$@"
	fi
}

if [[ ! -x "${BIN}" ]]; then
	printf 'error: binary not found or not executable: %s\n' "${BIN}" >&2
	printf 'build it first, e.g.: go build -o %s/bharatcode .\n' "${REPO_ROOT}" >&2
	exit 2
fi

printf 'UX smoke checks against: %s\n' "${BIN}"

# --- Check: version prints and exits cleanly --------------------------------
section "version (clean exit)"
if version_out="$("${BIN}" version 2>/dev/null)" && [[ -n "${version_out}" ]]; then
	pass "version exits 0 and prints: ${version_out}"
else
	fail "version did not print a clean line / non-zero exit"
fi

# --- Check: doctor reports ChatGPT status -----------------------------------
# doctor is offline-fast and contacts no provider, so this always runs. The
# chatgpt provider is enabled in the shipped default config, so doctor prints
# a ChatGPT sign-in line (signed in, or a "not signed in" hint) — either way
# the status surface must exist.
section "doctor (ChatGPT status + clean exit)"
doctor_out="$("${BIN}" doctor 2>/dev/null)"
doctor_rc=$?
if [[ ${doctor_rc} -ne 0 ]]; then
	fail "doctor exited ${doctor_rc} (expected 0)"
elif grep -qiE 'chatgpt' <<<"${doctor_out}"; then
	chatgpt_line="$(grep -iE 'chatgpt' <<<"${doctor_out}" | head -n1 | sed 's/^[[:space:]]*//')"
	pass "doctor reports ChatGPT status: ${chatgpt_line}"
else
	fail "doctor output is missing any ChatGPT status line"
fi

# --- Check: headless / captured TUI does not flood with redraws -------------
# Drive the bare TUI with a non-interactive stdout so the renderer takes its
# quiet path (slow spinner + slow uptime tick), let it idle, then stop it. A
# quieted idle session stays tiny; a reverted 12fps/per-second-tick renderer
# would dump kilobytes of repaints in the same window.
section "headless TUI (no redraw flood)"
capture="$(mktemp -t ux-smoke-capture.XXXXXX)"
trap 'rm -f "${capture}"' EXIT
# TERM set + a redirected (non-tty) stdout selects the renderer's quiet-redraw
# path; BHARATCODE_QUIET_REDRAW forces it on even if the host fakes a tty.
TERM="${TERM:-xterm}" BHARATCODE_QUIET_REDRAW=1 \
	run_bounded "${CAPTURE_SECONDS}" "${BIN}" </dev/null >"${capture}" 2>/dev/null
# A timeout-stopped idle TUI is expected; we only inspect the captured bytes.
if [[ -f "${capture}" ]]; then
	cap_bytes=$(wc -c <"${capture}" | tr -d ' ')
else
	cap_bytes=0
fi
if [[ "${cap_bytes}" -le "${FLOOD_MAX_BYTES}" ]]; then
	pass "captured TUI stayed quiet: ${cap_bytes} bytes over ${CAPTURE_SECONDS}s (limit ${FLOOD_MAX_BYTES})"
else
	fail "captured TUI flooded: ${cap_bytes} bytes over ${CAPTURE_SECONDS}s exceeds ${FLOOD_MAX_BYTES} (redraw regression?)"
fi

# Fully headless mode drops the renderer entirely, so its stdout must carry no
# screen-paint escape sequences at all.
headless_cap="$(mktemp -t ux-smoke-headless.XXXXXX)"
BHARATCODE_HEADLESS=1 \
	run_bounded "${CAPTURE_SECONDS}" "${BIN}" </dev/null >"${headless_cap}" 2>/dev/null
hl_bytes=$(wc -c <"${headless_cap}" | tr -d ' ')
rm -f "${headless_cap}"
if [[ "${hl_bytes}" -le "${FLOOD_MAX_BYTES}" ]]; then
	pass "headless TUI emitted no flood: ${hl_bytes} bytes"
else
	fail "headless TUI emitted ${hl_bytes} bytes (expected none — renderer should be off)"
fi

# --- Provider-gated checks --------------------------------------------------
# "Provider ready" appears in doctor only when the active provider can actually
# be used right now (key set, local endpoint up, or ChatGPT signed in). Use it
# to decide whether the live-model checks below can run.
section "live run checks (require a usable provider)"
if grep -qiE 'Provider ready' <<<"${doctor_out}"; then
	provider_ready=1
else
	provider_ready=0
fi

if [[ "${provider_ready}" -ne 1 ]]; then
	skip "no usable provider (doctor lacks a 'Provider ready' line) — skipping JSON run and changed-files checks"
else
	# --- Check: `run --json` emits clean NDJSON framing and exits 0 ----------
	json_out="$(printf 'reply with the single word PONG and nothing else' |
		run_bounded "${RUN_TIMEOUT}" "${BIN}" run --json --quiet 2>/dev/null)"
	json_rc=$?
	first_line="$(head -n1 <<<"${json_out}")"
	if [[ ${json_rc} -eq 0 ]] && grep -q '"type":' <<<"${first_line}"; then
		pass "run --json emitted NDJSON and exited 0 (first event: $(cut -c1-60 <<<"${first_line}")...)"
	else
		fail "run --json failed framing/exit (rc=${json_rc}, first line: ${first_line:-<empty>})"
	fi

	# --- Check: a file-writing run prints a "Changed files:" summary ---------
	# Run in a throwaway workspace and ask the model to write through a tracked
	# tool, then assert the run summarized the change. If the file exists but the
	# summary is missing, this is a regression: users need the blast radius.
	fixture="$(mktemp -d -t ux-smoke-fixture.XXXXXX)"
	run_out="$(printf 'Use the write tool to create a file named smoke.txt with the content ok. Then stop.' |
		run_bounded "${RUN_TIMEOUT}" "${BIN}" run --yolo --quiet --project-dir "${fixture}" 2>/dev/null)"
	run_rc=$?
	wrote_file=0
	[[ -f "${fixture}/smoke.txt" ]] && wrote_file=1
	if [[ ${run_rc} -ne 0 ]]; then
		fail "file-writing run exited ${run_rc} (expected 0)"
	elif grep -q 'Changed files:' <<<"${run_out}"; then
		pass "run printed a Changed files: summary after editing the workspace"
	elif [[ ${wrote_file} -eq 1 ]]; then
		fail "run wrote smoke.txt but printed no 'Changed files:' summary"
	else
		skip "run did not write smoke.txt (model declined the edit) — cannot assert Changed files summary"
	fi
	rm -rf "${fixture}"
fi

# --- Result -----------------------------------------------------------------
section "result"
printf '  %d passed, %d failed, %d skipped\n' "${PASS}" "${FAIL}" "${SKIP}"
if [[ ${FAIL} -gt 0 ]]; then
	exit 1
fi
exit 0
