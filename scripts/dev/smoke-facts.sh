#!/usr/bin/env bash
# Scenario: a SKU question that must go through the authorized facts branch.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=scripts/dev/smoke-scenario.sh
. "$repo/scripts/dev/smoke-scenario.sh"
run_scenario facts "${ECR_SMOKE_QUESTION:-SKU-CUP-480 保温杯现在有库存吗？价格多少？}"
