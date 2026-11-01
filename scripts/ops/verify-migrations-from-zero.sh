#!/usr/bin/env bash
# Apply every migration to an empty scratch database and verify the result.
#
# Usage:
#   ECR_SCRATCH_DSN='migration-role DSN for an empty scratch database' \
#   ECR_SCRATCH_GATEWAY_DSN='gateway-role DSN for the same scratch database' \
#   bash scripts/ops/verify-migrations-from-zero.sh
#
# The scratch database must already exist (owned by the migration role) and be
# empty. Exit codes: 0 PASS, 1 FAIL, 2 UNCONFIGURED (no DSN / no venv).
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo"

dsn=${ECR_SCRATCH_DSN:-}
gateway_dsn=${ECR_SCRATCH_GATEWAY_DSN:-}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dsn) dsn=${2:-}; shift 2 ;;
    --gateway-dsn) gateway_dsn=${2:-}; shift 2 ;;
    -h | --help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [[ -z "$dsn" ]]; then
  echo 'UNCONFIGURED scratch DSN not provided (ECR_SCRATCH_DSN or --dsn)'
  exit 2
fi
python=${PYTHON:-python/.venv/bin/python}
if [[ ! -x "$python" ]]; then
  echo 'UNCONFIGURED python venv is missing'
  exit 2
fi

# The migration role owns the scratch database; the vector extensions must exist
# before 0037 runs. A database administrator normally creates them first; the
# migration role can only add them when it has the required privilege.
extensions=$(psql "$dsn" -X -At -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM pg_extension WHERE extname IN ('vector','pg_trgm')")
if [[ "$extensions" != "2" ]]; then
  if ! psql "$dsn" -X -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION IF NOT EXISTS vector; CREATE EXTENSION IF NOT EXISTS pg_trgm' >/dev/null 2>&1; then
    echo 'FAIL scratch database needs vector and pg_trgm; ask the database administrator to create them'
    exit 1
  fi
fi

tmp=$(mktemp)
chmod 600 "$tmp"
trap 'rm -f "$tmp"' EXIT
printf 'V2_MIGRATION_DSN=%s\n' "$dsn" >"$tmp"
if ! "$python" scripts/ops/v2-migrate.py --env "$tmp"; then
  echo 'FAIL migrations from zero did not complete'
  exit 1
fi

files=(sql/migrations/[0-9][0-9][0-9][0-9]_*.sql)
rows=$(psql "$dsn" -X -At -v ON_ERROR_STOP=1 -c 'SELECT count(*) FROM schema_migration')
if [[ "$rows" != "${#files[@]}" ]]; then
  echo "FAIL schema_migration rows $rows != migration files ${#files[@]}"
  exit 1
fi

mapfile -t recorded < <(psql "$dsn" -X -At -F '|' -v ON_ERROR_STOP=1 -c 'SELECT version,checksum_sha256 FROM schema_migration ORDER BY version')
for row in "${recorded[@]}"; do
  version=${row%%|*}
  checksum=${row#*|}
  file=$(ls sql/migrations/"${version}"_*.sql 2>/dev/null | head -1)
  actual=$(sha256sum "$file" | awk '{print $1}')
  if [[ "$checksum" != "$actual" ]]; then
    echo "FAIL checksum mismatch for migration $version"
    exit 1
  fi
done

for table in schema_migration tenant app_user conversation turn conversation_execution turn_decision document document_version chunk; do
  exists=$(psql "$dsn" -X -At -v ON_ERROR_STOP=1 -c "SELECT to_regclass('public.$table') IS NOT NULL")
  if [[ "$exists" != "t" ]]; then
    echo "FAIL missing table $table"
    exit 1
  fi
done

if [[ -z "$gateway_dsn" ]]; then
  echo 'UNCONFIGURED gateway DSN not provided (ECR_SCRATCH_GATEWAY_DSN or --gateway-dsn)'
  exit 2
fi

# The migration seeds v2-demo-tenant/dev-user; create a conversation for the
# grant probe, then prove rag_v2_gateway can insert a turn inside a rolled-back
# transaction.
psql "$dsn" -X -v ON_ERROR_STOP=1 -c "INSERT INTO conversation(tenant_id,user_id,title)
  SELECT t.id,u.id,'scratch-grant' FROM tenant t JOIN app_user u ON u.tenant_id=t.id
  WHERE t.external_id='v2-demo-tenant' AND u.external_id='dev-user' LIMIT 1" >/dev/null
seeded=$(psql "$dsn" -X -At -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM conversation WHERE title='scratch-grant'")
if [[ "$seeded" != "1" ]]; then
  echo 'FAIL could not seed the grant-probe conversation'
  exit 1
fi
if ! psql "$gateway_dsn" -X -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
BEGIN;
INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status)
SELECT t.id,c.id,1,'scratch-grant','scratch grant','EXECUTING'
FROM tenant t JOIN conversation c ON c.tenant_id=t.id
WHERE t.external_id='v2-demo-tenant' AND c.title='scratch-grant' LIMIT 1;
ROLLBACK;
SQL
then
  echo 'FAIL rag_v2_gateway cannot INSERT turn after migrations'
  exit 1
fi

echo "PASS migrations from zero: ${#files[@]} files, checksums, key tables and gateway INSERT grant"
