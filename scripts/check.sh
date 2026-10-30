#!/usr/bin/env bash
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
# A clean checkout can reuse the main repository's virtualenv instead of building
# a new one from the index:
#   UV_PROJECT_ENVIRONMENT=/path/to/repo/python/.venv bash scripts/check.sh
if [ -n "${UV_PROJECT_ENVIRONMENT:-}" ]; then
  export UV_PROJECT_ENVIRONMENT
  echo "using UV_PROJECT_ENVIRONMENT=$UV_PROJECT_ENVIRONMENT"
fi

usage() {
  cat <<'EOF'
usage: bash scripts/check.sh [--db] [--e2e] [--all] [--cov] [--allow-unconfigured]

  (no flag)  fast layer only: static checks, unit tests and builds
  --db       fast layer plus the real-database layer (needs the integration env files)
  --e2e      fast layer plus the browser layer (needs the dev stack and Chromium)
  --all      fast layer plus both of the above
  --cov      reserved for coverage collection (batch 2); accepted, nothing collected yet
  --stack    use the isolated stack from scripts/dev/isolated-stack.sh
             (reads $ECR_PORTS_FILE, default /tmp/ecr-isolated/ports.json)
  --allow-unconfigured
             only with --all: accept items that could not run instead of failing the gate

Every check prints one stable line: [<layer>] <id> <command summary>.
The last three lines are always PASSED <n> / UNCONFIGURED <n> / FAILED <n>.
--all is strict: any UNCONFIGURED item makes it exit 1 unless --allow-unconfigured is given.
A plain run, --db or --e2e alone never fail because something is unconfigured.
Unknown flags exit 2; a failing check exits 1.
EOF
}

DB=0
E2E=0
COV=0
ALL=0
STACK=0
ALLOW_UNCONFIGURED=0
for arg in "$@"; do
  case "$arg" in
    --db) DB=1 ;;
    --e2e) E2E=1 ;;
    --all) DB=1; E2E=1; ALL=1 ;;
    --cov) COV=1 ;;
    --stack) STACK=1 ;;
    --allow-unconfigured) ALLOW_UNCONFIGURED=1 ;;
    -h | --help) usage; exit 0 ;;
    *)
      printf 'unknown argument: %s\n\n' "$arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

# ---------------------------------------------------------------- isolated stack
ports_file=${ECR_PORTS_FILE:-/tmp/ecr-isolated/ports.json}
stack_value() {
  STACK_KEY=$1 python3 -c 'import json,os,sys
try:
    print(json.load(open(os.environ["ECR_PORTS_FILE"] if "ECR_PORTS_FILE" in os.environ else sys.argv[1])).get(os.environ["STACK_KEY"], ""))
except Exception:
    print("")' "$ports_file"
}
stack_ready() {
  [ -f "$ports_file" ] || return 1
  local gateway ingest
  gateway=$(stack_value gateway)
  ingest=$(stack_value ingest)
  [ -n "$gateway" ] && [ -n "$ingest" ] || return 1
  curl -sf -m 2 "http://$gateway/healthz" >/dev/null 2>&1 || return 1
  (echo > "/dev/tcp/${ingest%:*}/${ingest#*:}") >/dev/null 2>&1 || return 1
  return 0
}
STACK_HINT="run scripts/dev/isolated-stack.sh to unlock"

passed=0
unconfigured=0
failed=0
unconfigured_items=()
failed_items=()
layer_t0=0

layer_start() {
  layer_t0=$(date +%s%N)
  echo "BEGIN $1"
}

layer_end() {
  echo "END $1 $((($(date +%s%N) - layer_t0) / 1000000))ms"
}

# run <layer> <id> <command...> — the id is the stable key for gate diffs.
run() {
  local layer=$1 id=$2
  shift 2
  local command="$*"
  printf '[%s] %s %s\n' "$layer" "$id" "$command"
  local status=0
  bash -c "$command" || status=$?
  if [ "$status" -eq 0 ]; then
    passed=$((passed + 1))
  else
    failed=$((failed + 1))
    failed_items+=("[$layer] $id (exit $status)")
  fi
}

