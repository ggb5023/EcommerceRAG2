#!/usr/bin/env bash
set -euo pipefail

# The supervisor constructs a separate environment for each owned process.
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
python="$repo/python/.venv/bin/python"
if [[ ! -x "$python" ]]; then
  echo 'V2 Python environment is missing; run uv sync --frozen in python/' >&2
  exit 2
fi
exec "$python" "$repo/scripts/v2_services.py" "$@"
