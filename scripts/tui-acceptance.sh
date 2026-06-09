#!/usr/bin/env bash
#
# tui-acceptance.sh — end-to-end TUI acceptance for `bharatcode --yolo`.
#
# Drives the COMPILED binary through a pseudo-terminal for five representative
# "build me an app" prompts (todo, calculator, notes, quiz, static HTML), the
# real shape of how a user reaches for BharatCode. Each case:
#
#   1. Runs the prompt in a fresh, throwaway project directory under a PTY so the
#      alt-screen TUI renders exactly as it does for a human.
#   2. Saves the normalized transcript (ANSI/spinner/timing/UUID/path noise
#      stripped) next to the run for inspection.
#   3. Asserts the app actually produced files on disk, and that the transcript
#      surfaced a "Changed files:" summary and a "Verification:" status line — the
#      two things a yolo user relies on to trust the result without reading a log.
#
# Usage:
#   scripts/tui-acceptance.sh [path-to-bharatcode-binary]
#
# The binary path defaults to ./bharatcode in the repo root. Build one first:
#   go build -o bharatcode .
#
# PROVIDER REQUIREMENT. Producing files, a "Changed files:" summary, and a
# "Verification:" line all require a working model — the agent has to actually do
# the task. When no provider is reachable (no API key, no local server, no
# sign-in) the per-case content assertions are SKIPPED rather than failed, so the
# script stays structurally runnable and lint-clean (bash -n) in an offline CI:
# it still exercises the PTY plumbing and the five cases, it just cannot grade
# their output. Set TUI_ACCEPT_REQUIRE_PROVIDER=1 to promote those skips to
# failures on a host that is expected to have a provider configured.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BIN="${1:-${REPO_ROOT}/bharatcode}"

# Where normalized transcripts are written, one file per case.
OUT_DIR="${TUI_ACCEPT_OUT_DIR:-$(mktemp -d -t tui-acceptance.XXXXXX)}"
# Upper bound (seconds) on any single live-model run so a stuck turn cannot wedge
# the suite.
RUN_TIMEOUT="${TUI_ACCEPT_RUN_TIMEOUT:-180}"

PASS=0
FAIL=0
SKIP=0

pass() { printf '  [PASS] %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() {
	if [[ "${TUI_ACCEPT_REQUIRE_PROVIDER:-0}" == "1" ]]; then
		fail "$1 (skip promoted to failure by TUI_ACCEPT_REQUIRE_PROVIDER=1)"
	else
		printf '  [SKIP] %s\n' "$1"
		SKIP=$((SKIP + 1))
	fi
}
section() { printf '\n== %s ==\n' "$1"; }

if [[ ! -x "${BIN}" ]]; then
	printf 'error: binary not found or not executable: %s\n' "${BIN}" >&2
	printf 'build it first, e.g.: go build -o %s/bharatcode .\n' "${REPO_ROOT}" >&2
	exit 2
fi

# run_bounded runs the rest of its arguments under a wall-clock cap, escalating to
# SIGKILL if the process ignores the soft signal. It abstracts over `timeout`
# (GNU/coreutils) vs `gtimeout` (Homebrew) and over hosts that ship neither. Exit
# status 124 marks a timeout.
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
		"$@"
	fi
}

# pty_capture runs a command inside a pseudo-terminal, relaying a fixed input
# stream to it, and writes the raw terminal output to the given file. It selects
# the right `script` invocation for the host: util-linux `script` takes
# `-qec "cmd" file`, while the BSD/macOS `script` takes `-q file cmd args…`. The
# input (a prompt plus a trailing Ctrl-C to quit the idle TUI) is fed on stdin.
#
# Arguments: <output-file> <input-string> <command> [args…]
pty_capture() {
	local outfile="$1" input="$2"
	shift 2
	if script --version >/dev/null 2>&1; then
		# util-linux: -e propagates the child exit status, -c runs the command.
		printf '%b' "${input}" | run_bounded "${RUN_TIMEOUT}" \
			script -qec "$(printf '%q ' "$@")" "${outfile}" >/dev/null 2>&1
	else
		# BSD/macOS: -q quiet, file then command.
		printf '%b' "${input}" | run_bounded "${RUN_TIMEOUT}" \
			script -q "${outfile}" "$@" >/dev/null 2>&1
	fi
}

