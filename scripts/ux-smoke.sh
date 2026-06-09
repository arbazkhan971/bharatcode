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
# UX_SMOKE_PROVIDER_MODE selects how that provider is supplied:
#
#   fake (default) — start a local, OpenAI-compatible fake provider and point
#     the binary at a self-contained fake config (BHARATCODE_CONFIG / --config),
#     so JSON framing and the changed-files summary are asserted deterministically
#     with no ChatGPT sign-in, API key, or network. The fake replies with a
#     write-tool call on the first turn and a short final message on the next, so
#     the changed-files path actually fires. CI can rely on these checks here.
#   live — use whatever provider doctor reports as ready (key set, local endpoint
#     up, or ChatGPT signed in). When none is reachable the live checks are
#     SKIPPED rather than failed, so the deterministic, offline checks (redraw
#     flood, doctor ChatGPT line, clean exits) still run. Set
#     UX_SMOKE_REQUIRE_PROVIDER=1 to turn those skips into failures on a host
#     that is expected to have a provider configured.
#
# Even in fake mode the live checks remain available as an optional extra: set
# UX_SMOKE_ALSO_LIVE=1 to run them after the fake-provider checks.
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

# Provider mode for the run-based checks: "fake" (default) stands up a local
# OpenAI-compatible fake provider so the checks are deterministic and offline;
# "live" gates them on a real provider doctor reports as ready.
UX_SMOKE_PROVIDER_MODE="${UX_SMOKE_PROVIDER_MODE:-fake}"

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

# --- Fake provider (deterministic, offline) ---------------------------------
# In the default fake mode the run-based checks need a provider that answers
# locally, with no ChatGPT sign-in / API key / network. We stand up a tiny
# OpenAI-compatible endpoint that the binary reaches through a self-contained
# fake config, then drive the same `run --json` / changed-files assertions
# against it. The fake answers the first turn with a write-tool call (so the
# changed-files path fires) and any later turn — once a tool result is in the
# transcript — with a short final message, ending the loop.
FAKE_SRV_PID=""
FAKE_CONFIG=""
FAKE_SCRIPT=""
FAKE_PORT=""
# Dummy secret the fake provider's api_key_env points at; the fake never checks
# it, but the openai_compatible client requires the env var to be present.
export UX_SMOKE_FAKE_API_KEY="ux-smoke-fake-key"

fake_provider_supported() {
	# The fake server is a ~40-line Python stdlib HTTP handler; Python 3 is the
	# only dependency. Without it (or the curl used for readiness polling) the
	# fake mode cannot run, so the caller falls back to live gating.
	command -v python3 >/dev/null 2>&1 && command -v curl >/dev/null 2>&1
}

