#!/usr/bin/env python3
"""Bounded HTTP lifecycle acceptance; default only plans controlled fixtures.

Run with the repository Python environment. --live requires an isolated empty
V2 index, the active online runtime config and its existing shared budget.
Reports contain metadata and hashes, never answers, source text or secrets.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import io
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from contextlib import contextmanager
from decimal import Decimal
from pathlib import Path
from uuid import uuid4
from xml.sax.saxutils import escape

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "python"))
sys.path.insert(0, str(ROOT / "scripts"))


class AcceptanceFailure(Exception):
    """Only fixed diagnostics may leave the acceptance process."""


def digest(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def fixtures(run: str, tenant: str, shop: str):
    origin = ROOT / "data/synthetic/ecommerce-demo-v1"
    faq = (origin / "faq.md").read_text()
    policy = (origin / "policies.md").read_text()
    cups = faq.split("## 保温杯容量\n", 1)[1].split("\n##", 1)[0].strip()
    returns = policy.split("## 退货\n", 1)[1].split("\n##", 1)[0].strip()
    product = next(csv.DictReader((origin / "products.csv").read_text().splitlines()))
    product.update(tenant_id=tenant, shop_id=shop)
    csv_text = io.StringIO()
    writer = csv.DictWriter(csv_text, fieldnames=list(product))
    writer.writeheader()
    writer.writerow(product)
    docx_bytes = io.BytesIO()
    # Deterministic WordprocessingML; the application has a standard-library
    # DOCX parser, so the acceptance entrypoint needs no optional dependency.
    document = ('<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
                '<w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>'
                '<w:r><w:t>受控合成退货政策</w:t></w:r></w:p>'
                f'<w:p><w:r><w:t>{escape(returns)}</w:t></w:r></w:p><w:sectPr/></w:body></w:document>')
    entries = {
        "[Content_Types].xml": '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
        "_rels/.rels": '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
        "word/document.xml": document,
    }
    with zipfile.ZipFile(docx_bytes, "w") as archive:
        for name, content in entries.items():
            archive.writestr(zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0)), content.encode())
    specs = [
        ("cups", "markdown", "cups.md", ("# 受控合成保温杯容量\n\n" + cups + "\n").encode(), "faq.md"),
        ("product", "csv", "product.csv", csv_text.getvalue().encode(), "products.csv"),
        ("returns", "docx", "returns.docx", docx_bytes.getvalue(), "policies.md"),
    ]
    return [{"document_id": f"live-{run}-{label}", "format": fmt, "path": path,
             "content": data, "sha256": digest(data), "origin": str((origin / original).relative_to(ROOT)),
             "origin_sha256": digest((origin / original).read_bytes()), "version": "v2-controlled-1",
             "disclosure_class": "external_allowed", "synthetic": True,
             "purpose": "isolated development lifecycle", "scope_adaptation": {"tenant": tenant, "shop": shop}}
            for label, fmt, path, data, original in specs]


def plan(specs, models):
    """Use enforced Provider maxima, not guessed token counts or discounts."""
    from app.ingest.pipeline import chunk_elements_v2, parser_for
    from app.providers.budget import MICRO_CNY, PRICE_VERSION, PRICES

    with tempfile.TemporaryDirectory(prefix="ecr-live-preflight-") as directory:
        for spec in specs:
            path = Path(directory) / spec["path"]
            path.write_bytes(spec["content"])
            elements = parser_for(spec["format"]).parse(path, document_id=spec["document_id"],
                version_id="preflight", metadata={"tenant_id": spec["scope_adaptation"]["tenant"],
                    "shop_id": spec["scope_adaptation"]["shop"], "disclosure_class": "external_allowed"})
            chunks = chunk_elements_v2(elements, max_chars=700)
            if not 1 <= len(chunks) <= 16 or any(len(c.content.encode()) > 8192 for c in chunks):
                raise AcceptanceFailure("fixture exceeds single document embedding batch")
            spec["preflight"] = {"elements": len(elements), "chunks": len(chunks),
                                 "chunk_hashes": [c.chunk_hash for c in chunks]}
    # Four questions, three source batches; empty-index generation is local.
    # 65536 is the adapter's request body limit, not the fixture size. Search
    # passes at most eight candidates; assume each rerank query fills the body.
    bounds = {"control": (4, 65536 * 2 + 8192, 256),
              "embedding": (7, 65536 * 2 + 8192 + 4096 * 16, 0),
              "rerank": (3, 65536 * 2 + 8192 + (65536 * 2 + 4096) * 8, 0),
              "generation": (3, 65536 * 2 + 8192, 512)}
    slots = {}
    total = 0
    for slot, (count, inputs, outputs) in bounds.items():
        if models[slot] not in PRICES:
            raise AcceptanceFailure("model requires reviewed budget pricing")
        rates = PRICES[models[slot]]
        cost = int((inputs * rates[0] + outputs * rates[1]).to_integral_value(rounding="ROUND_CEILING"))
        total += cost * count
        slots[slot] = {"requests_max": count, "input_tokens_bound": inputs,
                       "output_tokens_bound": outputs, "model": models[slot],
                       "upper_bound_cny": str(Decimal(cost * count) / MICRO_CNY)}
    return {"pricing": PRICE_VERSION, "slots": slots, "upper_bound_cny": str(Decimal(total) / MICRO_CNY),
            "automatic_retry": False, "basis": "enforced adapter payload limits; no discounts or refunds"}


class HTTP:
    def __init__(self, base: str):
        parsed = urllib.parse.urlsplit(base)
        if (parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or parsed.username
                or parsed.password or parsed.query or parsed.fragment or parsed.path not in {"", "/"}):
            raise AcceptanceFailure("loopback HTTP base required")
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, path, body=None, *, content_type="application/json", key=None):
        headers = {"Content-Type": content_type}
        if body is not None:
            headers["Idempotency-Key"] = key or uuid4().hex
        data = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, headers=headers)
        try:
            with self.opener.open(request, timeout=15) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise AcceptanceFailure(f"HTTP {error.code}") from None


def command(http, path, body):
    outcome = http.call(path, body)
    deadline = time.monotonic() + 30
    while outcome.get("command_status") == "pending" or outcome.get("status") == "processing":
        if time.monotonic() >= deadline or not outcome.get("command_id"):
            raise AcceptanceFailure("command reconciliation deadline exceeded")
        time.sleep(.2)
        outcome = http.call("/admin/v1/merchant/commands/" + outcome["command_id"])
    if outcome.get("audit_result") != "success":
        raise AcceptanceFailure("command was not confirmed successful")
    return outcome


@contextmanager
def phase(report, ledger, label, *, embedding_text_type):
    def last_attempt():
        with sqlite3.connect(ledger.path.as_uri() + "?mode=ro", uri=True) as conn:
            return conn.execute("SELECT coalesce(max(rowid),0) FROM attempt").fetchone()[0]
    item = {"phase": label, "embedding_text_type": embedding_text_type, "attempt_after": last_attempt()}
    report.setdefault("phases", []).append(item)
    try:
        yield
    finally:
        item["attempt_through"] = last_attempt()


def upload_body(specs, shop, run):
    import yaml

    manifest = yaml.safe_dump({"schema_version": 1, "source": {"id": f"live-{run}", "name": "Controlled fixtures"},
                               "pipeline_version": "v2-live-lifecycle", "documents": [
        {"document_id": spec["document_id"], "title": spec["document_id"], "path": spec["path"],
         "format": spec["format"], "sha256": spec["sha256"], "disclosure_class": "external_allowed"}
        for spec in specs]}, allow_unicode=True)
    boundary = "ecr-" + uuid4().hex
    parts = []

    def part(name, data, filename=None):
        disposition = f'Content-Disposition: form-data; name="{name}"'
        if filename:
            disposition += f'; filename="{filename}"'
        parts.append((f"--{boundary}\r\n{disposition}\r\n\r\n").encode() + data + b"\r\n")

    part("shop_id", shop.encode())
    part("manifest", manifest.encode())
    for spec in specs:
        part("paths", spec["path"].encode())
        part("files", spec["content"], spec["path"])
    return b"".join(parts) + f"--{boundary}--\r\n".encode(), f"multipart/form-data; boundary={boundary}"


def wait_job(http, job):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        result = http.call("/admin/v1/merchant/ingestion/" + job)
        if result["status"] == "awaiting_review":
            return result
        if result["status"] not in {"pending", "running"}:
            raise AcceptanceFailure("ingestion " + str(result.get("error_code") or result["status"]))
        time.sleep(.2)
    raise AcceptanceFailure("ingestion deadline exceeded")


def query(http, question, report, *, expected=None, terms=()):
    conversation = http.call("/v1/conversations", {})
    accepted = http.call(f'/v1/conversations/{conversation["id"]}/turns', {"query": question})
    deadline = time.monotonic() + 65
    result = {}
    while time.monotonic() < deadline:
        result = http.call("/v1/turns/" + accepted["request_id"])
        if result["status"] not in {"PENDING", "EXECUTING", "CANCEL_REQUESTED"}:
            break
        time.sleep(.2)
    answer = result.get("answer") or ""
    if not isinstance(answer, str):
        raise AcceptanceFailure("query answer contract is invalid")
    evidence = result.get("evidence", [])
    report["queries"].append({"query_sha256": digest(question.encode()), "request_id": accepted["request_id"],
                               "status": result.get("status"), "answer_sha256": digest(answer.encode()),
                               "evidence": [dict({k: item.get(k) for k in ("id", "documentId", "versionId")},
                                                 snippet_sha256=digest(item.get("snippet", "").encode()))
                                            for item in evidence], "citations": result.get("citations", [])})
    if result.get("status") != "DONE" or result.get("customer_reply", {}).get("can_copy") is not False:
        raise AcceptanceFailure("query did not reach controlled DONE")
    if expected is None:
        if evidence or not any(term in answer for term in ("没有", "不足", "无法", "未找到", "无可用")):
            raise AcceptanceFailure("empty index did not refuse unsupported answer")
    else:
        permitted = report["published"]
        if not evidence or not any(item.get("documentId") == expected for item in evidence):
            raise AcceptanceFailure("expected published document was not cited")
        if any(permitted.get(item.get("documentId")) != item.get("versionId") for item in evidence):
            raise AcceptanceFailure("citation was not bound to this run's version")
        if not result.get("citations") or any(c.get("evidence_id") not in {e["id"] for e in evidence}
                                              for c in result["citations"]):
            raise AcceptanceFailure("generation citations were not bound to retrieved evidence")
        if not all(term in answer for term in terms):
            raise AcceptanceFailure("answer missed controlled support checks")


def validate_provider_evidence(report, models):
    """A business result alone cannot prove all paid slots actually ran."""
    expected = {"empty-index-query": {"control": 1, "embedding": 1},
                "source-ingestion": {"embedding": 3},
                **{label: dict.fromkeys(("control", "embedding", "rerank", "generation"), 1)
                   for label in ("markdown-query", "csv-query", "docx-query")}}
    phases = report.get("phases", [])
    attempts = report.get("provider_attempts", [])
    if len(phases) != len(expected) or {p["phase"] for p in phases} != set(expected):
        raise AcceptanceFailure("incomplete Provider phase evidence")
    covered = []
    for item in phases:
        batch = [a for a in attempts if item["attempt_after"] < a["sequence"] <= item["attempt_through"]]
        counts = {}
        for attempt in batch:
            slot = attempt["slot"]
            counts[slot] = counts.get(slot, 0) + 1
            if (attempt["model"] != models.get(slot) or attempt["state"] != "response"
                    or attempt["http_status"] != 200 or attempt["error_code"]
                    or not attempt["request_id_sha256"] or not attempt["usage_json"]):
                raise AcceptanceFailure("Provider request evidence is incomplete or failed")
            covered.append(attempt["sequence"])
        if counts != expected[item["phase"]]:
            raise AcceptanceFailure("Provider phase slot coverage does not match lifecycle")
    if sorted(covered) != sorted(a["sequence"] for a in attempts):
        raise AcceptanceFailure("Provider requests outside lifecycle phases")
    chunks = report.get("indexed_chunks", [])
    if not chunks or any(c["embedding_model"] != models["embedding"] or c["embedding_dim"] != 1024
                         or report["published"].get(c["document_id"]) != c["version_id"] for c in chunks):
        raise AcceptanceFailure("indexed vectors do not match published Provider versions")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--base-url", default="http://127.0.0.1:18081")
    parser.add_argument("--runtime-env", type=Path)
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    run = uuid4().hex[:12]
    report = {"status": "PLAN", "profile": "v2_langchain_bailian", "real_service_acceptance": False,
              "run_id": run, "inputs": [], "queries": [], "published": {}}
    handle = None
    ledger = None
    attempt_start = None
    try:
        if not args.live:
            specs = fixtures(run, "v2-demo-tenant", "shop-demo")
            report["budget_plan"] = plan(specs, {"control": "qwen3.8-flash", "embedding": "qwen3.7-text-embedding",
                                               "rerank": "qwen3.7-text-rerank", "generation": "qwen3.7-plus"})
            report["inputs"] = [{k: v for k, v in item.items() if k != "content"} for item in specs]
            print(json.dumps(report, ensure_ascii=False, indent=2))
            return 0
        if args.runtime_env is None or args.report is None:
            raise AcceptanceFailure("live requires runtime env and new report")
        handle = os.open(args.report, os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        report["status"] = "FAIL"
        report["source_revision"] = {
            "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "branch": subprocess.check_output(["git", "branch", "--show-current"], cwd=ROOT, text=True).strip(),
            "worktree": subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).splitlines(),
        }
        import psycopg
        from v2_services import load_values

        from app.providers.budget import BudgetLedger
        from app.providers.config import ensure_secure_config_file, load_provider_config

        values = load_values(args.runtime_env)
        if values.get("RAG_PROFILE") != report["profile"]:
            raise AcceptanceFailure("online runtime profile required")
        http = HTTP(args.base_url)
        if http.base != "http://" + values.get("HTTP_ADDR", ""):
            raise AcceptanceFailure("HTTP base must match the reviewed runtime address")
        ensure_secure_config_file(values["V2_PROVIDER_CONFIG"])
        provider = load_provider_config(values["V2_PROVIDER_CONFIG"])
        if provider.profile != "aliyun-bailian" or provider.region != "cn-beijing":
            raise AcceptanceFailure("reviewed Beijing Bailian provider required")
        tenant, shop = values["V2_IDENTITY_TENANT"], values["V2_IDENTITY_SHOP"]
        specs = fixtures(run, tenant, shop)
        report["budget_plan"] = plan(specs, provider.models)
        report["inputs"] = [{k: v for k, v in item.items() if k != "content"} for item in specs]
        ledger = BudgetLedger(Path(values["V2_ONLINE_BUDGET_LEDGER"]), values["V2_ONLINE_BUDGET_ID"], values["V2_ONLINE_BUDGET_CNY"])
        report["budget_before"] = ledger.summary()
        with sqlite3.connect(ledger.path.as_uri() + "?mode=ro", uri=True) as conn:
            attempt_start = conn.execute("SELECT coalesce(max(rowid),0) FROM attempt").fetchone()[0]
        if Decimal(report["budget_before"]["remaining_cny"]) < Decimal(report["budget_plan"]["upper_bound_cny"]):
            raise AcceptanceFailure("bounded batch exceeds remaining budget")
        with psycopg.connect(values["V2_GATEWAY_DSN"]) as conn:
            name, role, superuser, bypass = conn.execute("SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").fetchone()
            if "v2_live" not in name or superuser or bypass or conn.execute("SELECT count(*) FROM chunk").fetchone()[0]:
                raise AcceptanceFailure("empty isolated live index required")
            report["database"] = {"name": name, "role": role, "superuser": superuser, "bypass_rls": bypass,
                                  "migrations": [dict(zip(("version", "checksum_sha256"), row)) for row in
                                                 conn.execute("SELECT version,checksum_sha256 FROM schema_migration ORDER BY version")]}
        session = http.call("/v1/session")
        if (session.get("profile") != report["profile"] or session.get("tenantId") != tenant
                or session.get("userId") != values["V2_IDENTITY_USER"] or session.get("shopId") != shop):
            raise AcceptanceFailure("HTTP profile does not match online runtime")
        with phase(report, ledger, "empty-index-query", embedding_text_type="query"):
            query(http, "本店是否提供月球快递？", report)
        body, content_type = upload_body(specs, shop, run)
        with phase(report, ledger, "source-ingestion", embedding_text_type="document"):
            accepted = http.call("/admin/v1/merchant/sources/import", body, content_type=content_type)
            report["job_id"] = accepted["job_id"]
            status = wait_job(http, accepted["job_id"])
        report["ingestion"] = {"status": status["status"], "items": [
            {k: item.get(k) for k in ("document_id", "version_id", "version_hash", "fencing_epoch", "status")}
            for item in status["items"]]}
        for spec in specs:
            item = next(i for i in status["items"] if i["document_id"] == spec["document_id"])
            version = item["version_id"]
            detail = http.call("/admin/v1/merchant/reviews/" + version)
            if detail["source_hash"] != spec["sha256"] or detail["quality"]["embedding_status"] != "passed":
                raise AcceptanceFailure("review integrity failed")
            admin = http.call("/admin/v1/session")
            base = {"confirm": True, "permission_revision": admin["permission_revision"], "reason": "Reviewed controlled synthetic source and its immutable hash.",
                    "expected_fencing_epoch": detail["fencing_epoch"]}
            command(http, f"/admin/v1/merchant/versions/{version}/review", dict(base, decision="approved", disclosure_class="external_allowed", external_allowed=True, expected_hash=spec["sha256"]))
            admin = http.call("/admin/v1/session")
            outcome = command(http, f"/admin/v1/merchant/versions/{version}/publish", dict(base, permission_revision=admin["permission_revision"], tenant_id=tenant, shop_id=shop, document_id=spec["document_id"], version_hash=spec["sha256"]))
            if outcome.get("status") != "active":
                raise AcceptanceFailure("publication did not reach active")
            report["published"][spec["document_id"]] = version
        for label, question, expected, terms in (
            ("markdown-query", "SKU-CUP-480 和 SKU-CUP-750 容量分别是多少？", specs[0]["document_id"], ("480", "750")),
            ("csv-query", "晨雾保温杯的杯盖密封圈应该怎样护理？", specs[1]["document_id"], ("擦干", "密封圈")),
            ("docx-query", "签收后多少天内可以申请无理由退货？有什么条件？", specs[2]["document_id"], ("7", "完整")),
        ):
            with phase(report, ledger, label, embedding_text_type="query"):
                query(http, question, report, expected=expected, terms=terms)
        with psycopg.connect(values["V2_GATEWAY_DSN"]) as conn:
            report["indexed_chunks"] = [dict(zip(("evidence_id", "document_id", "version_id", "source_sha256",
                "chunk_sha256", "embedding_model", "embedding_dim"), row)) for row in conn.execute(
                "SELECT 'db-chunk-'||c.id,d.logical_key,c.version_id::text,v.source_hash,c.chunk_hash,c.embedding_model,c.embedding_dim "
                "FROM chunk c JOIN document d ON d.id=c.doc_id AND d.tenant_id=c.tenant_id "
                "JOIN document_version v ON v.id=c.version_id AND v.tenant_id=c.tenant_id ORDER BY c.id")]
        report["status"] = "PASS"
    except Exception as error:  # noqa: BLE001 - persist fixed diagnostics for every failed acceptance
        report["failure"] = str(error) if isinstance(error, AcceptanceFailure) else type(error).__name__
        print("FAIL V2 live lifecycle: " + report["failure"], file=sys.stderr)
    finally:
        if ledger is not None:
            try:
                report["budget_after"] = ledger.summary()
                if attempt_start is not None:
                    with sqlite3.connect(ledger.path.as_uri() + "?mode=ro", uri=True) as conn:
                        report["provider_attempts"] = [dict(zip(("sequence", "slot", "model", "input_sha256", "request_id_sha256", "usage_json", "reserved_micro", "state", "http_status", "error_code"), row)) for row in conn.execute(
                            "SELECT rowid,slot,model,input_sha256,request_id_sha256,usage_json,reserved_micro,state,http_status,error_code "
                            "FROM attempt WHERE rowid>? ORDER BY rowid", (attempt_start,))]
                    if report["status"] == "PASS":
                        validate_provider_evidence(report, provider.models)
            except Exception:  # noqa: BLE001 - do not lose the failure report when the ledger fails
                report["budget_evidence_error"] = "Provider evidence incomplete; reservations not refunded"
                report["status"] = "FAIL"
        if handle is not None:
            os.write(handle, json.dumps(report, ensure_ascii=False, indent=2).encode())
            os.fsync(handle)
            os.close(handle)
            print(f'{report["status"]} report={args.report}')
    return 0 if report["status"] == "PASS" else 2


if __name__ == "__main__":
    raise SystemExit(main())
