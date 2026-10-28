#!/usr/bin/env bash
# Behaviour snapshot for the customer turn path.
#
# Runs the anonymous customer flow against a running gateway and prints a
# canonical, id-free summary: HTTP statuses, SSE event order, state and reason
# codes, message/citation counts and the sha256 of the final assistant text.
# Compare two runs (before/after a refactor) with diff; identical output means
# the observable behaviour did not change.
#
#   bash scripts/dev/smoke-turn.sh > .local/w2/before-smoke.txt
#
# Environment: ECR_BASE_URL (default http://127.0.0.1:8080),
# ECR_ORIGIN (must equal V2_CUSTOMER_ORIGIN, default http://127.0.0.1:5173),
# ECR_SHOP_SLUG (default shop-demo), ECR_SMOKE_QUESTION, ECR_SMOKE_WAIT seconds.
set -euo pipefail

base=${ECR_BASE_URL:-http://127.0.0.1:8080}
origin=${ECR_ORIGIN:-http://127.0.0.1:5173}
shop=${ECR_SHOP_SLUG:-shop-demo}
question=${ECR_SMOKE_QUESTION:-SKU-CUP-480 保温杯容量和材质？}
wait=${ECR_SMOKE_WAIT:-8}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
jar=$work/cookies.txt
stamp=$(date +%s%N)

status=$(curl -sS -c "$jar" -o "$work/session.json" -w '%{http_code}' \
  -X POST "$base/v1/customer/session" \
  -H 'Content-Type: application/json' -H "Origin: $origin" \
  -d "{\"shop_slug\":\"$shop\"}")
csrf=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("csrf_token",""))' "$work/session.json")
[ -n "$csrf" ] || { echo "FAIL customer session produced no csrf token"; exit 1; }

conv_status=$(curl -sS -b "$jar" -o "$work/conversation.json" -w '%{http_code}' \
  -X POST "$base/v1/customer/conversations" \
  -H 'Content-Type: application/json' -H "Origin: $origin" \
  -H "X-CSRF-Token: $csrf" -H "Idempotency-Key: smoke-conv-$stamp" -d '{}')
conversation=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("conversation_id",""))' "$work/conversation.json")
[ -n "$conversation" ] || { echo "FAIL conversation was not created"; exit 1; }

message_status=$(curl -sS -b "$jar" -o "$work/message.json" -w '%{http_code}' \
  -X POST "$base/v1/customer/conversations/$conversation/messages" \
  -H 'Content-Type: application/json' -H "Origin: $origin" \
  -H "X-CSRF-Token: $csrf" -H "Idempotency-Key: smoke-msg-$stamp" \
  -d "$(python3 -c 'import json,sys;print(json.dumps({"text": sys.argv[1]}, ensure_ascii=False))' "$question")")

timeout "$wait" curl -sS -N -b "$jar" "$base/v1/customer/conversations/$conversation/events" > "$work/events.txt" || true

python3 - "$work/events.txt" "$status" "$conv_status" "$message_status" <<'PY'
import hashlib, json, sys

path, session_status, conversation_status, message_status = sys.argv[1:5]
events, states, reasons, messages = [], [], [], []
with open(path, encoding="utf-8") as handle:
    name = None
    for line in handle:
        line = line.rstrip("\n")
        if line.startswith("event: "):
            name = line[7:]
            events.append(name)
        elif line.startswith("data: "):
            try:
                payload = json.loads(line[6:])
            except json.JSONDecodeError:
                continue
            if name == "state":
                states.append(payload.get("state", ""))
                if payload.get("reason_code"):
                    reasons.append(payload["reason_code"])
            elif name == "message":
                message = payload.get("message") or {}
                messages.append({"source": message.get("source"), "text": message.get("text") or "",
                                 "citations": len(message.get("citations") or [])})
answer = "".join(m["text"] for m in messages if m["source"] in {"ai", "system"})
print(f"session_status={session_status}")
print(f"conversation_status={conversation_status}")
print(f"message_status={message_status}")
print("events=" + ",".join(events))
print("states=" + ",".join(states))
print("reason_codes=" + ",".join(reasons))
print(f"messages={len(messages)}")
print(f"citations={sum(m['citations'] for m in messages)}")
print("answer_sha256=" + hashlib.sha256(answer.encode()).hexdigest())
PY
