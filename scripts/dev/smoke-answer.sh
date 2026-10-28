#!/usr/bin/env bash
# Behaviour snapshot for the staff turn path that actually answers.
#
# The customer channel refuses mock output by design, so the answer baseline is
# taken on the staff API: create a conversation, run one turn, replay the SSE
# stream and read the persisted turn. Output is id-free so two runs can be
# compared with diff.
#
#   bash scripts/dev/smoke-answer.sh > .local/w2/before-answer-smoke.txt
#
# Environment: ECR_BASE_URL (default http://127.0.0.1:8080),
# ECR_SMOKE_QUESTION, ECR_SMOKE_WAIT seconds.
set -euo pipefail

base=${ECR_BASE_URL:-http://127.0.0.1:8080}
question=${ECR_SMOKE_QUESTION:-配送需要几个工作日？}
wait=${ECR_SMOKE_WAIT:-20}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
stamp=$(date +%s%N)

conversation_status=$(curl -sS -o "$work/conversation.json" -w '%{http_code}' \
  -X POST "$base/v1/conversations" -H 'Content-Type: application/json' -d '{}')
conversation=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("id",""))' "$work/conversation.json")
[ -n "$conversation" ] || { echo "FAIL conversation was not created"; exit 1; }

turn_status=$(curl -sS -o "$work/turn.json" -w '%{http_code}' \
  -X POST "$base/v1/conversations/$conversation/turns" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: smoke-answer-$stamp" \
  -d "$(python3 -c 'import json,sys;print(json.dumps({"query": sys.argv[1]}, ensure_ascii=False))' "$question")")
turn=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("id",""))' "$work/turn.json")
[ -n "$turn" ] || { echo "FAIL turn was not accepted"; exit 1; }

timeout "$wait" curl -sS -N "$base/v1/turns/$turn/events" > "$work/events.txt" || true
curl -sS "$base/v1/turns/$turn" > "$work/result.json"

python3 - "$work/events.txt" "$work/result.json" "$conversation_status" "$turn_status" <<'PY'
import hashlib, json, sys

events_path, result_path, conversation_status, turn_status = sys.argv[1:5]
names, evidence_ids, statuses = [], [], []
with open(events_path, encoding="utf-8") as handle:
    name = None
    for line in handle:
        line = line.rstrip("\n")
        if line.startswith("event: "):
            name = line[7:]
            names.append(name)
        elif line.startswith("data: ") and name == "evidence":
            payload = json.loads(line[6:])
            evidence_ids.extend(item.get("id", "") for item in payload.get("evidence") or [])
        elif line.startswith("data: ") and name == "status":
            payload = json.loads(line[6:])
            statuses.append(payload.get("status", ""))

result = json.load(open(result_path, encoding="utf-8"))
answer = result.get("answer") or ""
citations = result.get("citations") or []
structure = json.dumps({"events": names, "evidence": sorted(evidence_ids),
                        "citations": len(citations), "status": result.get("status", "")}, sort_keys=True)
print(f"conversation_status={conversation_status}")
print(f"turn_status={turn_status}")
print(f"turn_state={result.get('status', '')}")
print("events=" + ",".join(names))
print("statuses=" + ",".join(statuses))
print("evidence_ids=" + ",".join(sorted(evidence_ids)))
print(f"citations={len(citations)}")
print(f"answer_chars={len(answer)}")
print("answer_sha256=" + hashlib.sha256(answer.encode()).hexdigest())
print("structure_sha256=" + hashlib.sha256(structure.encode()).hexdigest())
PY