# normalize_transcript strips the per-run terminal noise (ANSI/CSI/OSC escapes,
# cursor moves, the spinner glyph, control bytes) from a captured transcript and
# prints the readable result. It mirrors the Go normalizeTranscript used by the
# unit tests so a saved transcript reads the same way the test asserts on. It is
# deliberately conservative: it leaves the human-meaningful text — including the
# "Changed files:" and "Verification:" lines the asserts look for — intact.
normalize_transcript() {
	local src="$1"
	# 1) Drop CSI/OSC/other ESC sequences. 2) Drop carriage returns and the
	# braille spinner frames. 3) Squeeze blank lines.
	LC_ALL=C sed -E \
		-e 's/\x1b\[[0-9;?]*[ -/]*[@-~]//g' \
		-e 's/\x1b\][^\x07]*(\x07|\x1b\\)//g' \
		-e 's/\x1b[@-Z\\-_=>]//g' \
		-e 's/\r//g' "${src}" |
		LC_ALL=C tr -d '\000-\010\013\014\016-\037' |
		sed -E 's/[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]/ /g' |
		cat -s
}

# is_provider_ready reports whether a usable model is reachable right now, reusing
# `doctor`'s "Provider ready" line (the same signal ux-smoke.sh keys off). The
# content assertions below run only when this is true.
is_provider_ready() {
	"${BIN}" doctor 2>/dev/null | grep -qiE 'Provider ready'
}

printf 'TUI acceptance against: %s\n' "${BIN}"
printf 'transcripts: %s\n' "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

PROVIDER_READY=0
if is_provider_ready; then
	PROVIDER_READY=1
else
	printf 'note: no usable provider (doctor lacks a "Provider ready" line) — content assertions will be skipped\n'
fi

# Each case is "name|prompt". The prompt names a concrete, self-contained app so a
# single yolo turn can plausibly create files and (where it makes sense) verify
# them, exercising the full Changed-files + Verification surface.
CASES=(
	"todo|Build a small command-line todo app in this directory: a main source file and a passing test. Then stop."
	"calculator|Build a calculator module in this directory with add/subtract/multiply/divide and a passing test. Then stop."
	"notes|Build a notes app in this directory that saves notes to a file, with a passing test. Then stop."
	"quiz|Build a short multiple-choice quiz program in this directory with a passing test. Then stop."
	"statichtml|Create a static index.html page in this directory with a title, a heading, and a short paragraph. Then stop."
)

for case_spec in "${CASES[@]}"; do
	name="${case_spec%%|*}"
	prompt="${case_spec#*|}"
	section "case: ${name}"

	workdir="$(mktemp -d -t "tui-accept-${name}.XXXXXX")"
	rawfile="${OUT_DIR}/${name}.raw"
	normfile="${OUT_DIR}/${name}.txt"

	# Feed the prompt, give the turn a moment, then send Ctrl-C twice to quit the
	# idle TUI once the turn settles. The \003 bytes are Ctrl-C.
	input="${prompt}\n\003\003"
	pty_capture "${rawfile}" "${input}" \
		"${BIN}" --yolo --project-dir "${workdir}"

	if [[ -f "${rawfile}" ]]; then
		normalize_transcript "${rawfile}" >"${normfile}" 2>/dev/null
		pass "case ran and saved transcript: ${normfile}"
	else
		fail "case produced no transcript file"
		rm -rf "${workdir}"
		continue
	fi

	# --- Provider-gated content assertions ---------------------------------
	# Without a model the agent cannot create files or print the summary/verify
	# lines, so these are skipped (or promoted to failures) when no provider is
	# ready. With a provider they are hard assertions.
	if [[ "${PROVIDER_READY}" -ne 1 ]]; then
		skip "${name}: no provider — cannot assert files / Changed files: / Verification:"
		rm -rf "${workdir}"
		continue
	fi

	# Output files exist on disk.
	if [[ -n "$(find "${workdir}" -type f -not -path '*/.bharatcode/*' -print -quit 2>/dev/null)" ]]; then
		pass "${name}: produced at least one output file"
	else
		fail "${name}: produced no output files"
	fi

	# Transcript surfaced the changed-files summary.
	if grep -q 'Changed files:' "${normfile}"; then
		pass "${name}: transcript shows 'Changed files:'"
	else
		fail "${name}: transcript missing 'Changed files:'"
	fi

	# Transcript surfaced the verification status line.
	if grep -q 'Verification:' "${normfile}"; then
		pass "${name}: transcript shows 'Verification:'"
	else
		fail "${name}: transcript missing 'Verification:'"
	fi

	rm -rf "${workdir}"
done

# --- Result -----------------------------------------------------------------
section "result"
printf '  %d passed, %d failed, %d skipped\n' "${PASS}" "${FAIL}" "${SKIP}"
printf '  transcripts saved under: %s\n' "${OUT_DIR}"
if [[ ${FAIL} -gt 0 ]]; then
	exit 1
fi
exit 0
