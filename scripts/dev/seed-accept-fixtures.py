#!/usr/bin/env python3
"""Stage and confirm an import through the admin HTTP API, never through SQL.

The e2e browser cases need at least one version waiting for review. This script
uploads the first N documents of an existing fixture package, confirms the batch,
and waits until the review queue reports N pending versions.

It is idempotent: when the queue already has enough pending versions it does
nothing and exits 0.

    python3 scripts/dev/seed-accept-fixtures.py --count 2
    python3 scripts/dev/seed-accept-fixtures.py --cookies "session=..."   # other environments
"""

from __future__ import annotations

import argparse
import json
import os
import secrets
import sys
import tempfile
import time
import urllib.error
import urllib.request
from datetime import date
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - developer tooling only
    sys.exit("PyYAML is required to rewrite the fixture manifest")

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_FIXTURES = ROOT / "tests" / "fixtures" / "import-batch-20"
REASON = "seed the review queue for browser acceptance cases"


def call(base: str, path: str, headers: dict[str, str], body: bytes | None = None, content_type: str | None = None):
    request = urllib.request.Request(base + path, data=body, headers=dict(headers), method="POST" if body is not None else "GET")
    if content_type:
        request.add_header("Content-Type", content_type)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:400]
        return exc.code, {"error": detail}
    except urllib.error.URLError as exc:
        return 0, {"error": str(exc.reason)}


def multipart(fields: list[tuple[str, str]], files: list[tuple[str, str, bytes]]) -> tuple[bytes, str]:
    boundary = "ecr" + secrets.token_hex(12)
    chunks: list[bytes] = []
    for name, value in fields:
        chunks.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode()
        )
    for name, filename, content in files:
        chunks.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; filename="{filename}"\r\n'
            f"Content-Type: text/markdown\r\n\r\n".encode()
        )
        chunks.append(content)
        chunks.append(b"\r\n")
    chunks.append(f"--{boundary}--\r\n".encode())
    return b"".join(chunks), f"multipart/form-data; boundary={boundary}"


def corpus_package(document_id: str, workdir: Path, fixture_id: str) -> Path:
    """Materialise one corpus document as an import package."""
    import hashlib
    import os

    root = Path(os.environ.get(
        "ECR_EVAL_DOCUMENTS",
        "/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/documents.jsonl",
    ))
    if not root.is_file():
        sys.exit(f"eval corpus is unavailable: {root}")
    for line in root.read_text(encoding="utf-8").splitlines():
        record = json.loads(line)
        if record.get("document_id") != document_id:
            continue
        text = "\n\n".join(chunk["content"] for chunk in record.get("chunks") or [])
        workdir.mkdir(parents=True, exist_ok=True)
        target = workdir / "document.md"
        target.write_text(text + "\n", encoding="utf-8")
        manifest = {
            "schema_version": 1,
            "source": {"id": "answer-fixture", "name": "answer fixture"},
            "pipeline_version": "v2-import-v1",
            "documents": [{
                "document_id": fixture_id, "title": "配送时效核验资料", "path": "document.md",
                "format": "markdown", "sha256": hashlib.sha256(target.read_bytes()).hexdigest(),
                # The manifest proposes internal_only; the reviewer raises the
                # classification to external_allowed during review.
                "license": "internal", "disclosure_class": "internal_only",
            }],
        }
        (workdir / "manifest.yaml").write_text(yaml.safe_dump(manifest, allow_unicode=True, sort_keys=False), encoding="utf-8")
        return workdir
    sys.exit(f"corpus document not found: {document_id}")


def active_versions(base: str, headers: dict[str, str]) -> list[dict]:
    status, payload = call(base, "/admin/v1/merchant/versions", headers)
    if status != 200:
        sys.exit(f"versions unavailable: {status} {payload}")
    return [item for item in payload.get("items", [])
            if item.get("status") == "active" and (item.get("chunk_count") or 0) > 0]


