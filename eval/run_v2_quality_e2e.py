#!/usr/bin/env python3
"""Run the frozen evaluation cases through the Go V2 HTTP entrypoint.

The lifecycle runner must first publish the approved 37-document corpus.  This
driver only creates conversations and turns, then writes metadata-only
results. It never stores query text, answers, snippets, cookies, or credentials.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "eval"))
from quality_capture import capture, secure_directory

DEFAULT_REVISION_ROOT = Path("/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional")
DEFAULT_CASES = DEFAULT_REVISION_ROOT / "synthetic-m2-v1-revision-1.jsonl"
DEFAULT_SCENARIOS = ROOT / "eval/quality-scenarios.v1.json"


class E2EFailure(RuntimeError):
    pass


def load_scenarios(path: Path, cases_path: Path, cases: list[dict]) -> dict[str, list[str]]:
    value = json.loads(path.read_bytes())
    if (value.get("scenario_version") != "v2-quality-conversation-v1"
            or value.get("cases_sha256") != hashlib.sha256(cases_path.read_bytes()).hexdigest()):
        raise E2EFailure("scenario_baseline_mismatch")
    by_id = {case["case_id"]: case for case in cases}
    scenarios = {}
    for item in value["scenarios"]:
        identifier = item["case_id"]
        queries = item["prerequisite_queries"]
        if (identifier in scenarios or identifier not in by_id
                or item["query_sha256"] != digest(by_id[identifier]["query"])
                or not isinstance(queries, list) or not 1 <= len(queries) <= 3
                or any(not isinstance(query, str) or not query.strip() for query in queries)):
            raise E2EFailure("scenario_invalid")
        scenarios[identifier] = queries
    return scenarios


def digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    rows = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            value = json.loads(line)
            if not isinstance(value, dict):
                raise E2EFailure("evaluation case is not an object")
            rows.append(value)
    return rows


def runtime_identity(path: Path, base_url: str) -> tuple[dict[str, Any], Path]:
    sys.path.insert(0, str(ROOT / "scripts"))
    sys.path.insert(0, str(ROOT / "python"))
    from v2_services import load_values

    from app.graph.workflow import (
        CONTROL_SCHEMA,
        GENERATION_INSTRUCTION,
        GENERATION_SCHEMA,
    )
    from app.providers.config import ensure_secure_config_file, load_provider_config

    values = load_values(path)
    if values.get("RAG_PROFILE") != "v2_langchain_bailian" or base_url != "http://" + values.get("HTTP_ADDR", ""):
        raise E2EFailure("online_runtime_identity_invalid")
    config_path = Path(values["V2_PROVIDER_CONFIG"])
    ensure_secure_config_file(config_path)
    config = load_provider_config(config_path)
    if config.profile != "aliyun-bailian":
        raise E2EFailure("online_provider_profile_invalid")
    files = ("eval/run_v2_quality_e2e.py", "python/app/graph/workflow.py", "python/app/graph/service.py",
             "python/app/rpc/rag.py", "python/app/retrieval/postgres.py", "proto/rag/v1/rag.proto",
             "python/app/providers/aliyun_bailian.py", "go/internal/gateway/main.go",
             "go/internal/gateway/request_policy.go", "go/internal/gateway/fact_resource.go",
             "go/internal/facts/facts.go", "go/internal/facts/http_provider.go", "go/internal/facts/fake_server.go")
    return {"profile": config.profile, "region": config.region, "models": config.models,
            "embedding_dimensions": config.embedding_dimensions, "timeout_s": config.timeout_s, "quota_rpm": config.quota_rpm,
            "generation_instruction_sha256": digest(GENERATION_INSTRUCTION),
            "provider_schema_sha256": digest(json.dumps([CONTROL_SCHEMA, GENERATION_SCHEMA], sort_keys=True)),
            "runtime_config_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "budget_identity_sha256": digest(values["V2_ONLINE_BUDGET_ID"]),
            "business_date": values.get("SYNTHETIC_BUSINESS_DATE") or datetime.now(UTC).date().isoformat(),
            "git_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "implementation_sha256": {file: hashlib.sha256((ROOT / file).read_bytes()).hexdigest() for file in files}}, Path(values["V2_ONLINE_BUDGET_LEDGER"])


def read_retrieval_trace(runtime_env: Path, request_id: str) -> list[dict]:
    import psycopg
    from v2_services import load_values
    values = load_values(runtime_env)
    with psycopg.connect(values["V2_AI_DSN"], connect_timeout=5) as conn:
        rows = conn.execute("SELECT stage,candidates_json FROM retrieval_trace WHERE request_id=%s "
                            "AND stage IN ('v2_candidates','v2_rerank') ORDER BY id", (request_id,)).fetchall()
    return [{"stage": stage, "items": items} for stage, items in rows]


def read_context_bindings(runtime_env: Path, capture_path: Path) -> list[dict]:
    """Read-only proof: chunk fingerprint is not the SHA-256 of body text."""
    import psycopg
    from v2_services import load_values
    values = load_values(runtime_env)
    evidence = json.loads(capture_path.read_text())['result']['evidence']
    if any(not isinstance(e.get('id'), str) or not re.fullmatch(r'db-chunk-[1-9][0-9]*', e['id']) for e in evidence):
        raise E2EFailure('context_evidence_identity_invalid')
    ids = [int(e['id'].removeprefix('db-chunk-')) for e in evidence]
    if len(set(ids)) != len(ids):
        raise E2EFailure('context_evidence_identity_invalid')
    if not ids:
        return []
    with psycopg.connect(values['V2_AI_DSN'], connect_timeout=5) as conn:
        rows = conn.execute("SELECT 'db-chunk-'||c.id,d.logical_key,c.version_id::text,c.chunk_hash,c.content,v.source_hash "
                            "FROM chunk c JOIN document d ON d.id=c.doc_id AND d.tenant_id=c.tenant_id "
                            "JOIN document_version v ON v.id=c.version_id AND v.tenant_id=c.tenant_id "
                            "WHERE c.id=ANY(%s) ORDER BY c.id", (ids,)).fetchall()
    return [dict(evidence_id=i, document_id=d, version_id=v, chunk_sha256=h,
                 snippet_sha256=digest(content), source_sha256=source) for i,d,v,h,content,source in rows]


def attach_persisted_evidence(row: dict, runtime_env: Path, capture_dir: Path | None) -> None:
    """Keep failed diagnostics in the batch; never print database exceptions."""
    if not row.get('request_id'):
        return
    try:
        row['retrieval_stages'] = read_retrieval_trace(runtime_env, row['request_id'])
        if capture_dir is not None:
            row['context_bindings'] = read_context_bindings(runtime_env, capture_dir / row['capture_id'])
        row['persisted_evidence_read_failed'] = False
    except Exception:  # diagnostics cannot expose connection strings or erase prior results
        row['persisted_evidence_read_failed'] = True


def budget_snapshot(path: Path) -> dict[str, int]:
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as conn:
        count, last = conn.execute("SELECT count(*),coalesce(max(rowid),0) FROM attempt").fetchone()
    return {"attempt_count": count, "last_sequence": last}


class HTTP:
    def __init__(self, base: str):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or parsed.port is None:
            raise E2EFailure("base URL must be loopback HTTP with an explicit port")
        if parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise E2EFailure("base URL must not contain credentials or query parameters")
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, method: str, path: str, body: Mapping[str, Any] | None = None, *, key: str | None = None) -> Any:
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers = {"Content-Type": "application/json"}
        if data is not None:
            headers["Idempotency-Key"] = key or hashlib.sha256(data).hexdigest()
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=20) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise E2EFailure(f"http_{error.code}") from None
        except (OSError, ValueError) as error:
            raise E2EFailure(f"transport_{type(error).__name__}") from None

    def events(self, path: str, *, last_event_id: int = 0, timeout: float = 20.0) -> list[dict[str, Any]]:
        request = urllib.request.Request(self.base + path,
            headers={"Accept": "text/event-stream", "Last-Event-ID": str(last_event_id)})
        try:
            with self.opener.open(request, timeout=timeout) as response:
                if response.headers.get_content_type() != "text/event-stream":
                    raise E2EFailure("sse_content_type_invalid")
                events, current = [], {}
                for line in response:
                    text = line.decode("utf-8").rstrip("\r\n")
                    if not text:
                        if current:
                            events.append(current)
                            current = {}
                        continue
                    key, _, value = text.partition(":")
                    if key == "id":
                        current["id"] = value.lstrip()
                    elif key == "event":
                        current["event"] = value.lstrip()
                    elif key == "data":
                        current["data"] = value.lstrip()
                if current:
                    events.append(current)
                return events
        except urllib.error.HTTPError as error:
            raise E2EFailure(f"sse_http_{error.code}") from None
        except (OSError, UnicodeDecodeError) as error:
            raise E2EFailure(f"sse_transport_{type(error).__name__}") from None


def _document_ids(result: Mapping[str, Any]) -> list[str]:
    evidence = result.get("evidence", [])
    if not isinstance(evidence, list):
        return []
    return [str(item.get("documentId")) for item in evidence if isinstance(item, Mapping) and item.get("documentId")]


def run_case(http: HTTP, case: Mapping[str, Any], *, timeout_s: float,
             capture_dir: Path | None = None, prerequisite_queries: list[str] | None = None,
             conversation_id: str | None = None, run_id: str | None = None,
             parent_turn_id: str | None = None) -> dict[str, Any]:
    run_id = run_id or uuid4().hex
    case_id = str(case.get("case_id", ""))
    if not case_id:
        raise E2EFailure("case_id_missing")
    if conversation_id is None:
        body = {"title": "quality-eval"}
        conversation = http.call("POST", "/v1/conversations", body, key=f"quality-conversation-{run_id}-{case_id}")
        conversation_id = conversation.get("id")
    if not conversation_id:
        raise E2EFailure("conversation_id_missing")
    prerequisites = []
    for index, query in enumerate(prerequisite_queries or []):
        prior = run_case(http, {"case_id": f"{case_id}-context-{index + 1}", "query": query},
                         timeout_s=timeout_s, capture_dir=capture_dir, conversation_id=conversation_id, run_id=run_id)
        prerequisites.append(prior)
        if not case_passed(prior) or prior["status"] != "DONE":
            raise E2EFailure("scenario_prerequisite_failed")
    body = {"query": str(case.get("query", ""))}
    if parent_turn_id is not None:
        body["parent_turn_id"] = parent_turn_id
    turn = http.call(
        "POST",
        f"/v1/conversations/{conversation_id}/turns",
        body,
        key=f"quality-turn-{run_id}-{case_id}",
    )
    request_id = turn.get("request_id")
    if not request_id:
        raise E2EFailure("request_id_missing")
    repeated = http.call("POST", f"/v1/conversations/{conversation_id}/turns",
                         body, key=f"quality-turn-{run_id}-{case_id}")
    idempotent = repeated.get("request_id") == request_id
    deadline = time.monotonic() + timeout_s
    result: Mapping[str, Any] = {}
    sse_events = http.events(f"/v1/turns/{request_id}/events")
    while time.monotonic() < deadline:
        value = http.call("GET", f"/v1/turns/{request_id}")
        if not isinstance(value, Mapping):
            raise E2EFailure("turn_result_invalid")
        result = value
        if result.get("status") not in {"PENDING", "EXECUTING", "CANCEL_REQUESTED"}:
            break
        time.sleep(0.2)
    answer = result.get("answer") if isinstance(result.get("answer"), str) else ""
    tags = {str(tag) for tag in case.get("tags", []) if isinstance(tag, str)}
    excluded = "unauthorized" in tags and ("safety_and_routing" in tags or "freshness" in tags)
    document_ids = _document_ids(result)
    refreshed = http.call("GET", f"/v1/turns/{request_id}")
    pivot = len(sse_events) // 2
    last_id = int(sse_events[pivot - 1]["id"]) if pivot else 0
    replay = http.events(f"/v1/turns/{request_id}/events", last_event_id=last_id)
    replay_matches = replay == sse_events[pivot:]
    evidence = result.get("evidence") or []
    evidence_ids = {item.get("id") for item in evidence}
    citations = result.get("citations") or []
    citation_valid = all(item.get("evidence_id") in evidence_ids for item in citations)
    fact_reads = [json.loads(event["data"]) for event in sse_events if event["event"] == "fact_read"]
    row = {
        "case_id": case_id,
        "request_id": request_id,
        "status": result.get("status"),
        "answer_sha256": digest(answer),
        "evidence_document_ids": document_ids,
        "evidence_count": len(document_ids),
        "citation_count": len(result.get("citations", [])) if isinstance(result.get("citations"), list) else 0,
        "customer_copyable": bool((result.get("customer_reply") or {}).get("can_copy")) if isinstance(result.get("customer_reply"), Mapping) else False,
        "expected_document_count": len(case.get("expected_doc_ids", [])) if isinstance(case.get("expected_doc_ids"), list) else 0,
        "excluded_case": excluded,
        "sse_event_count": len(sse_events),
        "sse_sequence_monotonic": all(int(sse_events[i]["id"]) < int(sse_events[i + 1]["id"])
                                        for i in range(len(sse_events) - 1) if sse_events[i].get("id") and sse_events[i + 1].get("id")),
        "terminal_persisted": result.get("status") in {"DONE", "ASKING", "FAILED", "CANCELLED"},
        "successful_terminal": result.get("status") in {"DONE", "ASKING"},
        "idempotent_turn": idempotent,
        "refresh_matches": refreshed == result,
        "sse_replay_matches": replay_matches,
        "citation_ids_valid": citation_valid,
        "case_sha256": digest(json.dumps(case, ensure_ascii=False, sort_keys=True)),
        "query_sha256": digest(str(case.get("query", ""))),
        "prerequisite_results": prerequisites,
        "fact_reads": fact_reads,
    }
    if capture_dir is not None:
        row.update(capture(capture_dir, {"capture_version": "v2-http-quality-capture-v1",
                                        "case_id": case_id, "case_sha256": row["case_sha256"],
                                        "query_sha256": row["query_sha256"], "result": dict(result),
                                        "prerequisite_results": prerequisites, "fact_reads": fact_reads}))
    return row


def case_passed(row: Mapping[str, Any]) -> bool:
    checks = ("terminal_persisted", "successful_terminal", "sse_sequence_monotonic",
              "idempotent_turn", "refresh_matches", "sse_replay_matches", "citation_ids_valid")
    return (all(row.get(check) is True for check in checks) and row.get("sse_event_count", 0) > 0
            and not row.get('persisted_evidence_read_failed', False))


def write_report(path: Path, payload: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--base-url", default="http://127.0.0.1:18081")
    parser.add_argument("--cases", type=Path, default=DEFAULT_CASES)
    parser.add_argument("--lifecycle-report", type=Path, required=True)
    parser.add_argument("--safety-report", type=Path, required=True,
                        help="verified seven-case no-provider safety report")
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--limit", type=int, default=60)
    parser.add_argument("--timeout-s", type=float, default=65.0)
    parser.add_argument("--capture-dir", type=Path,
                        help="owned 0700 directory outside Git; immutable 0600 raw turn captures")
    parser.add_argument("--runtime-env", type=Path, help="protected online runtime identity; required for live runs")
    parser.add_argument("--case-id", action="append", default=[], help="targeted regression; full gate still requires 53 cases")
    parser.add_argument("--scenarios", type=Path, default=DEFAULT_SCENARIOS,
                        help="versioned conversation prerequisites, separate from frozen cases")
    parser.add_argument("--temporal-mode", choices=["historical", "current"], default="historical")
    args = parser.parse_args()
    if args.report.exists():
        print(json.dumps({"status": "FAIL", "issues": ["output_exists"], "online_requests_made": False}))
        return 2
    try:
        from evidence_boundary import DEFAULT_CONTRACT, validate_contract
        boundary = validate_contract(scenarios_path=args.scenarios)
        lifecycle = json.loads(args.lifecycle_report.read_text(encoding="utf-8"))
        if (lifecycle.get("status") != "PASS" or lifecycle.get("real_service_acceptance") is not False
                or lifecycle.get("report_version") != "v2-live-lifecycle-v1"
                or lifecycle.get("corpus", {}).get("document_count") != 37
                or not lifecycle.get("corpus", {}).get("corpus_sha256")
                or len(lifecycle.get("indexed_chunks", [])) == 0):
            raise E2EFailure("lifecycle_report_not_pass")
        all_cases = load_jsonl(args.cases)
        safety = json.loads(args.safety_report.read_text(encoding="utf-8"))
        excluded_ids = {str(row.get("case_id")) for row in safety.get("results", [])}
        if (safety.get("report_version") != "v2-quality-safety-v1" or safety.get("status") != "PASS"
                or len(excluded_ids) != 7 or any(row.get("passed") is not True or row.get("provider_attempt_delta") != 0
                                                 for row in safety.get("results", []))):
            raise E2EFailure("safety_report_invalid")
        cases = [case for case in all_cases if case.get("case_id") not in excluded_ids][: args.limit]
        if len(cases) != min(args.limit, 53):
            raise E2EFailure("eligible_case_partition_invalid")
        scenarios = load_scenarios(args.scenarios, args.cases, load_jsonl(args.cases))
        if args.case_id:
            selected = set(args.case_id)
            if not selected.issubset({case["case_id"] for case in cases}):
                raise E2EFailure("targeted_case_partition_invalid")
            cases = [case for case in cases if case["case_id"] in selected]
        if not args.live:
            print(json.dumps({"status": "PLAN", "case_count": len(cases), "real_service_acceptance": False}))
            return 0
        if args.runtime_env is None:
            raise E2EFailure("protected_runtime_identity_required")
        identity, ledger = runtime_identity(args.runtime_env, args.base_url)
        if args.temporal_mode == "historical" and identity["business_date"] != boundary["historical_business_date"]:
            raise E2EFailure("historical_runtime_date_mismatch")
        budget_before = budget_snapshot(ledger)
        run_id = uuid4().hex
        http = HTTP(args.base_url)
        session = http.call("GET", "/v1/session")
        if not isinstance(session, Mapping):
            raise E2EFailure("session_response_invalid")
        # A Bailian AI profile still uses the synthetic identity; keep the
        # result boundary explicit rather than treating it as M2.
        if (session.get("is_mock") is not False
                and session.get("profile") != "v2_langchain_bailian"):
            raise E2EFailure("unexpected_runtime_profile")
        if args.capture_dir:
            secure_directory(args.capture_dir)
        results = []
        for case in cases:
            started = time.monotonic()
            try:
                row = run_case(http, case, timeout_s=args.timeout_s, capture_dir=args.capture_dir,
                               prerequisite_queries=scenarios.get(case["case_id"], []), run_id=run_id)
            except E2EFailure as error:
                row = {"case_id": case["case_id"], "status": "FAILED", "failure_code": str(error),
                       "terminal_persisted": False, "successful_terminal": False}
            attach_persisted_evidence(row, args.runtime_env, args.capture_dir)
            row["latency_ms"] = round((time.monotonic() - started) * 1000)
            results.append(row)
            print(json.dumps({"case_id": results[-1]["case_id"], "status": results[-1]["status"],
                              "completed": len(results)}, ensure_ascii=False), flush=True)
        passed = all(case_passed(row) for row in results)
        payload = {
            "report_version": "v2-quality-e2e-v2",
            "status": "PASS" if passed else "NOT_PASSED",
            "case_count": len(results),
            "results": results,
            "lifecycle_report_sha256": hashlib.sha256(args.lifecycle_report.read_bytes()).hexdigest(),
            "cases_sha256": hashlib.sha256(args.cases.read_bytes()).hexdigest(),
            "scenarios_sha256": hashlib.sha256(args.scenarios.read_bytes()).hexdigest(),
            "excluded_case_ids": sorted(excluded_ids),
            "boundary_contract_sha256": hashlib.sha256(DEFAULT_CONTRACT.read_bytes()).hexdigest(),
            "temporal_mode": args.temporal_mode,
            "run_identity_sha256": digest(run_id),
            "safety_report_sha256": hashlib.sha256(args.safety_report.read_bytes()).hexdigest(),
            "real_service_acceptance": False,
            "model_quality_claim": False,
            "online_requests_made": True,
            "provider_identity": identity,
            "budget_before": budget_before,
            "budget_after": budget_snapshot(ledger),
        }
        write_report(args.report, payload)
        print(json.dumps({"status": payload["status"], "case_count": len(results), "real_service_acceptance": False}))
        return 0 if passed else 4
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, ValueError, E2EFailure) as error:
        print(json.dumps({"status": "FAIL", "issues": [str(error)], "online_requests_made": False}))
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
