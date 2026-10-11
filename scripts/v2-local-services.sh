#!/usr/bin/env bash
set -euo pipefail

# Start the four V2 processes with explicit, isolated configuration. This is a
# local operator entrypoint; it does not create databases or print secrets.
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
: "${V2_GATEWAY_DSN:?V2_GATEWAY_DSN is required}"
: "${V2_RETRIEVER_DSN:?V2_RETRIEVER_DSN is required}"
: "${V2_CHECKPOINT_DSN:?V2_CHECKPOINT_DSN is required}"
: "${V2_INGEST_DSN:?V2_INGEST_DSN is required}"
: "${V2_IDENTITY_TENANT:?V2_IDENTITY_TENANT is required}"
: "${V2_IDENTITY_USER:?V2_IDENTITY_USER is required}"
: "${V2_PROVIDER_CONFIG:?V2_PROVIDER_CONFIG is required}"

export APP_ENV=${APP_ENV:-development}
export RAG_PROFILE=${RAG_PROFILE:-v2_langgraph_fixture}
export V2_GRPC_ADDR=${V2_GRPC_ADDR:-127.0.0.1:50052}
export V2_INGEST_GRPC_ADDR=${V2_INGEST_GRPC_ADDR:-127.0.0.1:50053}
export HTTP_ADDR=${HTTP_ADDR:-127.0.0.1:8080}
export PYTHONPATH="$repo/python${PYTHONPATH:+:$PYTHONPATH}"

if [[ "$APP_ENV" != development && "$APP_ENV" != test ]]; then
  echo 'APP_ENV must be development or test' >&2
  exit 2
fi

pids=()
cleanup() {
  trap - EXIT INT TERM
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT INT TERM

go build -p 2 -o "$repo/.local/v2-gateway" ./go/cmd/gateway
"$repo/.local/v2-gateway" & pids+=("$!")
(cd "$repo/python" && exec .venv/bin/python -m app.rpc.server) & pids+=("$!")
(cd "$repo/python" && exec .venv/bin/python -m app.rpc.ingest_server) & pids+=("$!")
(cd "$repo/python" && exec .venv/bin/python -m app.ingest.worker_main) & pids+=("$!")

echo "V2 services started: gateway=$HTTP_ADDR ai=$V2_GRPC_ADDR ingest=$V2_INGEST_GRPC_ADDR"
wait -n "${pids[@]}"
status=$?
echo "V2 service exited with status $status" >&2
exit "$status"
