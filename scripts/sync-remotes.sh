#!/usr/bin/env bash
# Push main to the single GitHub remote (origin) and verify the readback.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
python3 scripts/privacy_guard.py --history
[[ "$(git branch --show-current)" == main ]] || { echo "Run from main"; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo "Working tree must be clean"; exit 1; }
commit=$(git rev-parse HEAD)
git push origin main:main
observed=$(git ls-remote origin refs/heads/main | awk '{print $1}')
if [[ "$observed" == "$commit" ]]; then echo "PASS origin $commit"; else echo "FAIL origin verification"; exit 1; fi
