#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

EXPECTED_VERSION="${GRAPHIFY_VERSION:-0.8.35}"
MAX_GRAPH_BYTES="${GRAPHIFY_MAX_GRAPH_BYTES:-20000000}"
GRAPH_PATH="${GRAPHIFY_GRAPH_PATH:-graphify-out/graph.json}"

fail() {
  printf "graphify preflight failed: %s\n" "$1" >&2
  exit 1
}

detect_graphify_version() {
  local version
  version="$(graphify --version 2>/dev/null | awk '{print $2}' || true)"
  if [[ -z "$version" ]] && command -v uv >/dev/null 2>&1; then
    version="$(uv tool list 2>/dev/null | awk '/^graphifyy / {print $2; exit}' | sed 's/^v//' || true)"
  fi
  printf "%s" "$version"
}

command -v graphify >/dev/null 2>&1 || fail "graphify executable is not installed"

ACTUAL_VERSION="$(detect_graphify_version)"
if [[ -z "$ACTUAL_VERSION" ]]; then
  fail "could not detect graphify version"
fi
if [[ "$ACTUAL_VERSION" != "$EXPECTED_VERSION" && "${GRAPHIFY_ALLOW_VERSION_DRIFT:-0}" != "1" ]]; then
  fail "expected graphify $EXPECTED_VERSION, found $ACTUAL_VERSION. Set GRAPHIFY_ALLOW_VERSION_DRIFT=1 to inspect locally."
fi

[[ -f "$GRAPH_PATH" ]] || fail "$GRAPH_PATH does not exist. Run scripts/graphify-update.sh first."

GRAPH_BYTES="$(wc -c < "$GRAPH_PATH" | tr -d ' ')"
if [[ "$GRAPH_BYTES" -gt "$MAX_GRAPH_BYTES" ]]; then
  fail "$GRAPH_PATH is ${GRAPH_BYTES} bytes, above limit ${MAX_GRAPH_BYTES}"
fi

if find graphify-out -type f \( \
  -name ".env" -o -name ".env.*" -o -name "*.pem" -o -name "*.key" -o \
  -name "*.p12" -o -name "*.pfx" -o -name "*.sqlite" -o -name "*.sqlite3" -o \
  -name "*.db" -o -name "*.dump" -o -name "*.bak" -o -name "*.png" -o \
  -name "*.jpg" -o -name "*.jpeg" -o -name "*.webp" -o -name "*.gif" -o \
  -name "*.pdf" -o -name "*.xlsx" -o -name "*.xls" -o -name "*.csv" -o \
  -name "*.mp4" -o -name "*.mov" -o -name "*.mp3" -o -name "*.wav" \
  \) | grep -q .; then
  fail "graphify-out contains files that should not be committed"
fi

if grep -Eiq '(^|[/"])(\.env|backups|node_modules|dist|build|\.next|\.vite|coverage|screenshots?|tmp|vendor)([/"]|$)' "$GRAPH_PATH"; then
  fail "$GRAPH_PATH references ignored runtime, build, or artifact paths"
fi

if grep -Eq '(sk-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{20,}|-----BEGIN ((RSA|OPENSSH|EC) )?PRIVATE KEY-----|eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})' "$GRAPH_PATH"; then
  fail "$GRAPH_PATH appears to contain token-like or private-key material"
fi

printf "graphify preflight ok: version=%s graph=%s bytes=%s\n" \
  "$ACTUAL_VERSION" "$GRAPH_PATH" "$GRAPH_BYTES"
