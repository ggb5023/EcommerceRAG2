#!/usr/bin/env bash
# One-click synthetic demo shop: start the isolated stack, import demo-store-v1,
# review and publish it through the existing admin API, or reset only its versions.
#
#   bash scripts/dev/demo-shop.sh [--ports 26081] [--with-vite] [--package PATH]
#   bash scripts/dev/demo-shop.sh [--ports 26081] --reset [--package PATH]
#
# All business writes go through the admin HTTP API. The script never writes SQL,
# never touches the systemd dev stack, and never resets other tenants/shops.
set -euo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
package="$repo/data/synthetic/demo-store-v1"
python="$repo/python/.venv/bin/python"
ports=26081
with_vite=0
reset=0
base_url="${ECR_DEMO_BASE_URL:-}"

usage() {
  sed -n '2,8p' "${BASH_SOURCE[0]}" >&2
  exit 2
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ports) ports=${2:-}; shift 2 ;;
    --with-vite) with_vite=1; shift ;;
    --reset) reset=1; shift ;;
    --package) package=${2:-}; shift 2 ;;
    --base-url) base_url=${2:-}; shift 2 ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[[ "$ports" =~ ^[0-9]+$ ]] || { echo "FAIL --ports must be numeric" >&2; exit 2; }
[[ -r "$package/manifest.yaml" ]] || { echo "FAIL demo package missing: $package" >&2; exit 2; }
[[ -x "$python" ]] || { echo "FAIL python venv missing; run uv sync --frozen" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "FAIL curl is required" >&2; exit 2; }

if [[ -z "$base_url" ]]; then
  base_url="http://127.0.0.1:$ports"
  if ! curl -sS -m 2 -o /dev/null "$base_url/healthz" 2>/dev/null; then
    stack_args=(--identity development --ports "$ports" --keep)
    [[ $with_vite -eq 1 ]] && stack_args+=(--with-vite)
    bash "$repo/scripts/dev/isolated-stack.sh" "${stack_args[@]}" || {
      echo "FAIL isolated stack did not start" >&2
      exit 1
    }
  fi
fi
for _ in $(seq 1 60); do
  if curl -sS -m 2 -o /dev/null -w '%{http_code}' "$base_url/healthz" 2>/dev/null | grep -qx 200; then
    break
  fi
  sleep 1
done
curl -sS -m 2 -o /dev/null -w '%{http_code}' "$base_url/v1/session" 2>/dev/null | grep -Eq '^(200|401|403)$' || {
  echo "FAIL demo stack is not answering at $base_url" >&2
  exit 1
}

exec "$python" - "$base_url" "$package" "$reset" <<'PY'
import hashlib
import json
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

import yaml

base_url, package = sys.argv[1].rstrip("/"), Path(sys.argv[2])
reset_only = sys.argv[3] == "1"
manifest_bytes = (package / "manifest.yaml").read_bytes()
manifest = yaml.safe_load(manifest_bytes)
label = str(manifest.get("dataset_id") or package.name)
docs = manifest.get("documents") or []
business_date = str(manifest.get("business_date", "")).strip()
if not docs or not business_date:
    raise SystemExit("FAIL manifest needs documents and business_date")
doc_ids = {doc["document_id"] for doc in docs}
hashes = {doc["document_id"]: doc["sha256"] for doc in docs}
package_hash = hashlib.sha256()
for path in sorted(value for value in package.rglob("*") if value.is_file()):
    package_hash.update(path.relative_to(package).as_posix().encode() + b"\0")
    package_hash.update(hashlib.sha256(path.read_bytes()).digest())
print(f"PACKAGE files={sum(1 for value in package.rglob('*') if value.is_file())} "
      f"package_sha256={package_hash.hexdigest()} manifest_sha256={hashlib.sha256(manifest_bytes).hexdigest()}")
for doc in docs:
    print(f"PACKAGE document={doc['document_id']} sha256={doc['sha256']}")


def request(path, *, method="GET", body=None, content_type=None):
    headers = {"Accept": "application/json"}
    if content_type:
        headers["Content-Type"] = content_type
    req = urllib.request.Request(base_url + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            raw = response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:800]
        raise RuntimeError(f"{method} {path} -> {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {path} -> {exc.reason}") from exc
    try:
        return response.status, json.loads(raw or b"{}")
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"{method} {path} returned non-JSON: {raw[:200]!r}") from exc


