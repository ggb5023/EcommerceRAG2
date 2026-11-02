#!/usr/bin/env bash
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
base=${WORKSPACE_URL:-http://127.0.0.1:5174}
out=${REDESIGN_OUT:-$repo/.local/redesign}
mkdir -p "$out"
cd "$repo/frontend"
WORKSPACE_URL="$base" REDESIGN_OUT="$out" node tests/admin-visual-shots.mjs
