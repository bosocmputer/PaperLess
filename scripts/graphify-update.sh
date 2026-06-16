#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

EXPECTED_VERSION="${GRAPHIFY_VERSION:-0.8.35}"

detect_graphify_version() {
  local version
  version="$(graphify --version 2>/dev/null | awk '{print $2}' || true)"
  if [[ -z "$version" ]] && command -v uv >/dev/null 2>&1; then
    version="$(uv tool list 2>/dev/null | awk '/^graphifyy / {print $2; exit}' | sed 's/^v//' || true)"
  fi
  printf "%s" "$version"
}

if ! command -v graphify >/dev/null 2>&1; then
  printf "graphify is not installed. Run: uv tool install graphifyy==%s\n" "$EXPECTED_VERSION" >&2
  exit 1
fi

ACTUAL_VERSION="$(detect_graphify_version)"
if [[ "$ACTUAL_VERSION" != "$EXPECTED_VERSION" && "${GRAPHIFY_ALLOW_VERSION_DRIFT:-0}" != "1" ]]; then
  printf "expected graphify %s, found %s. Set GRAPHIFY_ALLOW_VERSION_DRIFT=1 to inspect locally.\n" \
    "$EXPECTED_VERSION" "${ACTUAL_VERSION:-unknown}" >&2
  exit 1
fi

mkdir -p graphify-out

START_SECONDS="$(date +%s)"
printf "Updating Graphify graph for safe local scope with graphify %s...\n" "$ACTUAL_VERSION"
graphify update "$ROOT" --no-cluster "$@"

if [[ ! -f graphify-out/graph.json ]]; then
  printf "graphify did not create graphify-out/graph.json\n" >&2
  exit 1
fi

END_SECONDS="$(date +%s)"
printf "Graphify update completed in %ss\n" "$((END_SECONDS - START_SECONDS))"

"$ROOT/scripts/graphify-preflight.sh"