def session():
    status, body = request("/v1/session")
    if status != 200 or not body.get("tenantId") or not body.get("shopId"):
        raise RuntimeError(f"admin session unavailable: {status} {body}")
    return body


def review_items():
    status, body = request("/admin/v1/merchant/reviews")
    if status != 200:
        raise RuntimeError(f"review queue unavailable: {status} {body}")
    return body.get("items") or []


def matching_version(version_items, document_id, source_hash, job_id=None):
    for item in version_items:
        if (item.get("document_id") == document_id and item.get("source_hash") == source_hash
                and item.get("status") != "revoked"
                and (job_id is None or str(item.get("job_id")) == str(job_id))):
            return item
    return None


def refresh_until_versions(job_id):
    for _ in range(120):
        rows = review_items()
        if all(matching_version(rows, doc["document_id"], doc["sha256"], job_id) for doc in docs):
            return rows
        time.sleep(1)
    raise RuntimeError(f"worker did not produce all demo versions for job={job_id} in 120 seconds")


def multipart(fields, files):
    boundary = "ecr-demo-" + uuid.uuid4().hex
    chunks = []
    for name, value in fields:
        chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    for name, filename, content in files:
        chunks.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; filename="{filename}"\r\n'
            f"Content-Type: application/octet-stream\r\n\r\n".encode()
        )
        chunks.append(content)
        chunks.append(b"\r\n")
    chunks.append(f"--{boundary}--\r\n".encode())
    return b"".join(chunks), f"multipart/form-data; boundary={boundary}"


def command(path, payload):
    status, body = request(path, method="POST", body=json.dumps(payload).encode(), content_type="application/json")
    if status not in (200, 201, 202):
        raise RuntimeError(f"POST {path} -> {status}: {body}")
    return body


def revoke(item, auth):
    document_id = item.get("document_id")
    source_hash = item.get("source_hash")
    payload = {
        "tenant_id": auth["tenantId"], "shop_id": auth["shopId"], "document_id": document_id,
        "version_hash": source_hash, "permission_revision": auth["permission_revision"],
        "expected_fencing_epoch": item.get("fencing_epoch", 0), "confirm": True,
        "reason": f"{label} reset: revoke only this synthetic demo source",
    }
    command(f"/admin/v1/merchant/versions/{item['version_id']}/revoke", payload)


def reset_demo(auth):
    count = 0
    for item in review_items():
        if (item.get("document_id") not in doc_ids
                or item.get("status") in ("revoked", "superseded")):
            continue
        revoke(item, session())
        count += 1
        print(f"REVOKED version={item['version_id']} document={item['document_id']} status={item.get('status')}")
    print(f"RESET demo_versions={count}")
    return 0


def publish_document(item):
    auth = session()
    if item.get("status") == "active" and item.get("review_status") == "approved":
        return
    if item.get("review_status") != "approved":
        review = {
            "expected_hash": item["source_hash"], "expected_fencing_epoch": item.get("fencing_epoch", 0),
            "permission_revision": auth["permission_revision"], "confirm": True,
            "reason": f"{label} seed: synthetic demo review",
            "decision": "approved", "disclosure_class": "external_allowed", "external_allowed": True,
        }
        command(f"/admin/v1/merchant/versions/{item['version_id']}/review", review)
    auth = session()
    current = matching_version(review_items(), item["document_id"], item["source_hash"], item.get("job_id"))
    if current is None:
        raise RuntimeError(f"version disappeared during review: {item['document_id']}")
    if current.get("status") == "active":
        return
    publish = {
        "tenant_id": auth["tenantId"], "shop_id": auth["shopId"], "document_id": item["document_id"],
        "version_hash": item["source_hash"], "permission_revision": auth["permission_revision"],
        "expected_fencing_epoch": current.get("fencing_epoch", 0), "confirm": True,
        "reason": f"{label} seed: synthetic demo publication",
    }
    command(f"/admin/v1/merchant/versions/{item['version_id']}/publish", publish)
    print(f"ACTIVE document={item['document_id']} version={item['version_id']}")


auth = session()
if reset_only:
    raise SystemExit(reset_demo(auth))

