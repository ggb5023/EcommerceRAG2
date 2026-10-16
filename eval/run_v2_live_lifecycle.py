#!/usr/bin/env python3
"""Bounded HTTP lifecycle acceptance; default only plans controlled fixtures.

Run with the repository Python environment. --live requires an isolated empty
V2 index, the active online runtime config and its existing shared budget.
Reports contain metadata and hashes, never answers, source text or secrets.
"""
from __future__ import annotations

import argparse
import copy
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
sys.path.insert(0, str(ROOT / "scripts" / "dev"))
DEFAULT_CORPUS = Path("/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/documents.jsonl")


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


def corpus_fixtures(path: Path, run: str, tenant: str, shop: str):
    """Rebuild the approved 37-document source rail as uploadable Markdown.

    The controlled corpus is stored as immutable chunk metadata outside Git.
    Reconstructing a deterministic Markdown package lets the real ingestion
    path exercise all documents without copying evaluation queries or answers
    into the source.  The original per-document source hash remains evidence
    metadata; the uploaded package hash is computed from the bytes actually
    submitted to Go.
    """
    if not path.is_absolute() or path.is_symlink() or not path.is_file():
        raise AcceptanceFailure("approved corpus file is unavailable")
    rows = []
    seen = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise AcceptanceFailure("approved corpus row is invalid")
        document_id = value.get("document_id")
        if not isinstance(document_id, str) or not document_id or document_id in seen:
            raise AcceptanceFailure("approved corpus document set is invalid")
        chunks = value.get("chunks")
        if not isinstance(chunks, list) or not chunks:
            raise AcceptanceFailure("approved corpus document has no chunks")
        seen.add(document_id)
        rows.append(value)
    if len(rows) != 37:
        raise AcceptanceFailure("approved corpus must contain 37 documents")
    corpus_hash = digest(path.read_bytes())
    specs = []
    for value in rows:
        document_id = value["document_id"]
        chunks = value["chunks"]
        content = ("# " + document_id + "\n\n" + "\n\n".join(
            str(chunk.get("content", "")).strip() for chunk in chunks
        ) + "\n").encode("utf-8")
        disclosure = value.get("disclosure_class")
        if disclosure not in {"external_allowed", "internal_only", "unclassified"}:
            raise AcceptanceFailure("approved corpus disclosure class is invalid")
        if value.get("tenant_id") not in {"synthetic-tenant", tenant}:
            raise AcceptanceFailure("approved corpus tenant scope is invalid")
        if value.get("shop_id") not in {"shop-demo", shop}:
            raise AcceptanceFailure("approved corpus shop scope is invalid")
        if any(any(chunk.get(key) != value.get(key) for key in (
                "tenant_id", "shop_id", "disclosure_class", "effective_from", "effective_to"))
               for chunk in chunks):
            raise AcceptanceFailure("approved corpus chunk scope or dates differ from document")
        specs.append({
            "document_id": document_id,
            "format": "markdown",
            "path": f"{document_id}.md",
            "content": content,
            "sha256": digest(content),
            "origin": str(path),
            "origin_sha256": str(value.get("source_sha256", "")),
            "version": str(value.get("document_version_id", "")),
            "disclosure_class": disclosure,
            "synthetic": True,
            "purpose": "approved synthetic M2 lifecycle corpus",
            "scope_adaptation": {"tenant": tenant, "shop": shop},
            "corpus_sha256": corpus_hash,
            "source_type": value.get("source_type"),
            "source_version": value.get("source_version"),
            "effective_from": value.get("effective_from"),
            "effective_to": value.get("effective_to"),
        })
    return specs


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
                    "shop_id": spec["scope_adaptation"]["shop"], "disclosure_class": spec["disclosure_class"],
                    "effective_from": spec.get("effective_from"), "effective_to": spec.get("effective_to")})
            chunks = chunk_elements_v2(elements, max_chars=700)
            if not 1 <= len(chunks) <= 16 or any(len(c.content.encode()) > 8192 for c in chunks):
                raise AcceptanceFailure("fixture exceeds single document embedding batch")
            spec["preflight"] = {"elements": len(elements), "chunks": len(chunks),
                                 "chunk_hashes": [c.chunk_hash for c in chunks]}
    # One empty-index query, three representative queries and one source
    # embedding request per bounded batch; empty-index generation is local.
    # 65536 is the adapter's request body limit, not the fixture size. Search
    # passes at most eight candidates; assume each rerank query fills the body.
    source_embedding_requests = sum((item["preflight"]["chunks"] + 15) // 16 for item in specs)
    bounds = {"control": (4, 65536 * 2 + 8192, 256),
              "embedding": (source_embedding_requests + 4, 65536 * 2 + 8192 + 4096 * 16, 0),
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
    return {"pricing": PRICE_VERSION, "slots": slots, "source_embedding_requests": source_embedding_requests,
            "upper_bound_cny": str(Decimal(total) / MICRO_CNY),
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

    manifest = yaml.safe_dump({"schema_version": 1, "source": {"id": f"live-{run}", "name": "Approved synthetic M2 corpus"},
                               "pipeline_version": "v2-live-lifecycle", "documents": [
        {"document_id": spec["document_id"], "title": spec["document_id"], "path": spec["path"],
         "format": spec["format"], "sha256": spec["sha256"],
         "disclosure_class": spec.get("disclosure_class", "external_allowed"),
         "effective_from": spec.get("effective_from"), "effective_to": spec.get("effective_to")}
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


def wait_job(http, job, *, timeout_s=300):
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        result = http.call("/admin/v1/merchant/ingestion/" + job)
        if result["status"] == "awaiting_review":
            return result
        if result["status"] not in {"pending", "running"}:
            raise AcceptanceFailure("ingestion " + str(result.get("error_code") or result["status"]))
        time.sleep(.2)
    raise AcceptanceFailure("ingestion deadline exceeded")


def review_and_publish(http, specs, status, report, tenant, shop):
    """Keep frozen disclosure labels; unclassified sources stay unpublished."""
    report.setdefault("withheld", {})
    for spec in specs:
        item = next(i for i in status["items"] if i["document_id"] == spec["document_id"])
        version = item["version_id"]
        detail = http.call("/admin/v1/merchant/reviews/" + version)
        if (detail["source_hash"] != spec["sha256"]
                or detail["quality"]["embedding_status"] != "passed"
                or detail["disclosure_class"] != spec["disclosure_class"]):
            raise AcceptanceFailure("review integrity failed")
        disclosure = spec["disclosure_class"]
        if disclosure == "unclassified":
            if detail["status"] != "ready" or detail["external_allowed"]:
                raise AcceptanceFailure("unclassified source became visible")
            if detail["review_status"] == "pending":
                admin = http.call("/admin/v1/session")
                command(http, f"/admin/v1/merchant/versions/{version}/review", {
                    "confirm": True, "permission_revision": admin["permission_revision"],
                    "reason": "Frozen source classification is unclassified; publication is withheld.",
                    "expected_fencing_epoch": detail["fencing_epoch"], "decision": "rejected",
                    "disclosure_class": disclosure, "external_allowed": False,
                    "expected_hash": spec["sha256"],
                })
            elif detail["review_status"] != "rejected":
                raise AcceptanceFailure("unclassified review state is invalid")
            report["withheld"][spec["document_id"]] = version
            continue
        if detail["status"] == "active" and detail["review_status"] == "approved":
            report["published"][spec["document_id"]] = version
            continue
        if detail["status"] != "ready":
            raise AcceptanceFailure("source is not reviewable or active")
        admin = http.call("/admin/v1/session")
        base = {"confirm": True, "permission_revision": admin["permission_revision"],
                "reason": "Reviewed approved synthetic source and its immutable hash.",
                "expected_fencing_epoch": detail["fencing_epoch"]}
        if detail["review_status"] == "pending":
            command(http, f"/admin/v1/merchant/versions/{version}/review", dict(
                base, decision="approved", disclosure_class=disclosure,
                external_allowed=disclosure == "external_allowed", expected_hash=spec["sha256"]))
        elif detail["review_status"] != "approved":
            raise AcceptanceFailure("source review was rejected")
        admin = http.call("/admin/v1/session")
        outcome = command(http, f"/admin/v1/merchant/versions/{version}/publish", dict(
            base, permission_revision=admin["permission_revision"], tenant_id=tenant,
            shop_id=shop, document_id=spec["document_id"], version_hash=spec["sha256"]))
        if outcome.get("status") != "active":
            raise AcceptanceFailure("publication did not reach active")
        report["published"][spec["document_id"]] = version


def resumed_report(path, specs, database):
    """Resume only the completed ingestion phase of the same frozen corpus."""
    previous = json.loads(path.read_bytes())
    expected = {s["document_id"]: (s["sha256"], s["disclosure_class"], s.get("effective_from"), s.get("effective_to")) for s in specs}
    actual = {s["document_id"]: (s["sha256"], s["disclosure_class"], s.get("effective_from"), s.get("effective_to"))
              for s in previous.get("inputs", [])}
    if (previous.get("status") != "FAIL" or previous.get("report_version") != "v2-live-lifecycle-v1"
            or previous.get("profile") != "v2_langchain_bailian"
            or previous.get("real_service_acceptance") is not False
            or previous.get("database", {}).get("name") != database
            or previous.get("corpus", {}).get("corpus_sha256") != specs[0]["corpus_sha256"]
            or actual != expected or not previous.get("job_id")
            or previous.get("ingestion", {}).get("status") != "awaiting_review"
            or [p["phase"] for p in previous.get("phases", [])][:2] != ["empty-index-query", "source-ingestion"]
            or any(p["phase"] not in {"markdown-query", "csv-query", "docx-query"}
                   for p in previous.get("phases", [])[2:])
            or not previous.get("provider_attempts")):
        raise AcceptanceFailure("resume requires matching completed ingestion evidence")
    report = copy.deepcopy(previous)
    report["resumed_from"] = {"report_sha256": digest(path.read_bytes()),
                              "failure": report.pop("failure", None),
                              "source_revision": report.get("source_revision")}
    report.pop("budget_after", None)
    report.setdefault("superseded_phases", []).extend(report["phases"][2:])
    report["phases"] = report["phases"][:2]
    report.setdefault("superseded_queries", []).extend(report["queries"][1:])
    report["queries"] = report["queries"][:1]
    return report


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
    expected = report.get("phase_expectations") or {
        "empty-index-query": {"control": 1, "embedding": 1},
        "source-ingestion": {"embedding": 3},
        **{label: dict.fromkeys(("control", "embedding", "rerank", "generation"), 1)
           for label in ("markdown-query", "csv-query", "docx-query")}}
    phases = report.get("phases", [])
    attempts = report.get("provider_attempts", [])
    if len(phases) != len(expected) or {p["phase"] for p in phases} != set(expected):
        raise AcceptanceFailure("incomplete Provider phase evidence")
    covered = []
    covered.extend(report.get("intervening_provider_sequences", []))
    for item in report.get("superseded_phases", []):
        covered.extend(a["sequence"] for a in attempts
                       if item["attempt_after"] < a["sequence"] <= item["attempt_through"])
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
        # Multi-document search can call the existing applicability selector
        # after Understand. Permit that one control call explicitly; all calls
        # still require successful responses, model, usage and request IDs.
        allowed = report.get("phase_optional_requests", {}).get(item["phase"], {})
        minimum = expected[item["phase"]]
        if (set(counts) != set(minimum)
                or any(not minimum[slot] <= count <= minimum[slot] + allowed.get(slot, 0)
                       for slot, count in counts.items())):
            raise AcceptanceFailure("Provider phase slot coverage does not match lifecycle")
    if sorted(covered) != sorted(a["sequence"] for a in attempts):
        raise AcceptanceFailure("Provider requests outside lifecycle phases")
    chunks = report.get("indexed_chunks", [])
    versions = dict(report.get("withheld", {}), **report["published"])
    if not chunks or any(c["embedding_model"] != models["embedding"] or c["embedding_dim"] != 1024
                         or versions.get(c["document_id"]) != c["version_id"] for c in chunks):
        raise AcceptanceFailure("indexed vectors do not match published Provider versions")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--base-url", default="http://127.0.0.1:18081")
    parser.add_argument("--runtime-env", type=Path)
    parser.add_argument("--corpus", type=Path, default=DEFAULT_CORPUS)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--resume-report", type=Path,
                        help="failed completed-ingestion report; same database, new output only")
    args = parser.parse_args()
    run = uuid4().hex[:12]
    report = {"report_version": "v2-live-lifecycle-v1", "status": "PLAN", "profile": "v2_langchain_bailian", "real_service_acceptance": False,
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
        from app.providers.budget import BudgetLedger
        from app.providers.config import ensure_secure_config_file, load_provider_config
        from v2_services import load_values

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
        specs = corpus_fixtures(args.corpus, run, tenant, shop)
        report["budget_plan"] = plan(specs, provider.models)
        report["inputs"] = [{k: v for k, v in item.items() if k != "content"} for item in specs]
        report["corpus"] = {"path": str(args.corpus), "document_count": len(specs),
                            "corpus_sha256": specs[0].get("corpus_sha256") if specs else None,
                            "source_document_hashes": {item["document_id"]: item["origin_sha256"] for item in specs}}
        if len(specs) != 37:
            raise AcceptanceFailure("approved corpus must contain 37 documents")
        report["phase_expectations"] = {
            "empty-index-query": {"control": 1, "embedding": 1},
            "source-ingestion": {"embedding": report["budget_plan"]["source_embedding_requests"]},
            **{label: {"control": 1, "embedding": 1, "rerank": 1, "generation": 1}
               for label in ("markdown-query", "csv-query", "docx-query")},
        }
        ledger = BudgetLedger(Path(values["V2_ONLINE_BUDGET_LEDGER"]), values["V2_ONLINE_BUDGET_ID"], values["V2_ONLINE_BUDGET_CNY"])
        report["budget_before"] = ledger.summary()
        with sqlite3.connect(ledger.path.as_uri() + "?mode=ro", uri=True) as conn:
            attempt_start = conn.execute("SELECT coalesce(max(rowid),0) FROM attempt").fetchone()[0]
        if Decimal(report["budget_before"]["remaining_cny"]) < Decimal(report["budget_plan"]["upper_bound_cny"]):
            raise AcceptanceFailure("bounded batch exceeds remaining budget")
        with psycopg.connect(values["V2_GATEWAY_DSN"]) as conn:
            name, role, superuser, bypass = conn.execute("SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").fetchone()
            # Independently provisioned development databases may use the
            # generated ecr_v2_test_* name rather than the optional v2_live
            # label; migration and role checks still enforce the boundary.
            if ("v2" not in name or superuser or bypass
                    or (not args.resume_report and conn.execute("SELECT count(*) FROM chunk").fetchone()[0])):
                raise AcceptanceFailure("empty isolated live index required")
            report["database"] = {"name": name, "role": role, "superuser": superuser, "bypass_rls": bypass,
                                  "migrations": [dict(zip(("version", "checksum_sha256"), row)) for row in
                                                 conn.execute("SELECT version,checksum_sha256 FROM schema_migration ORDER BY version")]}
        if args.resume_report:
            current_revision = report["source_revision"]
            resume_boundary = attempt_start
            report = resumed_report(args.resume_report, specs, name)
            report["source_revision"] = current_revision
            if report["budget_before"]["window_id"] != ledger.window_id:
                raise AcceptanceFailure("resume budget window changed")
            attempt_start = min(a["sequence"] for a in report["provider_attempts"]) - 1
            last_previous = max(a["sequence"] for a in report["provider_attempts"])
            with sqlite3.connect(ledger.path.as_uri() + "?mode=ro", uri=True) as conn:
                report.setdefault("intervening_provider_sequences", []).extend(row[0] for row in conn.execute(
                    "SELECT rowid FROM attempt WHERE rowid>? AND rowid<=? ORDER BY rowid",
                    (last_previous, resume_boundary)))
        report["phase_optional_requests"] = {
            label: {"control": 1} for label in ("markdown-query", "csv-query", "docx-query")}
        session = http.call("/v1/session")
        if (session.get("profile") != report["profile"] or session.get("tenantId") != tenant
                or session.get("userId") != values["V2_IDENTITY_USER"] or session.get("shopId") != shop):
            raise AcceptanceFailure("HTTP profile does not match online runtime")
        if args.resume_report:
            status = http.call("/admin/v1/merchant/ingestion/" + report["job_id"])
            if status["status"] not in {"awaiting_review", "done"}:
                raise AcceptanceFailure("resumed ingestion state changed")
            actual = {i["document_id"]: i["version_id"] for i in status["items"]}
            expected = {i["document_id"]: i["version_id"] for i in report["ingestion"]["items"]}
            if actual != expected:
                raise AcceptanceFailure("resumed ingestion versions changed")
        else:
            with phase(report, ledger, "empty-index-query", embedding_text_type="query"):
                query(http, "本店是否提供月球快递？", report)
            body, content_type = upload_body(specs, shop, run)
            with phase(report, ledger, "source-ingestion", embedding_text_type="document"):
                accepted = http.call("/admin/v1/merchant/sources/import", body, content_type=content_type)
                report["job_id"] = accepted["job_id"]
                status = wait_job(http, accepted["job_id"], timeout_s=600)
        report["ingestion"] = {"status": status["status"], "items": [
            {k: item.get(k) for k in ("document_id", "version_id", "version_hash", "fencing_epoch", "status")}
            for item in status["items"]]}
        review_and_publish(http, specs, status, report, tenant, shop)
        selected = {item["document_id"]: item for item in specs}
        if all(key in selected for key in ("aligned-product-bedding-v1", "aligned-fact-promotion-v1", "aligned-web-usbc-v1")):
            query_cases = (
                ("markdown-query", "夏凉被适合怎样的室内环境？", "aligned-product-bedding-v1"),
                ("csv-query", "周末满减活动快照显示剩余名额是多少？", "aligned-fact-promotion-v1"),
                ("docx-query", "USB-C 相关规范的版本信息来自什么资料？", "aligned-web-usbc-v1"),
            )
        else:
            query_cases = (
                ("markdown-query", "SKU-CUP-480 和 SKU-CUP-750 容量分别是多少？", specs[0]["document_id"]),
                ("csv-query", "晨雾保温杯的杯盖密封圈应该怎样护理？", specs[1]["document_id"]),
                ("docx-query", "签收后多少天内可以申请无理由退货？有什么条件？", specs[2]["document_id"]),
            )
        for label, question, expected in query_cases:
            with phase(report, ledger, label, embedding_text_type="query"):
                query(http, question, report, expected=expected)
        with psycopg.connect(values["V2_GATEWAY_DSN"]) as conn:
            lifecycle_states = {row[0]: row[1:] for row in conn.execute(
                "SELECT d.logical_key,v.id::text,v.status,v.review_status,v.disclosure_class,v.external_allowed "
                "FROM document_version v JOIN document d ON d.id=v.document_id AND d.tenant_id=v.tenant_id")}
            for spec in specs:
                version, state, reviewed, disclosure, external = lifecycle_states[spec["document_id"]]
                withheld = spec["disclosure_class"] == "unclassified"
                if (disclosure != spec["disclosure_class"] or external != (disclosure == "external_allowed")
                        or state != ("ready" if withheld else "active")
                        or reviewed != ("rejected" if withheld else "approved")
                        or (report["withheld"] if withheld else report["published"])[spec["document_id"]] != version):
                    raise AcceptanceFailure("final lifecycle classification or visibility changed")
            report["publication_counts"] = {"published": len(report["published"]),
                                             "withheld": len(report["withheld"])}
            report["indexed_chunks"] = [dict(zip(("evidence_id", "document_id", "version_id", "source_sha256",
                "chunk_sha256", "embedding_model", "embedding_dim", "effective_from", "effective_to"), row)) for row in conn.execute(
                "SELECT 'db-chunk-'||c.id,d.logical_key,c.version_id::text,v.source_hash,c.chunk_hash,c.embedding_model,c.embedding_dim,"
                "c.effective_from::text,c.effective_to::text "
                "FROM chunk c JOIN document d ON d.id=c.doc_id AND d.tenant_id=c.tenant_id "
                "JOIN document_version v ON v.id=c.version_id AND v.tenant_id=c.tenant_id ORDER BY c.id")]
            for chunk in report["indexed_chunks"]:
                spec = selected[chunk["document_id"]]
                if any(chunk[key] != spec.get(key) for key in ("effective_from", "effective_to")):
                    raise AcceptanceFailure("indexed source effective dates changed")
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
