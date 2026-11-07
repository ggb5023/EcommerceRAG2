#!/usr/bin/env bash
# Verify the D2 front-proxy contract against the local dev gateway:
#   a) /admin/v1 is reverse-proxied with the original path;
#   b) /v1/turns/{id}/events streams SSE frames without buffering;
#   c) unknown client routes fall back to the SPA index.
#
# The staff turn is created inline: smoke-turn.sh exercises the customer channel
# and deletes its temporary ids, so it cannot supply a /v1/turns/{id} handle.
#
# Exit codes: 0 PASS, 1 FAIL, 2 UNCONFIGURED (gateway/dist not available).
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo"
base=${ECR_BASE_URL:-http://127.0.0.1:8080}
dist="$repo/frontend/dist"

if [ "$(curl -sS -o /dev/null -w '%{http_code}' -m 3 "$base/healthz" 2>/dev/null)" != "200" ]; then
  echo 'UNCONFIGURED dev gateway is not reachable (start the dev stack)'
  exit 2
fi
if [ ! -f "$dist/index.html" ]; then
  echo 'UNCONFIGURED frontend/dist/index.html is missing; run npm run build'
  exit 2
fi
outcome_code=$(curl -sS -o /dev/null -w '%{http_code}' -m 5 "$base/admin/v1/merchant/outcomes?range=30d" 2>/dev/null || true)
if [ "$outcome_code" = "401" ] || [ "$outcome_code" = "403" ]; then
  echo "UNCONFIGURED gateway requires browser identity (got $outcome_code); point ECR_BASE_URL at the isolated development stack"
  exit 2
fi

work=$(mktemp -d)
proxy_pid=""
cleanup() {
  if [ -n "$proxy_pid" ]; then
    kill "$proxy_pid" 2>/dev/null || true
    wait "$proxy_pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

if ! (cd go && go build -o "$work/d2-proxy" ./cmd/d2-proxy); then
  echo 'FAIL could not build the D2 proxy'
  exit 1
fi
"$work/d2-proxy" -listen 127.0.0.1:0 -target "$base" -root "$dist" >"$work/proxy.log" 2>&1 &
proxy_pid=$!
proxy=""
for _ in $(seq 1 50); do
  proxy=$(sed -n 's/^LISTEN //p' "$work/proxy.log" | head -1)
  [ -n "$proxy" ] && break
  sleep 0.2
done
if [ -z "$proxy" ]; then
  echo 'FAIL D2 proxy did not report a listen address'
  cat "$work/proxy.log" >&2
  exit 1
fi
proxy="http://$proxy"

failed=0

# a) API through the proxy, original path preserved.
api_code=$(curl -sS -o "$work/outcomes.json" -w '%{http_code}' -m 5 "$proxy/admin/v1/merchant/outcomes?range=30d")
if [ "$api_code" = "200" ] && grep -q 'scored_total' "$work/outcomes.json"; then
  echo 'PASS d2-api /admin/v1/merchant/outcomes 200 with scored_total'
else
  echo "FAIL d2-api status=$api_code body=$(head -c 200 "$work/outcomes.json")"
  failed=1
fi

# b) SSE: create one staff turn, wait for a terminal state, replay via proxy.
stamp=$(date +%s%N)
conversation=$(curl -sS -m 5 -X POST "$base/v1/conversations" -H 'Content-Type: application/json' -d '{}' \
  | jq -r '.id // empty')
turn=""
if [ -n "$conversation" ]; then
  turn=$(curl -sS -m 5 -X POST "$base/v1/conversations/$conversation/turns" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: d2-smoke-$stamp" \
    -d '{"query":"配送需要几个工作日？"}' | jq -r '.id // empty')
fi
state=""
if [ -n "$turn" ]; then
  for _ in $(seq 1 40); do
    state=$(curl -sS -m 5 "$base/v1/turns/$turn" | jq -r '.status // empty')
    case "$state" in
      DONE | ASKING | FAILED | CANCELLED) break ;;
    esac
    sleep 0.5
  done
fi
if [ -n "$turn" ] && [ -n "$state" ]; then
  curl -sS -N --max-time 3 "$proxy/v1/turns/$turn/events" >"$work/sse.txt" 2>/dev/null || true
fi
if [ -n "$turn" ] && grep -q '^event: ' "$work/sse.txt" 2>/dev/null && grep -q 'status' "$work/sse.txt" 2>/dev/null; then
  echo "PASS d2-sse /v1/turns/$turn/events received event frames (turn state $state)"
else
  echo "FAIL d2-sse no SSE event/status frame through the proxy (turn=${turn:-none} state=${state:-none})"
  failed=1
fi

# c) SPA fallback.
spa_code=$(curl -sS -o "$work/spa.html" -w '%{http_code}' -m 5 "$proxy/admin/nonexistent-route")
spa_type=$(curl -sS -o /dev/null -w '%{content_type}' -m 5 "$proxy/admin/nonexistent-route")
if [ "$spa_code" = "200" ] && printf '%s' "$spa_type" | grep -qi 'text/html' && grep -qi '<div id="app"' "$work/spa.html"; then
  echo 'PASS d2-spa /admin/nonexistent-route 200 text/html with SPA index'
else
  echo "FAIL d2-spa status=$spa_code type=$spa_type"
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  echo 'FAIL d2-proxy-smoke'
  exit 1
fi
echo 'PASS d2-proxy-smoke'