all_active = all(
    any(item.get("document_id") == doc["document_id"] and item.get("source_hash") == doc["sha256"]
        and item.get("status") == "active" and item.get("review_status") == "approved" for item in review_items())
    for doc in docs
)
if all_active:
    print(f"SKIP {label} already active manifest_sha256={hashlib.sha256(manifest_bytes).hexdigest()}")
    raise SystemExit(0)

# A reset revokes versions but keeps the immutable manifest bytes; add a
# runtime-only nonce so the ingest idempotency key does not resurrect the
# revoked job. The package manifest hash remains the identity printed below.
manifest_text = manifest_bytes.decode()
existing_rows = review_items()
if any(item.get("document_id") in doc_ids and item.get("status") == "revoked" for item in existing_rows):
    runtime_manifest = dict(manifest)
    runtime_manifest["demo_run"] = uuid.uuid4().hex
    manifest_text = yaml.safe_dump(runtime_manifest, allow_unicode=True, sort_keys=False)
    print("RUN nonce issued for a reset demo import")

fields = [("shop_id", "shop-demo"), ("business_date", business_date), ("manifest", manifest_text)]
files = []
for doc in docs:
    path = package / doc["path"]
    content = path.read_bytes()
    fields.append(("paths", doc["path"]))
    files.append(("files", doc["path"], content))
body, content_type = multipart(fields, files)
status, created = request("/admin/v1/merchant/imports", method="POST", body=body, content_type=content_type)
if status != 201:
    raise RuntimeError(f"import batch creation failed: {status} {created}")
if created.get("blocks") != 0:
    raise RuntimeError(f"import precheck has blocks: {created}")
batch = created["batch_id"]
print(f"PRECHECK batch={batch} blocks=0 item_count={created.get('item_count')} business_date={business_date}")
status, preview = request(f"/admin/v1/merchant/imports/{batch}")
if status != 200 or preview.get("business_date") != business_date:
    raise RuntimeError(f"batch preview mismatch: {status} {preview}")
preview_items = {item.get("precheck_detail", {}).get("document_id"): item
                 for item in preview.get("items") or []}
if set(preview_items) != doc_ids:
    raise RuntimeError(f"batch preview documents mismatch: {sorted(preview_items)}")
for doc in docs:
    item = preview_items[doc["document_id"]]
    detail = item.get("precheck_detail") or {}
    if detail.get("source_hash") != doc["sha256"]:
        raise RuntimeError(f"precheck source hash mismatch: {doc['document_id']}")
    blocking = [row for row in item.get("precheck_result") or []
                if str(row.get("severity", "")).lower() in ("block", "error", "fatal")]
    if blocking:
        raise RuntimeError(f"precheck contains blocking rows: {doc['document_id']} {blocking}")
status, confirmed = request(f"/admin/v1/merchant/imports/{batch}/confirm", method="POST",
                            body=json.dumps({"confirm": True, "reason": f"{label} seed: confirm synthetic package"}).encode(),
                            content_type="application/json")
if status != 202:
    raise RuntimeError(f"batch confirmation failed: {status} {confirmed}")
job_id = str(confirmed.get("job_id"))
if not job_id or job_id == "None":
    raise RuntimeError(f"batch confirmation did not return a job id: {confirmed}")
print(f"CONFIRMED batch={batch} job={job_id}")
items = refresh_until_versions(job_id)
auth = session()
priority = {"size_chart": 0, "product_table": 1}
for doc in sorted(docs, key=lambda value: priority.get(value.get("doc_type"), 2)):
    item = matching_version(items, doc["document_id"], doc["sha256"], job_id)
    if item is None:
        raise RuntimeError(f"missing version for {doc['document_id']}")
    publish_document(item)

final = review_items()
for doc in docs:
    item = matching_version(final, doc["document_id"], doc["sha256"], job_id)
    if item is None or item.get("status") != "active" or item.get("review_status") != "approved":
        raise RuntimeError(f"demo document from job {job_id} is not active: {doc['document_id']}")
    print(f"VERIFIED document={doc['document_id']} version={item['version_id']} job={job_id} status=active review=approved")
stale = [item for item in final if item.get("document_id") in doc_ids
         and item.get("status") not in ("active", "revoked", "superseded")]
if stale:
    raise RuntimeError(f"demo documents still pending outside job {job_id}: {stale}")
print(f"PASS {label} manifest_sha256={hashlib.sha256(manifest_bytes).hexdigest()}")
PY