def business_date() -> tuple[str, str]:
    """Prefer the date the services were started with, so prompts stay reproducible."""
    from_env = os.environ.get("SYNTHETIC_BUSINESS_DATE", "").strip()
    if from_env:
        return from_env, "SYNTHETIC_BUSINESS_DATE"
    runtime = Path(os.environ.get("ECR_RUNTIME_ENV", "/etc/ecommerce-rag/dev.env"))
    if runtime.is_file():
        for line in runtime.read_text(encoding="utf-8").splitlines():
            key, _, value = line.strip().partition("=")
            if key == "SYNTHETIC_BUSINESS_DATE" and value.strip():
                return value.strip().strip("'\""), str(runtime)
    return date.today().isoformat(), "today"


def pending_count(base: str, headers: dict[str, str]) -> int:
    status, payload = call(base, "/admin/v1/merchant/reviews", headers)
    if status != 200:
        sys.exit(f"reviews unavailable: {status} {payload}")
    return sum(1 for item in payload.get("items", []) if item.get("review_status") == "pending")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--cookies", default="", help="Cookie header value for a real session")
    parser.add_argument("--token", default="", help="Bearer token when the deployment uses one")
    parser.add_argument("--count", type=int, default=2)
    parser.add_argument("--shop", default="", help="shop_id, defaults to the first shop in scope")
    parser.add_argument("--fixtures", type=Path, default=DEFAULT_FIXTURES)
    parser.add_argument("--from-corpus", default="", metavar="DOCUMENT_ID",
                        help="build the package from one document of the eval corpus")
    parser.add_argument("--document-id", default="w2-delivery",
                        help="fixture document id for corpus mode; change it after a revoke")
    parser.add_argument("--publish", action="store_true",
                        help="review as external_allowed and publish, then require an active version")
    parser.add_argument("--timeout", type=int, default=180, help="seconds to wait for the ingest worker")
    args = parser.parse_args()
    used_date, date_source = business_date()
    print(f"BUSINESS_DATE {used_date} ({date_source})")
    base = args.base_url.rstrip("/")
    headers = {}
    if args.token:
        headers["Authorization"] = "Bearer " + args.token
    if args.cookies:
        headers["Cookie"] = args.cookies

    fixtures = args.fixtures
    if args.from_corpus:
        fixtures = corpus_package(args.from_corpus, Path(tempfile.mkdtemp(prefix="ecr-answer-")), args.document_id)
        args.count = 1
        if args.publish:
            for item in active_versions(base, headers):
                if item.get("document_id") == args.document_id:
                    print(f"SKIP {args.document_id} already active as version {item['version_id']} "
                          f"({item.get('chunk_count')} chunk(s))")
                    return 0

    manifest_path = fixtures / "manifest.yaml"
    if not manifest_path.is_file():
        return fail(f"fixture manifest missing: {manifest_path}")
    manifest = yaml.safe_load(manifest_path.read_text(encoding="utf-8"))
    documents = manifest.get("documents") or []
    if len(documents) < args.count:
        return fail(f"fixture has {len(documents)} documents, --count wants {args.count}")

    existing = 0 if args.from_corpus else pending_count(base, headers)
    if not args.from_corpus and existing >= args.count:
        print(f"SKIP {existing} pending version(s) already in the review queue (wanted {args.count})")
        return 0

    status, shops = call(base, "/admin/v1/merchant/shops", headers)
    if status != 200:
        return fail(f"shops unavailable: {status} {shops}")
    shop = args.shop or (shops.get("items") or [{}])[0].get("shop_id", "")
    if not shop:
        return fail("no shop in scope; pass --shop")

    selected = documents[: args.count]
    manifest["documents"] = selected
    manifest_text = yaml.safe_dump(manifest, allow_unicode=True, sort_keys=False)
    files = []
    for document in selected:
        path = fixtures / document["path"]
        if not path.is_file():
            return fail(f"fixture file missing: {path}")
        files.append(("files", document["path"], path.read_bytes()))
    fields = [("shop_id", shop), ("manifest", manifest_text), ("business_date", used_date)]
    fields += [("paths", document["path"]) for document in selected]
    body, content_type = multipart(fields, files)

    status, created = call(base, "/admin/v1/merchant/imports", headers, body, content_type)
    if status != 201:
        return fail(f"import create failed: {status} {created}")
    batch = created.get("batch_id")
    print(f"STAGED batch={batch} shop={shop} items={created.get('item_count')} blocks={created.get('blocks')} warnings={created.get('warnings')}")

    confirm = json.dumps({"confirm": True, "reason": REASON}).encode()
    status, confirmed = call(base, f"/admin/v1/merchant/imports/{batch}/confirm", headers, confirm, "application/json")
    if status != 202:
        return fail(f"import confirm failed: {status} {confirmed}")
    print(f"CONFIRMED batch={batch} job={confirmed.get('job_id')}")

    document_ids = {args.document_id} if args.from_corpus else {document["document_id"] for document in selected}
    deadline = time.time() + args.timeout
    # Wait for *our* documents, not for any pending row: other imports may already
    # sit in the queue and would satisfy a global count immediately.
    while time.time() < deadline:
        status, reviews = call(base, "/admin/v1/merchant/reviews", headers)
        if status == 200:
            waiting = [item for item in reviews.get("items", [])
                       if item.get("document_id") in document_ids and item.get("review_status") == "pending"]
            if len(waiting) >= args.count:
                print(f"PASS pending versions for {sorted(document_ids)}: {len(waiting)} (wanted {args.count})")
                break
        time.sleep(3)
    else:
        return fail(f"no pending version for {sorted(document_ids)} after {args.timeout}s")
    if not args.publish:
        return 0
    return publish_pending(base, headers, document_ids, args.timeout)


