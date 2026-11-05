#!/usr/bin/env bash
# Tenant-scoped retention for append-only event/trace tables.
#
# Default is a read-only dry-run. Pass --execute explicitly to delete. Only the
# fixed allowlist below is touched; usage_log, admin_audit and local_auth_event
# are deliberately excluded. The connection must be an owner/migration role
# (ECR_RETENTION_DSN); production should switch to a least-privilege role.
set -euo pipefail

dry_run=1
days=90
tenant=""
all_tenants=0
tables=(
  sse_event
  customer_retrieval_trace
  customer_event
  retrieval_trace
  tool_call_trace
  routing_trace
  ingest_event
)

usage() {
  cat <<'USAGE'
usage: ECR_RETENTION_DSN='...' bash scripts/ops/retention.sh [options]

  --dry-run          count rows that would be deleted (default)
  --execute          actually delete rows (requires explicit opt-in)
  --days N           age threshold in days (default 90)
  --tenant ID        restrict to one tenant
  --all-tenants      process every tenant
  -h, --help         show this help

Exit codes: 0 done, 1 failure, 2 UNCONFIGURED (missing DSN).
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1; shift ;;
    --execute) dry_run=0; shift ;;
    --days) days=${2:-}; shift 2 ;;
    --tenant) tenant=${2:-}; shift 2 ;;
    --all-tenants) all_tenants=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

if ! [[ "$days" =~ ^[1-9][0-9]*$ ]]; then
  printf 'FAIL days must be a positive integer\n' >&2
  exit 2
fi
if [[ "$all_tenants" -eq 1 && -n "$tenant" ]]; then
  printf 'FAIL choose --tenant or --all-tenants, not both\n' >&2
  exit 2
fi
if [[ "$all_tenants" -eq 1 ]]; then
  filter='TRUE'
elif [[ -n "$tenant" ]]; then
  if ! [[ "$tenant" =~ ^[0-9]+$ ]]; then
    printf 'FAIL tenant must be a numeric id\n' >&2
    exit 2
  fi
  filter="tenant_id = $tenant"
else
  printf 'FAIL --tenant or --all-tenants is required\n' >&2
  exit 2
fi
if [[ -z "${ECR_RETENTION_DSN:-}" ]]; then
  printf 'UNCONFIGURED retention: ECR_RETENTION_DSN is required (owner/migration role)\n'
  exit 2
fi
if ! command -v psql >/dev/null 2>&1; then
  printf 'FAIL retention: psql is unavailable\n' >&2
  exit 1
fi

cutoff="now() - interval '$days days'"
printf 'RETENTION mode=%s days=%s scope=%s\n' \
  "$([[ "$dry_run" -eq 1 ]] && printf dry-run || printf execute)" \
  "$days" "$([[ "$all_tenants" -eq 1 ]] && printf all-tenants || printf "tenant:$tenant")"
printf 'PROTECTED usage_log admin_audit local_auth_event\n'

if [[ "$dry_run" -eq 1 ]]; then
  total=0
  for table in "${tables[@]}"; do
    count=$(psql "$ECR_RETENTION_DSN" -X -Atq -v ON_ERROR_STOP=1 \
      -c "SELECT count(*) FROM $table WHERE $filter AND created_at < $cutoff")
    printf 'DRY-RUN %-30s %s\n' "$table" "$count"
    total=$((total + count))
  done
  printf 'DRY-RUN total %s\n' "$total"
  exit 0
fi

sql='BEGIN;'
# All targets are leaf tables: parent turn/execution rows are retained.
for table in "${tables[@]}"; do
  sql+="WITH deleted AS (DELETE FROM $table WHERE $filter AND created_at < $cutoff RETURNING 1) SELECT '$table', count(*) FROM deleted;"
done
sql+='COMMIT;'
result=$(psql "$ECR_RETENTION_DSN" -X -Atq -v ON_ERROR_STOP=1 -c "$sql")
total=0
while IFS='|' read -r table count; do
  [[ -z "$table" ]] && continue
  printf 'DELETED %-30s %s\n' "$table" "$count"
  total=$((total + count))
done <<< "$result"
printf 'DELETED total %s\n' "$total"
