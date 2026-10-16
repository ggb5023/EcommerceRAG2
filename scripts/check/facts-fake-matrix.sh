#!/usr/bin/env bash
set -euo pipefail

# Process-level acceptance for the loopback merchant facts fake. It never
# connects to PostgreSQL, Redis, a real merchant API, or an online model.
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/ecr-facts-fake.XXXXXX")
chmod 700 "$tmp"
binary="$tmp/facts-fake"
port=$((18090 + $$ % 700))
pid=''

cleanup() {
  set +e
  [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

(cd "$repo/go" && go build -p 2 -o "$binary" ./cmd/facts-fake)

start_fake() {
  local fault=${1:-}
  FACTS_FAKE_ADDR="127.0.0.1:$port" FACTS_FAKE_VERSION=2026-10 FACTS_FAKE_FAULT="$fault" \
    "$binary" >"$tmp/fake.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 80); do
    if curl --silent --show-error --fail "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.05
  done
  echo "FAIL facts fake did not become ready" >&2
  return 1
}

stop_fake() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    pid=''
  fi
}

request_status() {
  local tool=$1
  local expected=$2
  local actual
  actual=$(curl --silent --show-error --output "$tmp/body" --write-out '%{http_code}' \
    -H 'X-Tenant-ID: v2-demo-tenant' -H 'X-User-ID: dev-user' -H 'X-Shop-ID: shop-demo' \
    -H 'X-Fact-Resource-ID: V2-CUP-480' \
    "http://127.0.0.1:$port/v1/facts/$tool")
  [[ "$actual" == "$expected" ]] || {
    echo "FAIL facts fake $tool status=$actual expected=$expected" >&2
    return 1
  }
}

start_fake
request_status get_stock 200
python3 - "$tmp/body" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1]))
assert payload["version"] == "2026-10"
assert payload["tenant_id"] == "v2-demo-tenant"
assert payload["shop_id"] == "shop-demo"
assert payload["tool_name"] == "get_stock"
assert payload["is_mock"] is True
assert payload["value_json"]["available"] == 12
assert payload["observed_at"] == "2026-10-12T00:00:00Z"
PY
# The request above intentionally uses the valid scope; exercise revocation as
# an ownership failure with a different shop header.
actual=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H 'X-Tenant-ID: v2-demo-tenant' -H 'X-User-ID: dev-user' -H 'X-Shop-ID: revoked-shop' \
  "http://127.0.0.1:$port/v1/facts/get_stock")
[[ "$actual" == 403 ]] || { echo "FAIL revoked scope status=$actual" >&2; exit 1; }
stop_fake

for row in \
  'empty|get_stock|200' \
  'malformed|get_stock|200' \
  'wrong_owner|get_stock|200' \
  'unauthenticated|get_stock|401' \
  'unauthorized|get_stock|403' \
  'rate_limited|get_stock|429' \
  'server_error|get_stock|502' \
  'partial|get_price|502'; do
  IFS='|' read -r fault tool expected <<<"$row"
  start_fake "$fault"
  request_status "$tool" "$expected"
  if [[ "$expected" == 200 ]]; then
    python3 - "$fault" "$tmp/body" <<'PY'
import json
import sys

fault, path = sys.argv[1:]
payload = json.load(open(path))
if fault == "empty":
    assert payload["value_json"] is None
elif fault == "malformed":
    assert payload.get("version") == "wrong"
elif fault == "wrong_owner":
    assert payload.get("shop_id") == "other-shop"
PY
  fi
  stop_fake
done

start_fake timeout
if curl --silent --show-error --max-time 0.2 \
  -H 'X-Tenant-ID: v2-demo-tenant' -H 'X-User-ID: dev-user' -H 'X-Shop-ID: shop-demo' \
  "http://127.0.0.1:$port/v1/facts/get_stock" >"$tmp/timeout-body" 2>/dev/null; then
  echo 'FAIL facts fake timeout unexpectedly completed' >&2
  exit 1
fi
curl --silent --show-error --fail "http://127.0.0.1:$port/healthz" >/dev/null
stop_fake

echo 'PASS facts fake HTTP fault matrix (normal, empty, 401/403, 429, 5xx, timeout, ownership, partial)'
echo 'real_service_acceptance=false'
