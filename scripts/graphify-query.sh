#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GRAPH_PATH="${GRAPHIFY_GRAPH_PATH:-graphify-out/graph.json}"
BUDGET="${GRAPHIFY_QUERY_BUDGET:-1600}"

if [[ $# -lt 1 ]]; then
  printf "usage: scripts/graphify-query.sh \"question about the codebase\"\n" >&2
  exit 2
fi

if [[ ! -f "$GRAPH_PATH" ]]; then
  printf "%s does not exist. Run scripts/graphify-update.sh first.\n" "$GRAPH_PATH" >&2
  exit 1
fi

QUESTION="$*"
graphify query "$QUESTION" --graph "$GRAPH_PATH" --budget "$BUDGET"
