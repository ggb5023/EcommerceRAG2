#!/usr/bin/env bash
# One-click commerce-lab dev shop: isolated stack + commerce simulator + the
# knowledge package from data/synthetic/commerce-lab-v1 imported and published.
#
#   bash scripts/dev/commerce-lab.sh [--ports 26081] [--with-vite] [--reset]
#
# The stack binds loopback only; the simulator serves the facts contract on
# ports+4 and never joins a selfhost deployment.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
ports=26081
with_vite=0
reset=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ports) ports=${2:-}; shift 2 ;;
    --with-vite) with_vite=1; shift ;;
    --reset) reset=1; shift ;;
    -h | --help) sed -n '2,9p' "${BASH_SOURCE[0]}" >&2; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ "$ports" =~ ^[0-9]+$ ]] || { echo "FAIL --ports must be numeric" >&2; exit 2; }
package="$repo/data/synthetic/commerce-lab-v1"
[[ -r "$package/manifest.yaml" ]] || { echo "FAIL dataset missing: $package" >&2; exit 2; }
python3 "$repo/scripts/dev/generate_commerce_dataset.py" --check

stack_args=(--identity development --ports "$ports" --commerce-sim --keep)
[[ $with_vite -eq 1 ]] && stack_args+=(--with-vite)
bash "$repo/scripts/dev/isolated-stack.sh" "${stack_args[@]}"

demo_args=(--ports "$ports" --package "$package")
[[ $reset -eq 1 ]] && demo_args+=(--reset)
bash "$repo/scripts/dev/demo-shop.sh" "${demo_args[@]}"

commerce_port=$((ports + 4))
cat <<EOF
PASS commerce-lab ready
  gateway          : http://127.0.0.1:$ports
  commerce facts   : http://127.0.0.1:$commerce_port/v1/facts/get_stock
  dev REST orders  : http://127.0.0.1:$commerce_port/dev/v1/orders
  dev REST tickets : http://127.0.0.1:$commerce_port/dev/v1/tickets
EOF
[[ $with_vite -eq 1 ]] && echo "  merchant console : http://127.0.0.1:$((ports + 3))/admin"
