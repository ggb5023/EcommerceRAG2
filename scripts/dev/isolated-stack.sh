#!/usr/bin/env bash
# Isolated stack for the --db and --e2e layers.
#
#   bash scripts/dev/isolated-stack.sh [--db ENV] [--ports BASE] [--with-vite] [--keep]
#   bash scripts/dev/isolated-stack.sh stop
#
# The --db file supplies the DSNs (default: the integration database). The
# remaining non-secret options come from --template; ports are always overridden
# from --ports so the stack never collides with the dev stack.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
root=${ECR_ISOLATED_ROOT:-/tmp/ecr-isolated}
ports_file="$root/ports.json"
python="$repo/python/.venv/bin/python"

usage() { sed -n '2,9p' "${BASH_SOURCE[0]}" >&2; exit 2; }

stop_stack() {
  if [[ ! -f "$ports_file" ]]; then
    echo "PASS isolated stack already stopped"
    return 0
  fi
  "$python" - "$ports_file" <<'PY'
import json, os, signal, sys, time

def ours(pid):
    """Never signal a recycled pid: it must still be one of our processes."""
    try:
        cmdline = open(f"/proc/{pid}/cmdline", "rb").read().decode(errors="replace")
    except OSError:
        return False
    return ("v2_services.py" in cmdline or "v2-local-services.sh" in cmdline
            or "vite" in cmdline or "npm run dev" in cmdline)

state = json.load(open(sys.argv[1]))
alive = {name: pid for name, pid in state.get("pids", {}).items() if pid and ours(pid)}
for pid in alive.values():
    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
deadline = time.time() + 10
while time.time() < deadline:
    if not [pid for pid in alive.values() if os.path.exists(f"/proc/{pid}")]:
        break
    time.sleep(0.1)
for pid in alive.values():
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
PY
  rm -f "$ports_file"
  echo "PASS isolated stack stopped"
}

[[ "${1:-}" == "stop" ]] && { stop_stack; exit 0; }

db_env=/etc/ecommerce-rag/test-integration.env
template=/etc/ecommerce-rag/eval-runtime.env
base=26081
with_vite=0
keep=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db_env=$2; shift 2 ;;
    --template) template=$2; shift 2 ;;
    --ports) base=$2; shift 2 ;;
    --with-vite) with_vite=1; shift ;;
    --keep) keep=1; shift ;;
    *) usage ;;
  esac
done
for file in "$db_env" "$template"; do
  [[ -r "$file" ]] || { echo "FAIL cannot read $file" >&2; exit 2; }
done
[[ -x "$python" ]] || { echo "FAIL python/.venv missing; run uv sync --frozen" >&2; exit 2; }
stop_stack >/dev/null 2>&1 || true
mkdir -p "$root"
chmod 700 "$root"