# skip <layer> <id> <reason> <command...> — counted as UNCONFIGURED, never as a failure.
skip() {
  local layer=$1 id=$2 reason=$3
  shift 3
  printf '[%s] %s UNCONFIGURED (%s) %s\n' "$layer" "$id" "$reason" "$*"
  unconfigured=$((unconfigured + 1))
  unconfigured_items+=("$id — $reason")
}

# ---------------------------------------------------------------- fast layer
layer_start fast
run fast privacy-guard "python3 scripts/check/privacy_guard.py"
run fast privacy-tests "python3 -m unittest discover -s tests -p 'test_privacy*.py'"
run fast publication-tests "python3 -m unittest discover -s tests -p 'test_publication*.py'"
run fast worker-harness-tests "python3 -m unittest discover -s tests -p 'test_worker_process_harness.py'"
run fast docs-links "python3 scripts/check/check-docs.py"
run fast openapi "python3 scripts/check/check-openapi.py"
run fast eval-corpus "python3 scripts/check/check-eval-corpus.py"
run fast doc-baseline "python3 scripts/check/check-doc-baseline.py"
run fast ownership "python3 scripts/check/check-ownership.py"
run fast docs-drift "python3 scripts/check/check-docs-drift.py"
run fast stage-boundaries "python3 scripts/check/check-stage-boundaries.py"
run fast admin-boundaries "python3 scripts/check/check-admin-boundaries.py"
run fast contracts "python3 scripts/check/check-contracts.py"
run fast decisions-index "python3 scripts/check/check-decisions-index.py"
run fast doc-paths "python3 scripts/check/check-doc-paths.py"
run fast domain-imports "python3 scripts/check/check-domain-imports.py"
run fast service-logs "python3 scripts/check/check-service-logs.py"
run fast shell-syntax "while IFS= read -r -d '' script; do bash -n \"\$script\"; done < <(find scripts docker eval -name '*.sh' -print0)"
run fast local-services-exec "test -x scripts/dev/v2-local-services.sh"
run fast shellcheck "shellcheck -S warning scripts/*.sh scripts/*/*.sh eval/*.sh docker/db/*.sh docker/db/init/*.sh"
run fast go-test "cd go && go test -p 2 ./..."
run fast go-vet "cd go && go vet ./..."
run fast python-sync "cd python && uv sync --frozen"
run fast python-tests "cd python && uv run --frozen pytest -q"
run fast python-lint "cd python && uv run --frozen ruff check app ../eval ../scripts/check/artifact-cleanup-fault-smoke.py ../scripts/dev/v2_services.py"
run fast python-imports "cd python && uv run --frozen python -c 'from app.providers.contracts import EmbeddingResult; from app.ai_process import build_ai_process; from app.rpc.server import build_server'"
run fast provider-smoke "cd python && uv run --frozen python ../scripts/check/provider-smoke.py --response-fixture ../scripts/check/provider-smoke-fixture.json"
run fast artifact-storage-smoke "cd python && uv run --frozen python ../scripts/check/artifact-storage-smoke.py"
run fast artifact-cleanup-smoke "cd python && uv run --frozen python ../scripts/check/artifact-cleanup-fault-smoke.py"
run fast buf-lint "buf lint"
run fast buf-build "buf build -o /dev/null"
run fast buf-breaking "buf breaking --against .git#branch=main"
run fast npm-ci "cd frontend && npm ci --include=dev --no-audit --no-fund"
run fast frontend-tests "cd frontend && npm test"
run fast frontend-build "cd frontend && npm run build"
run fast facts-fake-matrix "bash scripts/check/facts-fake-matrix.sh"
run fast eval-offline "bash eval/run.sh offline"
layer_end fast

