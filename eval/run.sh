#!/usr/bin/env bash
# Single evaluation entry point.
#   bash eval/run.sh offline   free: eval unit tests, frozen-set validation,
#                              lifecycle PLAN and the deterministic retrieval baseline
#   bash eval/run.sh live      paid: prints the ordered real-model steps (see eval/README.md)
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
# Reuse an external virtualenv when one is provided (clean-checkout checks).
py="${UV_PROJECT_ENVIRONMENT:-$repo/python/.venv}/bin/python"
case "$py" in /*) ;; *) py="$repo/$py" ;; esac
revision=${ECR_EVAL_REVISION:-/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional}

case "${1:-offline}" in
  offline)
    (cd eval && "$py" -m pytest -q -p no:cacheprovider .)
    "$py" eval/validate_eval.py >/dev/null && echo "PASS frozen 60-case set"
    "$py" eval/run_mock_retrieval.py >/dev/null && echo "PASS mock retrieval classification"
    "$py" eval/run_v2_live_lifecycle.py >/dev/null && echo "PASS live lifecycle PLAN (no requests)"
    if [[ -d "$revision" ]]; then
      "$py" eval/run_aligned_retrieval.py --revision-root "$revision" \
        | "$py" -c '
import json, sys
d = json.load(sys.stdin); m = d["metrics"]; ok = d["status"] == "PASS"
print("PASS" if ok else "FAIL", "aligned retrieval baseline cases=%s recall@5=%.3f mrr=%.3f"
      % (d["measured_case_count"], m["recall_at_5"] or 0, m["mrr"] or 0))
sys.exit(0 if ok else 1)'
    else
      echo "SKIP aligned retrieval baseline (approved corpus not found at $revision)"
    fi
    ;;
  live)
    cat <<'EOF'
Real-model evaluation (uses the shared budget ledger; see docs/decisions/0006):
  1. eval/run_v2_live_lifecycle.py --live      ingest and publish the 37-document corpus
  2. eval/run_bailian_batch.py --live          frozen 53-case batch, then eval/quality_gate.py
  3. eval/run_v2_quality_e2e.py                HTTP/SSE answers through Go
  4. eval/run_v2_quality_safety_http.py --live seven safety cases
  5. eval/run_customer_service.py              15 merchant customer cases, then customer_service_quality.py
Each step needs protected runtime configuration; arguments are in eval/README.md.
EOF
    ;;
  *) echo "usage: bash eval/run.sh [offline|live]" >&2; exit 2 ;;
esac