for port in "$base" $((base + 1)) $((base + 2)) $((base + 3)); do
  if (echo > "/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    echo "FAIL port $port is already in use; stop the previous stack first" >&2
    exit 2
  fi
done

gateway_port=$base
rag_port=$((base + 1))
ingest_port=$((base + 2))
vite_port=$((base + 3))

# Build the merged runtime configuration: template options, DSNs from --db,
# ports from --ports. Secrets stay in $root/runtime.env (0600).
"$python" - "$db_env" "$template" "$root/runtime.env" "$gateway_port" "$rag_port" "$ingest_port" "$vite_port" "$repo" <<'PY'
import pathlib, sys
db_env, template, target = (pathlib.Path(arg) for arg in sys.argv[1:4])
repo = pathlib.Path(sys.argv[8])
sys.path.insert(0, str(repo / "scripts" / "dev"))
from v2_services import CONFIG_KEYS
gateway_port, rag_port, ingest_port, vite_port = sys.argv[4:8]
values = {}
for line in template.read_text().splitlines():
    if "=" in line and not line.lstrip().startswith("#"):
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip('"\'')
for line in db_env.read_text().splitlines():
    if "=" in line and not line.lstrip().startswith("#"):
        key, value = line.split("=", 1)
        key = key.strip()
        if key.startswith("V2_") and key.endswith("_DSN"):
            values[key] = value.strip().strip('"\'')
# All databases live on one cluster, so the template DSNs are retargeted at the
# database named by the --db file instead of duplicating credentials here.
def dbname(dsn: str) -> str:
    if "dbname=" in dsn:
        return dsn.split("dbname=")[1].split()[0].strip("'\"")
    return dsn.rsplit("/", 1)[-1].split("?")[0]

target_db = dbname(values.get("V2_GATEWAY_DSN", ""))
if target_db:
    for key, value in list(values.items()):
        if key.endswith("_DSN") and key != "V2_MIGRATION_DSN":
            if "dbname=" in value:
                head, _, rest = value.partition("dbname=")
                tail = rest.split(None, 1)
                values[key] = head + "dbname=" + target_db + ((" " + tail[1]) if len(tail) > 1 else "")
            else:
                base_path, _, query = value.rpartition("/")
                values[key] = base_path + "/" + target_db + (("?" + query) if query else "")

values["RAG_PROFILE"] = "v2_langchain_mock"
values["HTTP_ADDR"] = f"127.0.0.1:{gateway_port}"
values["V2_GRPC_ADDR"] = f"127.0.0.1:{rag_port}"
values["V2_INGEST_GRPC_ADDR"] = f"127.0.0.1:{ingest_port}"
values["AUTHORITY_HTTP_BASE"] = f"http://127.0.0.1:{gateway_port}"
values["V2_CUSTOMER_ORIGIN"] = f"http://127.0.0.1:{vite_port}"
values.setdefault("APP_ENV", "development")
out = target.open("w")
for key, value in sorted(values.items()):
    # The supervisor rejects keys it does not know, so drop template extras.
    if key in CONFIG_KEYS:
        out.write(f"{key}={value}\n")
out.close()
target.chmod(0o600)
PY

# The browser specs need the stack's database and a fixed local password.
browser_env="$root/browser.env"
migration_dsn=$(sed -n "s/^V2_MIGRATION_DSN=//p" "$db_env" | head -1 | tr -d "\"'")
{
  printf 'V2_MIGRATION_DSN=%s\n' "$migration_dsn"
  printf 'LOGIN_PASSWORD=%s\n' "${ECR_BROWSER_LOGIN_PASSWORD:-stack-login-Passw0rd!}"
  printf 'WORKSPACE_URL=http://127.0.0.1:%s\n' "$vite_port"
} > "$browser_env"
chmod 600 "$browser_env"

log="$root/stack.log"
: > "$log"
V2_SERVICE_LOG_DIR="$root/logs" setsid nohup bash "$repo/scripts/dev/v2-local-services.sh" --env "$root/runtime.env" >>"$log" 2>&1 &
supervisor=$!
echo "$supervisor" > "$root/supervisor.pid"

fail() {
  echo "FAIL $1" >&2
  echo "--- last 30 log lines ($log) ---" >&2
  tail -n 30 "$log" >&2 || true
  stop_stack >/dev/null 2>&1 || true
  [[ $keep -eq 1 ]] || rm -rf "$root"
  exit 1
}

deadline=$(( $(date +%s) + 120 ))
gateway_ready() {
  curl -sS -o /dev/null -m 1 "http://127.0.0.1:$gateway_port/healthz" 2>/dev/null &&
    [ "$(curl -sS -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$gateway_port/v1/session" 2>/dev/null)" = "200" ]
}
until gateway_ready; do
  kill -0 "$supervisor" 2>/dev/null || fail "supervisor exited before the gateway became ready"
  [[ $(date +%s) -lt $deadline ]] || fail "gateway did not answer /healthz and /v1/session on port $gateway_port"
  sleep 1
done
until (echo > "/dev/tcp/127.0.0.1/$ingest_port") 2>/dev/null; do
  kill -0 "$supervisor" 2>/dev/null || fail "supervisor exited before the ingest RPC became ready"
  [[ $(date +%s) -lt $deadline ]] || fail "ingest RPC did not become ready on port $ingest_port"
  sleep 1
done

vite_pid=0
if [[ $with_vite -eq 1 ]]; then
  # Vite must proxy to this stack's gateway, not the default dev gateway.
  ( cd "$repo/frontend" && VITE_API_PROXY_TARGET="http://127.0.0.1:$gateway_port" \
      setsid nohup npm run dev -- --port "$vite_port" --strictPort >>"$log" 2>&1 & echo $! > "$root/vite.pid" )
  vite_pid=$(cat "$root/vite.pid")
  deadline=$(( $(date +%s) + 90 ))
  until curl -sS -o /dev/null -m 1 "http://127.0.0.1:$vite_port/" 2>/dev/null; do
    [[ $(date +%s) -lt $deadline ]] || fail "vite did not become ready on port $vite_port"
    sleep 1
  done
fi

"$python" - "$ports_file" "$root" "$supervisor" "$vite_pid" "$gateway_port" "$rag_port" "$ingest_port" "$vite_port" <<'PY'
import json, sys
target, root, supervisor, vite_pid, gateway, rag, ingest, vite = sys.argv[1:9]
state = {
    "root": root,
    "gateway": f"127.0.0.1:{gateway}",
    "rag": f"127.0.0.1:{rag}",
    "ingest": f"127.0.0.1:{ingest}",
    "vite": f"127.0.0.1:{vite}" if int(vite_pid) else "",
    "pids": {"supervisor": int(supervisor), "vite": int(vite_pid)},
    "log": f"{root}/stack.log",
}
open(target, "w").write(json.dumps(state, indent=2, sort_keys=True) + "\n")
PY

echo "PASS isolated stack ready"
"$python" - "$ports_file" <<'PY'
import json, sys
state = json.load(open(sys.argv[1]))
for name in ("gateway", "rag", "ingest", "vite"):
    if state[name]:
        print(f"  {name:8s} {state[name]}")
print(f"  ports    {sys.argv[1]}")
print(f"  log      {state['log']} (per service: {{root}}/logs/<name>.log)".replace("{root}", state["root"]))
print(f"  browser  {state['root']}/browser.env")
PY
echo "stop with: bash scripts/dev/isolated-stack.sh stop"
