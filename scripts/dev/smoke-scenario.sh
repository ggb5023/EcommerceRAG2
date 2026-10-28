#!/usr/bin/env bash
# Shared runner for one staff-turn scenario.
#
# A scenario script sets SCENARIO and QUESTION, then calls run_scenario.
# Output is canonical and id-free so two runs can be compared with diff.
set -euo pipefail

base=${ECR_BASE_URL:-http://127.0.0.1:8080}
wait=${ECR_SMOKE_WAIT:-20}

run_scenario() {
  local scenario=$1 question=$2
  local work
  work=$(mktemp -d)
  local stamp
  stamp=$(date +%s%N)

  local conversation_status turn_status
  conversation_status=$(curl -sS -o "$work/conversation.json" -w '%{http_code}' \
    -X POST "$base/v1/conversations" -H 'Content-Type: application/json' -d '{}')
  local conversation
  conversation=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("id",""))' "$work/conversation.json")
  [ -n "$conversation" ] || { echo "FAIL conversation was not created"; rm -rf "$work"; return 1; }

  turn_status=$(curl -sS -o "$work/turn.json" -w '%{http_code}' \
    -X POST "$base/v1/conversations/$conversation/turns" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: smoke-$scenario-$stamp" \
    -d "$(python3 -c 'import json,sys;print(json.dumps({"query": sys.argv[1]}, ensure_ascii=False))' "$question")")
  local turn
  turn=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("id",""))' "$work/turn.json")
  [ -n "$turn" ] || { echo "FAIL turn was not accepted"; cat "$work/turn.json"; rm -rf "$work"; return 1; }

  timeout "$wait" curl -sS -N "$base/v1/turns/$turn/events" > "$work/events.txt" || true
  curl -sS "$base/v1/turns/$turn" > "$work/result.json"

  python3 - "$work/events.txt" "$work/result.json" "$conversation_status" "$turn_status" "$scenario" "$question" <<'PY'
import hashlib, json, sys

events_path, result_path, conversation_status, turn_status, scenario, question = sys.argv[1:7]
names, statuses, facts, evidence_ids = [], [], [], []
with open(events_path, encoding="utf-8") as handle:
    name = None
    for line in handle:
        line = line.rstrip("\n")
        if line.startswith("event: "):
            name = line[7:]
            names.append(name)
        elif line.startswith("data: "):
            try:
                payload = json.loads(line[6:])
            except json.JSONDecodeError:
                continue
            if name == "status":
                statuses.append(payload.get("status", ""))
            elif name == "fact_read":
                facts.append(payload.get("tool_name", ""))
            elif name == "evidence":
                evidence_ids.extend(item.get("id", "") for item in payload.get("evidence") or [])

result = json.load(open(result_path, encoding="utf-8"))
answer = result.get("answer") or ""
citations = result.get("citations") or []
structure = json.dumps({"events": names, "facts": sorted(facts), "evidence": sorted(evidence_ids),
                        "citations": len(citations), "status": result.get("status", "")}, sort_keys=True)
print(f"scenario={scenario}")
print(f"question={question}")
print(f"conversation_status={conversation_status}")
print(f"turn_status={turn_status}")
print(f"turn_state={result.get('status', '')}")
print("events=" + ",".join(names))
print("statuses=" + ",".join(statuses))
print("facts=" + ",".join(sorted(facts)))
print("evidence_ids=" + ",".join(sorted(evidence_ids)))
print(f"citations={len(citations)}")
print(f"answer_chars={len(answer)}")
print("answer_sha256=" + hashlib.sha256(answer.encode()).hexdigest())
print("structure_sha256=" + hashlib.sha256(structure.encode()).hexdigest())
PY
  rm -rf "$work"
}
