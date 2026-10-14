#!/usr/bin/env python3
"""Audit a resolved, pinned Medusa 2.18.0 closure before installation/deployment.

Reads a package lock and npm audit report. Fetches only undeclared-license
package artifacts for license inspection; never installs or runs package scripts.
Outputs public metadata and hashes, with full artifacts in restricted storage.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import os
import tarfile
import urllib.request
from collections import Counter
from pathlib import Path

REVIEWED = {"MIT", "Apache-2.0", "0BSD", "BSD-2-Clause", "BSD-3-Clause", "ISC",
            "(MIT OR CC0-1.0)", "CC-BY-4.0", "BlueOak-1.0.0"}
SOURCE_LICENSE_SHA256 = "f792b19e548b936c2a9a8e489e2f21e3b5a91e482910c57d9c9508f8933789af"


def license_text_kind(text):
    lower = text.lower()
    if "permission is hereby granted, free of charge" in lower and "the software is provided" in lower:
        return "MIT"
    if "redistribution and use in source and binary forms" in lower and "this software is provided" in lower:
        return "BSD_text_requires_notice_retention"
    return None


def artifact_license(record, directory, source_license_hash, package):
    url = record.get("resolved", "")
    if not url.startswith("https://registry.npmjs.org/") or not record.get("integrity", "").startswith("sha512-"):
        raise ValueError("unpinned_package_artifact")
    request = urllib.request.Request(url, headers={"User-Agent": "EcommerceRAG-license-audit/1.0"})
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read(32 * 1024 * 1024 + 1)
    if len(data) > 32 * 1024 * 1024:
        raise ValueError("package_artifact_size_exceeded")
    expected = record["integrity"].split("-", 1)[1]
    if base64.b64encode(hashlib.sha512(data).digest()).decode() != expected:
        raise ValueError("package_artifact_integrity_mismatch")
    digest = hashlib.sha256(data).hexdigest()
    destination = directory / (digest + ".tgz")
    if not destination.exists():
        with os.fdopen(os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as output:
            output.write(data)
    found = []
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for item in archive.getmembers():
            if item.isfile() and Path(item.name).name.lower().startswith(("license", "licence", "copying")) and item.size < 131072:
                text = archive.extractfile(item).read().decode("utf-8", errors="replace")
                found.append({"path": item.name, "sha256": hashlib.sha256(text.encode()).hexdigest(),
                              "license": license_text_kind(text)})
    resolved = [row for row in found if row["license"]]
    if resolved:
        return {"artifact_sha256": digest, "license_basis": "artifact_notice", "notices": resolved}
    if package.startswith("node_modules/@medusajs/") and record["version"] == "2.18.0":
        return {"artifact_sha256": digest, "license_basis": "pinned_monorepo_source_MIT",
                "source_license_sha256": source_license_hash, "retain_source_notice": True}
    raise ValueError("package_license_unresolved")


def run(args):
    lock = json.loads(args.lock.read_text())
    for name in ("@medusajs/medusa", "@medusajs/framework", "@medusajs/cli"):
        if lock["packages"].get("node_modules/" + name, {}).get("version") != "2.18.0":
            raise ValueError("selected_version_mismatch")
    source = args.source_license.read_bytes()
    if hashlib.sha256(source).hexdigest() != SOURCE_LICENSE_SHA256 or license_text_kind(source.decode()) != "MIT":
        raise ValueError("source_MIT_notice_missing")
    args.artifact_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(args.artifact_dir, 0o700)
    rows, blocking = [], []
    for path, record in lock["packages"].items():
        if not path:
            continue
        row = {"package_path": path, "version": record["version"], "dev": bool(record.get("dev")),
               "optional": bool(record.get("optional")), "license_declaration": record.get("license"),
               "integrity": record.get("integrity")}
        if not record.get("integrity", "").startswith("sha512-") or not record.get("resolved", "").startswith("https://registry.npmjs.org/"):
            blocking.append({"package_path": path, "reason": "unpinned_registry_artifact"})
        if record.get("license") not in REVIEWED:
            try:
                row.update(artifact_license(record, args.artifact_dir, hashlib.sha256(source).hexdigest(), path))
            except Exception:  # noqa: BLE001 - fixed failure, no arbitrary transport output
                blocking.append({"package_path": path, "reason": "unresolved_license_or_artifact"})
        rows.append(row)
    audit = json.loads(args.audit.read_text())
    if audit.get("error") or "vulnerabilities" not in audit:
        blocking.append({"reason": "security_audit_unavailable"})
    for name, vulnerability in audit.get("vulnerabilities", {}).items():
        if vulnerability["severity"] in {"high", "critical"}:
            blocking.append({"package": name, "reason": "high_or_critical_security_advisory",
                             "severity": vulnerability["severity"], "range": vulnerability["range"],
                             "fix_available": vulnerability.get("fixAvailable")})
    report = {"report_version": "medusa-lock-audit-v1", "status": "PASS" if not blocking else "NOT_PASSED",
              "version": "2.18.0", "lock_sha256": hashlib.sha256(args.lock.read_bytes()).hexdigest(),
              "source_license_sha256": hashlib.sha256(source).hexdigest(),
              "security_audit_sha256": hashlib.sha256(args.audit.read_bytes()).hexdigest(),
              "packages": rows, "license_counts": dict(Counter(r["license_declaration"] or "artifact_review" for r in rows)),
              "security_counts": audit.get("metadata", {}).get("vulnerabilities"), "blocking": blocking,
              "installation_scripts_executed": False, "deployed": False, "real_service_acceptance": False}
    with os.fdopen(os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as output:
        json.dump(report, output, indent=2)
    print(json.dumps({"status": report["status"], "package_count": len(rows), "blocking": blocking}))
    return 0 if not blocking else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("lock", "audit", "source-license", "artifact-dir", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    try:
        return run(parser.parse_args())
    except Exception:  # noqa: BLE001 - diagnostics never print runtime paths/transport details
        print(json.dumps({"status": "NOT_PASSED", "failure_code": "medusa_lock_audit_failed"}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