# ---------------------------------------------------------- real database layer
if [ "$DB" -eq 1 ]; then
  layer_start db
  import_env=${ECR_IMPORT_INTEGRATION_ENV:-/etc/ecommerce-rag/import-integration.env}
  test_env=${ECR_TEST_INTEGRATION_ENV:-/etc/ecommerce-rag/test-integration.env}

  # The import and customer suites drive the real ingest gRPC service; without a
  # reachable endpoint the queue step fails for reasons the gate cannot fix.
  import_reason=""
  if stack_ready; then
    # The isolated stack serves the integration database, so the import suites run
    # against it even though the env file points at another ingest endpoint.
    V2_INTEGRATION_INGEST_ADDR=$(stack_value ingest)
    export V2_INTEGRATION_INGEST_ADDR
    export ECR_PORTS_FILE=$ports_file
    # The stack serves the integration database, so the suites must use that
    # database's DSNs rather than the import env file's.
    import_env=$test_env
    echo "using isolated stack: ingest $V2_INTEGRATION_INGEST_ADDR db $(basename "$import_env")"
    if [ ! -f /etc/ecommerce-rag/eval-scope.key ]; then
      import_reason="missing /etc/ecommerce-rag/eval-scope.key"
    fi
  elif [ ! -f "$import_env" ]; then
    import_reason="missing $import_env"
  elif ! grep -q "^V2_INGEST_GRPC_ADDR=" "$import_env" 2>/dev/null; then
    import_reason="$import_env has no V2_INGEST_GRPC_ADDR"
  elif [ ! -f /etc/ecommerce-rag/eval-scope.key ]; then
    import_reason="missing /etc/ecommerce-rag/eval-scope.key"
  else
    addr=$(sed -n "s/^V2_INGEST_GRPC_ADDR=//p" "$import_env" | head -1 | tr -d "\"'")
    if ! timeout 2 bash -c "exec 3<>/dev/tcp/${addr%:*}/${addr##*:}" 2>/dev/null; then
      import_reason="ingest $addr is not reachable"
    fi
  fi

  python_reason=""
  if [ ! -f "$test_env" ]; then
    python_reason="missing $test_env"
  elif ! grep -q "^V2_INGEST_DSN=" "$test_env" 2>/dev/null; then
    python_reason="$test_env has no V2_INGEST_DSN"
  elif ! grep -q "^V2_AI_DSN=" "$test_env" 2>/dev/null; then
    python_reason="$test_env has no V2_AI_DSN"
  fi

  if [ "$STACK" -eq 1 ] && [ -z "$import_reason" ] && ! stack_ready; then
    import_reason="$STACK_HINT (no $ports_file)"
  fi
  if [ -n "$import_reason" ]; then
    skip db db-import-batch "$import_reason" "cd go && V2_INTEGRATION_ENV=$import_env go test ./internal/admin -run TestImportBatch"
    skip db db-import-customer "$import_reason" "cd go && V2_INTEGRATION_ENV=$import_env go test ./internal/customer ./internal/ingestclient"
  else
    run db db-import-batch "cd go && V2_INTEGRATION_ENV=$import_env go test ./internal/admin -run TestImportBatch"
    run db db-import-customer "cd go && V2_INTEGRATION_ENV=$import_env go test ./internal/customer ./internal/ingestclient"
  fi
  if [ ! -f "$test_env" ]; then
    skip db db-admin-access "missing $test_env" "cd go && V2_INTEGRATION_ENV=$test_env go test ./internal/admin -run TestDatabaseAdministrativeAccess"
    skip db db-localauth "missing $test_env" "cd go && V2_INTEGRATION_ENV=$test_env go test ./internal/localauth"
  else
    run db db-admin-access "cd go && V2_INTEGRATION_ENV=$test_env go test ./internal/admin -run TestDatabaseAdministrativeAccess"
    run db db-localauth "cd go && V2_INTEGRATION_ENV=$test_env go test ./internal/localauth"
  fi
  if [ -n "$python_reason" ]; then
    skip db db-python "$python_reason" "cd python && V2_INTEGRATION_ENV=$test_env uv run pytest tests/test_v2_ingest_database.py tests/test_postgres_retriever.py -q"
  else
    run db db-python "cd python && V2_INTEGRATION_ENV=$test_env uv run pytest tests/test_v2_ingest_database.py tests/test_postgres_retriever.py -q"
  fi
  layer_end db
fi

