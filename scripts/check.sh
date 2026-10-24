#!/usr/bin/env bash
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
python3 scripts/check/privacy_guard.py
python3 -m unittest discover -s tests -p 'test_privacy*.py'
python3 -m unittest discover -s tests -p 'test_publication*.py'
python3 -m unittest discover -s tests -p 'test_worker_process_harness.py'
python3 scripts/check/check-docs.py
python3 scripts/check/check-doc-baseline.py
while IFS= read -r -d '' script; do bash -n "$script"; done < <(find scripts docker eval -name '*.sh' -print0)
test -x scripts/dev/v2-local-services.sh
shellcheck -S warning scripts/*.sh scripts/*/*.sh eval/*.sh docker/db/*.sh docker/db/init/*.sh
(cd go; go test -p 2 ./...; go vet ./...)
(cd python; uv sync --frozen; uv run pytest -q; uv run ruff check app ../scripts/check/artifact-cleanup-fault-smoke.py ../scripts/dev/v2_services.py ../scripts/dev/provider-budget.py; uv run python -c 'from app.providers.contracts import EmbeddingResult; from app.ai_process import build_ai_process; from app.rpc.server import build_server')
(cd python; uv run python ../scripts/check/provider-smoke.py --response-fixture ../scripts/check/provider-smoke-fixture.json)
(cd python; uv run python ../scripts/check/artifact-storage-smoke.py)
(cd python; uv run python ../scripts/check/artifact-cleanup-fault-smoke.py)
buf lint
buf build -o /dev/null
buf breaking --against .git#branch=main
(cd web; npm ci --include=dev --no-audit --no-fund; npm test; npm run build)
(cd admin-web; npm ci --include=dev --no-audit --no-fund; npm test; npm run build)
bash scripts/check/facts-fake-matrix.sh
bash eval/run.sh offline
echo "PASS foundation checks (business and real API tests not included)"
