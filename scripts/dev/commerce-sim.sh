#!/usr/bin/env bash
# Start/stop the loopback commerce simulator for dev and smoke checks.
#
#   bash scripts/dev/commerce-sim.sh [start|stop|status] [--addr HOST:PORT]
#
# The binary is built from go/cmd/commerce-sim; the dataset defaults to
# data/synthetic/commerce-lab-v1. Loopback addresses only.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
action=start
addr=${ECR_COMMERCE_SIM_ADDR:-127.0.0.1:8091}
while [[ $# -gt 0 ]]; do
  case "$1" in
    start | stop | status) action=$1; shift ;;
    --addr) addr=$2; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
root=${ECR_COMMERCE_SIM_ROOT:-$repo/.local}
pidfile="$root/commerce-sim.pid"
logfile="$root/commerce-sim.log"
binary="$root/commerce-sim"
mkdir -p "$root"
chmod 700 "$root"

alive() {
  [[ -f "$pidfile" ]] || return 1
  local pid
  pid=$(cat "$pidfile")
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null &&
    grep -q "commerce-sim" "/proc/$pid/cmdline" 2>/dev/null
}

case "$action" in
  status)
    if alive; then echo "RUNNING commerce-sim $(cat "$pidfile") $addr"; else echo "STOPPED commerce-sim"; exit 1; fi
    ;;
  stop)
    if alive; then
      pid=$(cat "$pidfile")
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 50); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
      done
      kill -9 "$pid" 2>/dev/null || true
      echo "PASS commerce-sim stopped"
    else
      echo "PASS commerce-sim already stopped"
    fi
    rm -f "$pidfile"
    ;;
  start)
    if alive; then
      echo "PASS commerce-sim already running $(cat "$pidfile")"
      exit 0
    fi
    if (echo > "/dev/tcp/${addr%:*}/${addr##*:}") 2>/dev/null; then
      echo "FAIL $addr is already in use" >&2
      exit 2
    fi
    go -C "$repo/go" build -o "$binary" ./cmd/commerce-sim
    setsid nohup "$binary" --dataset "$repo/data/synthetic/commerce-lab-v1" --addr "$addr" \
      >>"$logfile" 2>&1 &
    echo $! >"$pidfile"
    for _ in $(seq 1 100); do
      if curl -fsS -m 1 "http://$addr/healthz" >/dev/null 2>&1; then
        echo "PASS commerce-sim $addr (pid $(cat "$pidfile"), log $logfile)"
        exit 0
      fi
      sleep 0.1
    done
    echo "FAIL commerce-sim did not become ready; see $logfile" >&2
    tail -n 20 "$logfile" >&2 || true
    exit 1
    ;;
esac