def publish_pending(base: str, headers: dict[str, str], document_ids: set[str], timeout: int) -> int:
    """Review every pending version of the given documents as external and publish it."""
    status, reviews = call(base, "/admin/v1/merchant/reviews", headers)
    if status != 200:
        return fail(f"reviews unavailable: {status} {reviews}")
    targets = [item for item in reviews.get("items", [])
               if item.get("document_id") in document_ids and item.get("review_status") == "pending"]
    if not targets:
        return fail(f"no pending version for {sorted(document_ids)}")
    status, session = call(base, "/admin/v1/session", headers)
    if status != 200:
        return fail(f"session unavailable: {status} {session}")
    revision = session["permission_revision"]
    for item in targets:
        version = item["version_id"]
        review = {
            "confirm": True, "decision": "approved", "disclosure_class": "external_allowed",
            "external_allowed": True, "expected_hash": item["source_hash"],
            "expected_fencing_epoch": item["fencing_epoch"], "permission_revision": revision,
            "reason": REASON,
        }
        status, reviewed = call(base, f"/admin/v1/merchant/versions/{version}/review", headers,
                                json.dumps(review).encode(), "application/json")
        if status not in (200, 202):
            return fail(f"review failed for version {version}: {status} {reviewed}")
        publish = {
            "confirm": True, "tenant_id": session["tenant_id"], "shop_id": session["shop_id"],
            "document_id": item["document_id"], "version_hash": item["source_hash"],
            "permission_revision": revision, "expected_fencing_epoch": item["fencing_epoch"],
            "reason": REASON,
        }
        status, published = call(base, f"/admin/v1/merchant/versions/{version}/publish", headers,
                                 json.dumps(publish).encode(), "application/json")
        if status not in (200, 202):
            return fail(f"publish failed for version {version}: {status} {published}")
        print(f"PUBLISHED version={version} document={item['document_id']}")

    deadline = time.time() + timeout
    while time.time() < deadline:
        active = [item for item in active_versions(base, headers) if item.get("document_id") in document_ids]
        if active:
            print(f"PASS active version(s): " + ", ".join(
                f"{item['version_id']}(chunks={item.get('chunk_count')})" for item in active))
            return 0
        time.sleep(3)
    return fail("no active version with chunks after publish")


def fail(message: str) -> int:
    print(f"FAIL {message}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
