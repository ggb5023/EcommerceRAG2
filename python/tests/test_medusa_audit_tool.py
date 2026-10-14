"""License declarations cannot hide unsafe or unpinned dependency closures."""
import importlib.util
import json
from argparse import Namespace
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("medusa_audit", ROOT / "scripts/audit-medusa-lock.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def inputs(tmp_path, monkeypatch):
    import hashlib
    source = tmp_path / "LICENSE"
    source.write_text("Permission is hereby granted, free of charge. The software is provided as is.")
    monkeypatch.setattr(module, "SOURCE_LICENSE_SHA256", hashlib.sha256(source.read_bytes()).hexdigest())
    record = {"version": "2.18.0", "license": "MIT", "integrity": "sha512-fixture",
              "resolved": "https://registry.npmjs.org/synthetic-fixture"}
    lock = tmp_path / "lock.json"
    lock.write_text(json.dumps({"packages": {"node_modules/" + n: record.copy() for n in
                                          ("@medusajs/medusa", "@medusajs/framework", "@medusajs/cli")}}))
    audit = tmp_path / "audit.json"
    audit.write_text(json.dumps({"vulnerabilities": {}}))
    return Namespace(lock=lock, audit=audit, source_license=source, artifact_dir=tmp_path / "artifacts",
                     report=tmp_path / "report.json")


def test_security_advisory_prevents_pass_even_with_MIT(tmp_path, monkeypatch):
    args = inputs(tmp_path, monkeypatch)
    args.audit.write_text(json.dumps({"vulnerabilities": {"synthetic-dependency": {
        "severity": "high", "range": "*", "fixAvailable": False}}}))
    assert module.run(args) == 1
    report = json.loads(args.report.read_text())
    assert report["status"] == "NOT_PASSED" and report["deployed"] is False
    assert report["blocking"][0]["reason"] == "high_or_critical_security_advisory"


def test_missing_security_report_cannot_claim_safe(tmp_path, monkeypatch):
    args = inputs(tmp_path, monkeypatch)
    args.audit.write_text('{"error":{"code":"synthetic-unavailable"}}')
    assert module.run(args) == 1
    assert json.loads(args.report.read_text())["blocking"][0]["reason"] == "security_audit_unavailable"


def test_selected_version_cannot_change_silently(tmp_path, monkeypatch):
    args = inputs(tmp_path, monkeypatch)
    value = json.loads(args.lock.read_text())
    value["packages"]["node_modules/@medusajs/medusa"]["version"] = "2.21.2"
    args.lock.write_text(json.dumps(value))
    with pytest.raises(ValueError, match="selected_version_mismatch"):
        module.run(args)


def test_unpinned_artifact_prevents_pass(tmp_path, monkeypatch):
    args = inputs(tmp_path, monkeypatch)
    value = json.loads(args.lock.read_text())
    value["packages"]["node_modules/@medusajs/medusa"].pop("integrity")
    args.lock.write_text(json.dumps(value))
    assert module.run(args) == 1
    assert any(r["reason"] == "unpinned_registry_artifact" for r in json.loads(args.report.read_text())["blocking"])
