#!/usr/bin/env bash
set -euo pipefail
config=${ECR_DEV_ENV:-/etc/ecommerce-rag/dev.env}
command -v psql >/dev/null || { echo "UNVERIFIED missing psql"; exit 2; }
[[ -r "$config" ]] || { echo "UNVERIFIED credentials file unavailable"; exit 2; }
set -a
# shellcheck source=/dev/null
source "$config"
set +a
export PGCONNECT_TIMEOUT=5
failed=0
result=$(psql -X -w -At -v ON_ERROR_STOP=1 -c "SELECT current_user || ':' || rolsuper FROM pg_roles WHERE rolname=current_user" 2>/dev/null) || result=FAILED
if [[ "$result" == rag_app:false ]]; then echo "PASS PostgreSQL app role"; else echo "FAIL PostgreSQL app role"; failed=1; fi
version=$(psql -X -w -At -c "SELECT extversion FROM pg_extension WHERE extname='vector' AND string_to_array(extversion,'.')::int[] >= ARRAY[0,8,0]" 2>/dev/null) || version=
if [[ -n "$version" ]]; then echo "PASS pgvector $version"; else echo "FAIL pgvector version"; failed=1; fi
exit "$failed"
