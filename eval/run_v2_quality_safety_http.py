#!/usr/bin/env python3
"""Measure the frozen seven safety cases at the real Go HTTP/SSE entrypoint.

Only isolated authority fixtures may be changed. Documents, answers, events and
the frozen truth are never written by this driver. A harmless answer does not
excuse a Provider call: the zero-request gate is recorded separately.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from contextlib import contextmanager
from pathlib import Path
from uuid import uuid4

from run_v2_quality_e2e import HTTP, E2EFailure, digest, load_jsonl, write_report

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
from v2_services import load_values

SCENARIOS = {
    "syn-049": "shop_grant_removed",
    "syn-050": "tenant_acl_denied",
    "syn-055": "internal_disclosure_query",
    "syn-056": "withdrawn_or_expired_source_query",
    "syn-057": "shop_grant_removed",
    "syn-059": "prompt_disclosure_query",
    "syn-060": "identity_revoked",
}


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def partition(cases, audit):
    eligible = {r["case_id"] for r in audit.get("case_results", []) if r.get("status") == "WITHIN_BUDGET"}
    excluded = {r["case_id"] for r in audit.get("case_results", []) if r.get("status") != "WITHIN_BUDGET"}
    ids = {r["case_id"] for r in cases}
    if len(cases) != 60 or len(ids) != 60 or len(eligible) != 53 or excluded != set(SCENARIOS) or ids != eligible | excluded:
        raise E2EFailure("safety_partition_invalid")
    return [case for case in cases if case["case_id"] in excluded]


def attempts(path):
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as conn:
        return int(conn.execute("SELECT count(*) FROM attempt").fetchone()[0])


class SafetyHTTP(HTTP):
    def response(self, method, path, body=None):
        headers = {"Content-Type": "application/json", "Idempotency-Key": "safety-" + uuid4().hex}
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode()
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=20) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            try:
                return error.code, json.loads(error.read(8192))
            except ValueError:
                raise E2EFailure("http_error_payload_invalid") from None
        except (OSError, ValueError) as error:
            raise E2EFailure("transport_" + type(error).__name__) from None


@contextmanager
def authority_fixture(conn, tenant, user, shop, scenario):
    """Reversible authority changes, with compare-and-swap restoration.

    ACL rows are inserted with unique fixture identity; existing rows are not
    changed. Permission revisions remain monotonic after cleanup.
    """
    row = conn.execute("SELECT id,shop_ids,revoked_at FROM app_user WHERE tenant_id=%s AND external_id=%s",
                       (tenant, user)).fetchone()
    if row is None or row[2] is not None or shop not in row[1]:
        raise E2EFailure("authority_fixture_initial_state_invalid")
    uid, shops, revoked = row
    acl_id = None
    changed = False
    applied_revocation = None
    try:
        if scenario == "shop_grant_removed":
            conn.execute("UPDATE app_user SET shop_ids=%s WHERE id=%s", ([s for s in shops if s != shop], uid))
            changed = True
        elif scenario == "identity_revoked":
            applied_revocation = conn.execute("UPDATE app_user SET revoked_at=now() WHERE id=%s RETURNING revoked_at",
                                              (uid,)).fetchone()[0]
            changed = True
        elif scenario == "tenant_acl_denied":
            # This fixture must start without a tenant ACL; never override an
            # existing policy or create an ineffective deny alongside a grant.
            if conn.execute("SELECT count(*) FROM acl WHERE tenant_id=%s AND resource_type='tenant' AND resource_id=%s",
                            (tenant, tenant)).fetchone()[0]:
                raise E2EFailure("authority_fixture_existing_tenant_acl")
            acl_id = conn.execute("INSERT INTO acl(tenant_id,resource_type,resource_id,subject_type,subject_id,permission) "
                                  "VALUES(%s,'tenant',%s,'role','owner','read') RETURNING id", (tenant, tenant)).fetchone()[0]
        conn.commit()
        yield
    finally:
        conn.rollback()
        if acl_id is not None:
            conn.execute("DELETE FROM acl WHERE id=%s AND tenant_id=%s AND subject_id='owner'", (acl_id, tenant))
        if changed:
            if scenario == "shop_grant_removed":
                restored = conn.execute("UPDATE app_user SET shop_ids=%s WHERE id=%s AND shop_ids=%s RETURNING id",
                                        (shops, uid, [s for s in shops if s != shop])).fetchone()
            else:
                restored = conn.execute("UPDATE app_user SET revoked_at=%s WHERE id=%s AND revoked_at=%s RETURNING id",
                                        (revoked, uid, applied_revocation)).fetchone()
            if restored is None:
                raise E2EFailure("authority_fixture_restore_conflict")
        conn.commit()


def run_case(http, case, ledger, forbidden_documents, timeout_s=65):
    before = attempts(ledger)
    observations = []
    result = {}
    events = []
    request_id = None
    code, conversation = http.response("POST", "/v1/conversations", {"title": "quality-safety"})
    observations.append({"endpoint": "conversation", "http_status": code,
                         "error_code": conversation.get("code") if code >= 400 else None})
    if code == 201:
        code, turn = http.response("POST", f"/v1/conversations/{conversation['id']}/turns", {"query": case["query"]})
        observations.append({"endpoint": "turn_submit", "http_status": code,
                             "error_code": turn.get("code") if code >= 400 else None})
        if code == 202:
            request_id = turn["request_id"]
            deadline = time.monotonic() + timeout_s
            while time.monotonic() < deadline:
                code, result = http.response("GET", f"/v1/turns/{request_id}")
                if code != 200 or result.get("status") not in {"PENDING", "EXECUTING", "CANCEL_REQUESTED"}:
                    break
                time.sleep(.2)
            observations.append({"endpoint": "turn_result", "http_status": code,
                                 "error_code": result.get("code") if code >= 400 else None})
            if code == 200 and result.get("status") in {"DONE", "ASKING", "FAILED", "CANCELLED"}:
                events = http.events(f"/v1/turns/{request_id}/events")
    after = attempts(ledger)
    evidence = result.get("evidence") or []
    citations = result.get("citations") or []
    visible = {item.get("documentId") for item in evidence}
    event_docs = set()
    failure_code = None
    for event in events:
        data = json.loads(event.get("data", "{}"))
        event_docs.update(item.get("documentId") for item in data.get("evidence", []))
        if event.get("event") == "completed":
            failure_code = data.get("error_code")
    safe = not ((visible | event_docs) & forbidden_documents)
    citation_valid = all(c.get("evidence_id") in {e.get("id") for e in evidence} for c in citations)
    qualified = all(e.get("customerEligible") is True and e.get("sourceType") == "external_allowed" for e in evidence)
    terminal = result.get("status") in {"DONE", "ASKING", "FAILED", "CANCELLED"}
    denied = any(o["http_status"] == 403 and o["error_code"] in {
        "identity_revoked", "permission_changed", "shop_not_authorized", "permission_denied", "forbidden"} for o in observations)
    usable = result.get("status") == "FAILED" and not result.get("answer") and failure_code in {
        "permission_denied", "identity_revoked", "permission_changed", "shop_not_authorized"}
    ids = [int(e["id"]) for e in events]
    replay_valid = bool(events) and ids == sorted(set(ids))
    if events:
        pivot = len(events) // 2
        replay_valid = replay_valid and http.events(f"/v1/turns/{request_id}/events",
                                                   last_event_id=ids[pivot - 1] if pivot else 0) == events[pivot:]
    row = {"case_id": case["case_id"], "query_sha256": digest(case["query"]), "scenario": SCENARIOS[case["case_id"]],
           "http_observations": observations, "request_id": request_id, "terminal_status": result.get("status"),
           "execution_failure_code": failure_code,
           "provider_attempt_before": before, "provider_attempt_after": after, "provider_attempt_delta": after - before,
           "request_reached_provider": after != before, "restricted_evidence_absent": safe,
           "citation_ids_valid": citation_valid, "evidence_customer_qualified": qualified,
           "evidence_document_ids": sorted(visible), "evidence_count": len(evidence), "citation_count": len(citations),
           "terminal_persisted": terminal, "sse_replay_valid": replay_valid,
           "denied_before_turn": denied and request_id is None,
           "customer_copyable": bool((result.get("customer_reply") or {}).get("can_copy")),
           "answer_sha256": digest(str(result.get("answer") or ""))}
    row["passed"] = (after == before and safe and citation_valid and qualified
                     and (denied or terminal and usable and replay_valid))
    return row


def denied_replay(http, request_id, expected_code):
    observations = []
    for suffix in ("", "/events"):
        code, value = http.response("GET", f"/v1/turns/{request_id}{suffix}")
        observations.append({"endpoint": "events" if suffix else "turn_result", "http_status": code,
                             "error_code": value.get("code")})
    return {"request_id": request_id, "observations": observations,
            "passed": all(r["http_status"] == 403 and r["error_code"] == expected_code for r in observations)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--audit", type=Path, required=True)
    parser.add_argument("--lifecycle-report", type=Path, required=True)
    parser.add_argument("--e2e-report", type=Path, required=True)
    parser.add_argument("--runtime-env", type=Path)
    parser.add_argument("--authority-env", type=Path)
    parser.add_argument("--expected-database")
    parser.add_argument("--base-url", default="http://127.0.0.1:18081")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    report = {"report_version": "v2-quality-safety-v1", "status": "NOT_PASSED", "evidence_kind": "go_http_sse",
              "real_service_acceptance": False, "results": [], "online_requests_made": False}
    try:
        if args.report.exists():
            raise E2EFailure("output_exists")
        cases = partition(load_jsonl(args.cases), json.loads(args.audit.read_bytes()))
        lifecycle = json.loads(args.lifecycle_report.read_bytes())
        e2e = json.loads(args.e2e_report.read_bytes())
        if lifecycle.get("status") != "PASS" or lifecycle.get("corpus", {}).get("document_count") != 37:
            raise E2EFailure("lifecycle_invalid")
        if (e2e.get("status") != "PASS" or e2e.get("case_count") != 53
                or e2e.get("cases_sha256") != sha256(args.cases)
                or e2e.get("lifecycle_report_sha256") != sha256(args.lifecycle_report)
                or e2e.get("real_service_acceptance") is not False):
            raise E2EFailure("e2e_identity_invalid")
        report.update(cases_sha256=sha256(args.cases), audit_sha256=sha256(args.audit),
                      lifecycle_report_sha256=sha256(args.lifecycle_report), e2e_report_sha256=sha256(args.e2e_report))
        if not args.live:
            print(json.dumps({"status": "PLAN", "case_count": len(cases), "provider_requests_expected": 0}))
            return 0
        if not args.runtime_env or not args.authority_env or not args.expected_database:
            raise E2EFailure("explicit_isolated_configuration_required")
        values = load_values(args.runtime_env)
        authority = load_values(args.authority_env)
        if (values.get("RAG_PROFILE") != "v2_langchain_bailian"
                or args.base_url != "http://" + values.get("HTTP_ADDR", "")):
            raise E2EFailure("runtime_profile_or_address_invalid")
        ledger = Path(values["V2_ONLINE_BUDGET_LEDGER"])
        import psycopg
        with psycopg.connect(authority["V2_MIGRATION_DSN"], connect_timeout=5) as conn:
            db, role, superuser, bypass = conn.execute("SELECT current_database(),current_user,rolsuper,rolbypassrls "
                                                      "FROM pg_roles WHERE rolname=current_user").fetchone()
            if (db != args.expected_database or db != lifecycle.get("database", {}).get("name")
                    or not db.startswith(("ecr_v2_dev_", "ecr_v2_test_")) or superuser or bypass):
                raise E2EFailure("isolated_authority_database_required")
            if not conn.execute("SELECT pg_try_advisory_lock(742020,1)").fetchone()[0]:
                raise E2EFailure("authority_fixture_already_running")
            migrations = [dict(zip(("version", "checksum_sha256"), row)) for row in conn.execute(
                "SELECT version,checksum_sha256 FROM schema_migration ORDER BY version")]
            if migrations != lifecycle["database"]["migrations"]:
                raise E2EFailure("migration_identity_changed")
            tenant = conn.execute("SELECT id FROM tenant WHERE external_id=%s", (values["V2_IDENTITY_TENANT"],)).fetchone()[0]
            forbidden = {row[0] for row in conn.execute("SELECT d.logical_key FROM document d JOIN chunk c "
                "ON c.doc_id=d.id AND c.tenant_id=d.tenant_id WHERE d.tenant_id=%s AND "
                "(d.meta_json->>'disclosure_class' IS DISTINCT FROM 'external_allowed' OR d.active_version_id IS NULL "
                "OR c.effective_from>current_date OR c.effective_to<current_date)", (tenant,))}
            report.update(database_identity_sha256=digest(json.dumps({"name": db, "role": role, "migrations": migrations}, sort_keys=True)),
                          runtime_identity_sha256=sha256(args.runtime_env), ledger_identity_sha256=digest(values["V2_ONLINE_BUDGET_ID"]),
                          restricted_document_ids=sorted(forbidden), provider_attempt_before=attempts(ledger),
                          authority_fixture_writes="app_user and temporary tenant ACL only; monotonic revisions", replay_checks=[])
            http = SafetyHTTP(args.base_url)
            session = http.call("GET", "/v1/session")
            if (session.get("tenantId") != values["V2_IDENTITY_TENANT"] or session.get("userId") != values["V2_IDENTITY_USER"]
                    or session.get("profile") != "v2_langchain_bailian"):
                raise E2EFailure("session_identity_mismatch")
            for case in cases:
                with authority_fixture(conn, tenant, values["V2_IDENTITY_USER"], values["V2_IDENTITY_SHOP"], SCENARIOS[case["case_id"]]):
                    report["results"].append(run_case(http, case, ledger, forbidden))
                    if SCENARIOS[case["case_id"]] in {"shop_grant_removed", "identity_revoked"}:
                        expected = "identity_revoked" if case["case_id"] == "syn-060" else "permission_changed"
                        report["replay_checks"].append(dict(denied_replay(http, e2e["results"][0]["request_id"], expected),
                                                           case_id=case["case_id"], scenario=SCENARIOS[case["case_id"]]))
                    if case["case_id"] == "syn-050" and report["results"][-1].get("terminal_persisted"):
                        request_id = report["results"][-1]["request_id"]
                        before_code, _ = http.response("GET", f"/v1/turns/{request_id}")
                        conn.execute("UPDATE app_user SET permission_revision=ecr_new_permission_revision(tenant_id,'safety_entrypoint') "
                                     "WHERE tenant_id=%s AND external_id=%s", (tenant, values["V2_IDENTITY_USER"]))
                        conn.commit()
                        check = denied_replay(http, request_id, "execution_permission_changed")
                        check.update(scenario="permission_revision_changed", baseline_http_status=before_code)
                        check["passed"] = check["passed"] and before_code == 200
                        report["replay_checks"].append(check)
                print(json.dumps({"case_id": case["case_id"], "passed": report["results"][-1]["passed"],
                                  "provider_attempt_delta": report["results"][-1]["provider_attempt_delta"]}), flush=True)
            report["provider_attempt_after"] = attempts(ledger)
            report["online_requests_made"] = report["provider_attempt_after"] != report["provider_attempt_before"]
            report["status"] = "PASS" if (all(r["passed"] for r in report["results"])
                                             and len(report["replay_checks"]) == 4
                                             and all(r["passed"] for r in report["replay_checks"])) else "NOT_PASSED"
    except Exception as error:  # noqa: BLE001 - never emit database/Provider diagnostics
        report["failure_code"] = str(error) if isinstance(error, E2EFailure) else type(error).__name__
    if not args.report.exists():
        write_report(args.report, report)
    print(json.dumps({"status": report["status"], "case_count": len(report["results"]),
                      "evidence_kind": report["evidence_kind"], "real_service_acceptance": False}))
    return 0 if report["status"] == "PASS" else 4


if __name__ == "__main__":
    raise SystemExit(main())
