#!/usr/bin/env bash
#
# sqlc-generate.sh — regenerate the type-safe Go database layer from SQL.
#
# Runs `sqlc generate` from the repository root using the existing sqlc.yaml.
# Schema and query paths in sqlc.yaml are repo-root-relative, so this script
# resolves the repo root from its own location before invoking sqlc.
#
# Requires sqlc on PATH: https://docs.sqlc.dev/en/latest/overview/install.html
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

if ! command -v sqlc >/dev/null 2>&1; then
  echo "error: sqlc is not installed or not on PATH" >&2
  echo "install it from https://docs.sqlc.dev/en/latest/overview/install.html" >&2
  exit 1
fi

echo "Running sqlc generate from ${REPO_ROOT}"
sqlc generate
echo "sqlc generate complete"
