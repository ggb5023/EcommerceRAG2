#!/usr/bin/env bash
# Scenario: a vague question that must end in ASKING with a clarification.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=scripts/dev/smoke-scenario.sh
. "$repo/scripts/dev/smoke-scenario.sh"
run_scenario clarify "${ECR_SMOKE_QUESTION:-那个怎么样}"