# start_fake_provider writes the server + config to temp files, picks a free
# port, launches the server, and waits for it to accept a request. It sets
# FAKE_SRV_PID / FAKE_CONFIG on success and returns non-zero on failure so the
# caller can degrade gracefully.
start_fake_provider() {
	FAKE_SCRIPT="$(mktemp -t ux-smoke-fakeprov.XXXXXX.py)"
	FAKE_CONFIG="$(mktemp -t ux-smoke-fakecfg.XXXXXX.json)"
	# Let the OS hand us a free port to avoid colliding with anything already
	# bound on the host or a parallel run of this script.
	FAKE_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
	if [[ -z "${FAKE_PORT}" ]]; then
		return 1
	fi

	cat >"${FAKE_SCRIPT}" <<'PY'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):  # silence per-request logging
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except Exception:
            body = {}
        messages = body.get("messages", [])

        def text_of(msg):
            content = msg.get("content")
            if isinstance(content, str):
                return content
            if isinstance(content, list):  # multimodal parts
                return " ".join(p.get("text", "") for p in content if isinstance(p, dict))
            return ""

        # The most recent user turn decides the response shape. Only a prompt
        # that explicitly asks to write drives a tool call; every other prompt
        # (e.g. the JSON-framing "reply with PONG" check) gets a plain text reply
        # so the run finishes in one turn and exits cleanly.
        last_user = ""
        for msg in messages:
            if msg.get("role") == "user":
                last_user = text_of(msg)
        wants_write = "write" in last_user.lower()
        # Once a tool result is in the transcript the work is done: reply with a
        # short final message and no tool calls so the agent loop terminates.
        has_tool_result = any(m.get("role") == "tool" for m in messages)

        if wants_write and not has_tool_result:
            # First turn of a write request: drive a write through the tracked
            # write tool so the run edits the workspace and prints a "Changed
            # files:" summary.
            payload = {
                "choices": [{"message": {"content": "", "tool_calls": [{
                    "id": "call_1",
                    "type": "function",
                    "function": {
                        "name": "write",
                        "arguments": json.dumps({"path": "smoke.txt", "content": "ok"}),
                    },
                }]}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1},
            }
        else:
            payload = {
                "choices": [{"message": {"content": "PONG", "tool_calls": []}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1},
            }
        data = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

if __name__ == "__main__":
    port = int(sys.argv[1])
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY

	# A self-contained config: openai_compatible provider pointed at the fake
	# endpoint, one model on it, and a coder agent bound to that model. Config
	# slices replace (not merge with) the defaults, so this leaves no real
	# provider reachable — the run is fully deterministic.
	cat >"${FAKE_CONFIG}" <<JSON
{
  "providers": [
    {
      "name": "ux-smoke-fake",
      "type": "openai_compatible",
      "base_url": "http://127.0.0.1:${FAKE_PORT}/v1",
      "api_key_env": "UX_SMOKE_FAKE_API_KEY",
      "models": ["ux-smoke-fake-model"]
    }
  ],
  "models": [
    {
      "id": "ux-smoke-fake-model",
      "provider": "ux-smoke-fake",
      "context_window": 8192,
      "supports_tools": true
    }
  ],
  "agents": [
    {
      "name": "coder",
      "model": "ux-smoke-fake-model",
      "system_prompt": "You are a deterministic UX-smoke test agent."
    }
  ]
}
JSON

	# Export the fake config path under BHARATCODE_CONFIG too. The binary reads
	# the path from the --config flag (passed by run_provider_checks), so this
	# export is for any tooling that keys off the env var and to make the
	# fake-config contract explicit; the flag remains the load-bearing handle.
	export BHARATCODE_CONFIG="${FAKE_CONFIG}"

	python3 "${FAKE_SCRIPT}" "${FAKE_PORT}" >/dev/null 2>&1 &
	FAKE_SRV_PID=$!

	# Poll until the server accepts a POST (or give up). A fresh Python HTTP
	# server binds in well under a second; 50 * 0.1s leaves ample headroom.
	local i
	for i in $(seq 1 50); do
		if ! kill -0 "${FAKE_SRV_PID}" 2>/dev/null; then
			return 1 # server died before it was ready
		fi
		if curl -s -o /dev/null -X POST \
			"http://127.0.0.1:${FAKE_PORT}/v1/chat/completions" -d '{"messages":[]}'; then
			return 0
		fi
		perl -e 'select(undef, undef, undef, 0.1)' 2>/dev/null || sleep 1
	done
	return 1
}

# stop_fake_provider tears down the server and removes its temp files. Safe to
# call when nothing was started (all vars empty).
stop_fake_provider() {
	if [[ -n "${FAKE_SRV_PID}" ]]; then
		kill "${FAKE_SRV_PID}" 2>/dev/null
		wait "${FAKE_SRV_PID}" 2>/dev/null
		FAKE_SRV_PID=""
	fi
	[[ -n "${FAKE_SCRIPT}" ]] && rm -f "${FAKE_SCRIPT}"
	[[ -n "${FAKE_CONFIG}" ]] && rm -f "${FAKE_CONFIG}"
	FAKE_SCRIPT=""
	FAKE_CONFIG=""
	unset BHARATCODE_CONFIG
}

# cleanup is the single EXIT handler: it removes the redraw-capture temp file
# and tears down the fake provider if one is still running. Both pieces are
# no-ops when their target was never created, so it is safe to register once up
# front regardless of which checks actually run.
capture=""
cleanup() {
	[[ -n "${capture}" ]] && rm -f "${capture}"
	stop_fake_provider
}
trap cleanup EXIT

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
# run_provider_checks drives the two run-based assertions (JSON framing,
# changed-files summary) against whatever provider the caller has arranged. The
# binary is invoked as `"${BIN}" "${run_args[@]}" run ...`, so a fake-mode
# caller passes "--config <fake>" while a live-mode caller passes nothing. The
# assertions themselves are identical in both modes.
run_provider_checks() {
	local -a run_args=("$@")

	# --- Check: `run --json` emits clean NDJSON framing and exits 0 ----------
	# The ${run_args[@]+...} guard keeps an empty array (live mode passes none)
	# from tripping set -u on older bash (3.2 ships on macOS).
	json_out="$(printf 'reply with the single word PONG and nothing else' |
		run_bounded "${RUN_TIMEOUT}" "${BIN}" ${run_args[@]+"${run_args[@]}"} run --json --quiet 2>/dev/null)"
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
		run_bounded "${RUN_TIMEOUT}" "${BIN}" ${run_args[@]+"${run_args[@]}"} run --yolo --quiet --project-dir "${fixture}" 2>/dev/null)"
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
}

# live_provider_checks runs the run-based checks against a real provider, gated
# on doctor reporting one ready. "Provider ready" appears in doctor only when
# the active provider can actually be used right now (key set, local endpoint
# up, or ChatGPT signed in). When none is ready the checks are skipped (or
# promoted to failures under UX_SMOKE_REQUIRE_PROVIDER=1 via skip()).
live_provider_checks() {
	section "live run checks (require a usable provider)"
	if grep -qiE 'Provider ready' <<<"${doctor_out}"; then
		run_provider_checks
	else
		skip "no usable provider (doctor lacks a 'Provider ready' line) — skipping JSON run and changed-files checks"
	fi
}

# Default (fake) mode: stand up the local fake provider and assert framing +
# changed-files deterministically, with the live checks available as an opt-in
# extra. Live mode skips the fake server and gates purely on doctor.
if [[ "${UX_SMOKE_PROVIDER_MODE}" == "fake" ]]; then
	section "fake-provider run checks (deterministic, offline)"
	if ! fake_provider_supported; then
		skip "fake provider needs python3 + curl — falling back to live provider gating"
		live_provider_checks
	elif start_fake_provider; then
		run_provider_checks --config "${FAKE_CONFIG}"
		stop_fake_provider
		# The live checks are an optional extra in fake mode.
		if [[ "${UX_SMOKE_ALSO_LIVE:-0}" == "1" ]]; then
			live_provider_checks
		fi
	else
		stop_fake_provider
		fail "could not start the fake provider (server failed to bind/respond)"
	fi
else
	# Any non-"fake" value selects the historical live-only behavior.
	live_provider_checks
fi

# --- Result -----------------------------------------------------------------
section "result"
printf '  %d passed, %d failed, %d skipped\n' "${PASS}" "${FAIL}" "${SKIP}"
if [[ ${FAIL} -gt 0 ]]; then
	exit 1
fi
exit 0
