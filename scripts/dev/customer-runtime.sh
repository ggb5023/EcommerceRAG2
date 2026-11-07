#!/usr/bin/env bash
# Controlled selfhost runtime for the customer/keycloak browser specs.
#
#   bash scripts/dev/customer-runtime.sh up [--fresh]
#   bash scripts/dev/customer-runtime.sh status
#   bash scripts/dev/customer-runtime.sh run
#   bash scripts/dev/customer-runtime.sh run-spec NAME
#   bash scripts/dev/customer-runtime.sh restore
#   bash scripts/dev/customer-runtime.sh down [--clean]
#
# Requires root (the protected runtime directory and selfhost provisioning),
# Docker, the real provider config at /etc/ecommerce-rag/providers.env and a
# Playwright Chromium. Exit 2 means UNCONFIGURED; it never means PASS.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
exec python3 "$repo/scripts/dev/customer_runtime.py" "$@"