# ------------------------------------------------------------- browser layer
if [ "$E2E" -eq 1 ]; then
  layer_start e2e
  e2e_reason=""
  if ! command -v node >/dev/null 2>&1; then
    e2e_reason="node is not installed"
  elif [ ! -d frontend/node_modules ]; then
    e2e_reason="frontend/node_modules is missing"
  else
    # The browser layer is bound to the isolated stack: the dev stack serves a
    # different database, so running against it only produces false failures.
    e2e_gateway=$(stack_value gateway)
    e2e_workspace="http://$(stack_value vite)"
    if stack_ready; then
      e2e_gateway=$(stack_value gateway)
      stack_vite=$(stack_value vite)
      if [ -n "$stack_vite" ]; then
        e2e_workspace="http://$stack_vite"
      fi
      export ECR_PORTS_FILE=$ports_file
    else
      e2e_reason="$STACK_HINT (no $ports_file)"
    fi
    if [ -z "$e2e_reason" ] && ! curl -sf -m 2 "http://$e2e_gateway/healthz" >/dev/null 2>&1; then
      e2e_reason="gateway $e2e_gateway is not reachable"
    elif [ -z "$e2e_reason" ] && ! curl -sf -m 2 "$e2e_workspace/" >/dev/null 2>&1; then
      e2e_reason="vite $e2e_workspace is not reachable"
    fi
    export WORKSPACE_URL=$e2e_workspace
    browser_env="$(dirname "$ports_file")/browser.env"
    if [ -f "$browser_env" ]; then
      ECR_BROWSER_ENV=$browser_env
      export ECR_BROWSER_ENV
      LOGIN_PASSWORD=$(sed -n "s/^LOGIN_PASSWORD=//p" "$browser_env" | head -1)
      export LOGIN_PASSWORD
    fi
  fi
  for spec in frontend/tests/*.browser.mjs; do
    name=$(basename "$spec" .browser.mjs)
    if [ "$name" = "login" ]; then
      # The login flow needs the local-account identity, which is a separate stack.
      local_ports=${ECR_LOCAL_PORTS_FILE:-/tmp/ecr-isolated-local/ports.json}
      local_env="$(dirname "$local_ports")/browser.env"
      if [ ! -f "$local_ports" ] || [ ! -f "$local_env" ]; then
        skip e2e "e2e-login" "run scripts/dev/isolated-stack.sh --identity local --ports 26091" "cd frontend && node tests/login.browser.mjs"
        continue
      fi
      WORKSPACE_URL=$e2e_workspace
      ECR_BROWSER_ENV=$local_env
      LOGIN_PASSWORD=$(sed -n "s/^LOGIN_PASSWORD=//p" "$local_env" | head -1)
      LOGIN_USER=$(sed -n "s/^LOGIN_USER=//p" "$local_env" | head -1)
      export WORKSPACE_URL ECR_BROWSER_ENV LOGIN_PASSWORD LOGIN_USER
      run e2e "e2e-login" "cd frontend && node tests/login.browser.mjs"
      continue
    fi
    if [ -n "$e2e_reason" ]; then
      skip e2e "e2e-$name" "$e2e_reason" "cd frontend && node tests/$name.browser.mjs"
    else
      run e2e "e2e-$name" "cd frontend && node tests/$name.browser.mjs"
    fi
  done
  layer_end e2e
fi

if [ "$COV" -eq 1 ]; then
  echo "[cov] coverage-collect reserved for batch 2: nothing collected in this batch"
fi

# --------------------------------------------------------------------- summary
for item in ${failed_items[@]+"${failed_items[@]}"}; do
  echo "FAILED ITEM: $item"
done
for item in ${unconfigured_items[@]+"${unconfigured_items[@]}"}; do
  echo "UNCONFIGURED ITEM: $item"
done
if [ "$DB" -eq 0 ] && [ "$E2E" -eq 0 ] && [ "$failed" -eq 0 ]; then
  echo "PASS foundation checks (business and real API tests not included)"
fi
echo "PASSED $passed"
echo "UNCONFIGURED $unconfigured"
echo "FAILED $failed"
if [ "$failed" -gt 0 ]; then
  exit 1
fi
if [ "$ALL" -eq 1 ] && [ "$unconfigured" -gt 0 ] && [ "$ALLOW_UNCONFIGURED" -eq 0 ]; then
  echo "STRICT: $unconfigured item(s) could not run; use --allow-unconfigured to accept this"
  exit 1
fi
exit 0
