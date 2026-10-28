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
import secrets
import sys
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
    parser.add_argument("--timeout", type=int, default=180, help="seconds to wait for the ingest worker")
    args = parser.parse_args()
    base = args.base_url.rstrip("/")
    headers = {}
    if args.token:
        headers["Authorization"] = "Bearer " + args.token
    if args.cookies:
        headers["Cookie"] = args.cookies

    manifest_path = args.fixtures / "manifest.yaml"
    if not manifest_path.is_file():
        return fail(f"fixture manifest missing: {manifest_path}")
    manifest = yaml.safe_load(manifest_path.read_text(encoding="utf-8"))
    documents = manifest.get("documents") or []
    if len(documents) < args.count:
        return fail(f"fixture has {len(documents)} documents, --count wants {args.count}")

    existing = pending_count(base, headers)
    if existing >= args.count:
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
        path = args.fixtures / document["path"]
        if not path.is_file():
            return fail(f"fixture file missing: {path}")
        files.append(("files", document["path"], path.read_bytes()))
    fields = [("shop_id", shop), ("manifest", manifest_text), ("business_date", date.today().isoformat())]
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

    deadline = time.time() + args.timeout
    pending = existing
    while time.time() < deadline:
        pending = pending_count(base, headers)
        if pending >= args.count:
            print(f"PASS pending versions in the review queue: {pending} (wanted {args.count})")
            return 0
        time.sleep(3)
    return fail(f"review queue still has {pending} pending version(s), wanted {args.count}")


def fail(message: str) -> int:
    print(f"FAIL {message}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
