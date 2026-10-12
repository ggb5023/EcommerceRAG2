#!/usr/bin/env python3
"""Run the frozen evaluation cases through the Go V2 HTTP entrypoint.

The lifecycle runner must first publish the controlled corpus.  This driver
only creates conversations and turns, then writes metadata-only results.  It
never stores query text, answers, snippets, cookies, or credentials.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_REVISION_ROOT = Path("/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional")
DEFAULT_CASES = DEFAULT_REVISION_ROOT / "synthetic-m2-v1-revision-1.jsonl"


class E2EFailure(RuntimeError):
    pass


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


def run_case(http: HTTP, case: Mapping[str, Any], *, timeout_s: float) -> dict[str, Any]:
    case_id = str(case.get("case_id", ""))
    if not case_id:
        raise E2EFailure("case_id_missing")
    body = {"title": "quality-eval"}
    conversation = http.call("POST", "/v1/conversations", body, key=f"quality-conversation-{case_id}")
    conversation_id = conversation.get("id")
    if not conversation_id:
        raise E2EFailure("conversation_id_missing")
    turn = http.call(
        "POST",
        f"/v1/conversations/{conversation_id}/turns",
        {"query": str(case.get("query", ""))},
        key=f"quality-turn-{case_id}",
    )
    request_id = turn.get("request_id")
    if not request_id:
        raise E2EFailure("request_id_missing")
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
    return {
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
    }


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
    args = parser.parse_args()
    if args.report.exists():
        print(json.dumps({"status": "FAIL", "issues": ["output_exists"], "online_requests_made": False}))
        return 2
    try:
        lifecycle = json.loads(args.lifecycle_report.read_text(encoding="utf-8"))
        if (lifecycle.get("status") != "PASS" or lifecycle.get("real_service_acceptance") is not False
                or lifecycle.get("report_version") != "v2-live-lifecycle-v1"):
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
        if not args.live:
            print(json.dumps({"status": "PLAN", "case_count": len(cases), "real_service_acceptance": False}))
            return 0
        http = HTTP(args.base_url)
        session = http.call("GET", "/v1/session")
        if not isinstance(session, Mapping):
            raise E2EFailure("session_response_invalid")
        # A Bailian AI profile still uses the synthetic identity; keep the
        # result boundary explicit rather than treating it as M2.
        if (session.get("is_mock") is not False
                and session.get("profile") != "v2_langchain_bailian"):
            raise E2EFailure("unexpected_runtime_profile")
        results = [run_case(http, case, timeout_s=args.timeout_s) for case in cases]
        if any(not row["terminal_persisted"] or row["sse_event_count"] == 0 or not row["sse_sequence_monotonic"] for row in results):
            raise E2EFailure("sse_or_terminal_persistence_invalid")
        payload = {
            "report_version": "v2-quality-e2e-v2",
            "status": "PASS",
            "case_count": len(results),
            "results": results,
            "lifecycle_report_sha256": hashlib.sha256(args.lifecycle_report.read_bytes()).hexdigest(),
            "cases_sha256": hashlib.sha256(args.cases.read_bytes()).hexdigest(),
            "excluded_case_ids": sorted(excluded_ids),
            "safety_report_sha256": hashlib.sha256(args.safety_report.read_bytes()).hexdigest(),
            "real_service_acceptance": False,
            "model_quality_claim": False,
            "online_requests_made": True,
        }
        write_report(args.report, payload)
        print(json.dumps({"status": "PASS", "case_count": len(results), "real_service_acceptance": False}))
        return 0
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, E2EFailure) as error:
        print(json.dumps({"status": "FAIL", "issues": [str(error)], "online_requests_made": False}))
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
