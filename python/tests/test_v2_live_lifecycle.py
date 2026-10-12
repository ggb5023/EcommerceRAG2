"""Offline checks of the paid acceptance boundary and real HTTP contracts."""
import importlib.util
import json
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("live_lifecycle", ROOT / "eval/run_v2_live_lifecycle.py")
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


def test_plan_parses_all_formats_without_network_and_is_reproducible(monkeypatch, capsys):
    def forbidden(*args, **kwargs):
        pytest.fail("offline planning attempted HTTP")
    monkeypatch.setattr(runner.HTTP, "call", forbidden)
    monkeypatch.setattr(runner.sys, "argv", ["run_v2_live_lifecycle.py"])
    assert runner.main() == 0
    report = json.loads(capsys.readouterr().out)
    assert report["status"] == "PLAN" and report["real_service_acceptance"] is False
    assert {item["format"] for item in report["inputs"]} == {"markdown", "csv", "docx"}
    assert all(item["preflight"]["chunks"] > 0 for item in report["inputs"])
    from decimal import Decimal
    assert Decimal(report["budget_plan"]["upper_bound_cny"]) < 6
    left = runner.fixtures("first", "tenant", "shop")
    right = runner.fixtures("second", "tenant", "shop")
    assert [item["sha256"] for item in left] == [item["sha256"] for item in right]
    assert "content" not in report["inputs"][0]


@pytest.mark.parametrize("base", ["http://example.com", "https://127.0.0.1", "http://user@127.0.0.1",
                                 "http://127.0.0.1/private", "http://127.0.0.1?x=1"])
def test_paid_runner_rejects_non_local_bases(base):
    with pytest.raises(runner.AcceptanceFailure):
        runner.HTTP(base)


def test_live_report_is_not_overwritten_or_followed(monkeypatch, tmp_path):
    target = tmp_path / "report.json"
    target.write_text("existing evidence")
    link = tmp_path / "report-link.json"
    link.symlink_to(target)
    for path in (target, link):
        monkeypatch.setattr(runner.sys, "argv", ["runner", "--live", "--runtime-env", "missing", "--report", str(path)])
        assert runner.main() == 2
        assert target.read_text() == "existing evidence"


def test_live_address_mismatch_fails_before_provider_or_database(monkeypatch, tmp_path):
    config = tmp_path / "runtime.env"
    config.write_text("RAG_PROFILE=v2_langchain_bailian\nHTTP_ADDR=127.0.0.1:18080\n")
    config.chmod(0o600)
    report = tmp_path / "failure.json"
    monkeypatch.setattr(runner.sys, "argv", ["runner", "--live", "--runtime-env", str(config), "--report", str(report)])
    assert runner.main() == 2
    result = json.loads(report.read_text())
    assert result["failure"] == "HTTP base must match the reviewed runtime address"
    assert report.stat().st_mode & 0o777 == 0o600


def test_command_unknown_commit_is_polled_without_resending(monkeypatch):
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)
    class Client:
        def __init__(self):
            self.calls = []
        def call(self, path, body=None):
            self.calls.append((path, body))
            if body is not None:
                return {"command_id": "c1", "command_status": "pending", "status": "processing"}
            return {"command_id": "c1", "command_status": "done", "status": "active", "audit_result": "success"}
    client = Client()
    assert runner.command(client, "/publish", {"confirm": True})["status"] == "active"
    assert client.calls == [("/publish", {"confirm": True}), ("/admin/v1/merchant/commands/c1", None)]


@pytest.mark.parametrize("version,citations", [("old", [{"evidence_id": "chunk"}]), ("current", [])])
def test_done_cannot_pass_with_old_version_or_missing_generation_citation(version, citations):
    class Client:
        def call(self, path, body=None):
            if path == "/v1/conversations":
                return {"id": "conversation"}
            if path.endswith("/turns"):
                return {"request_id": "request"}
            return {"status": "DONE", "answer": "private source supported answer", "customer_reply": {"can_copy": False},
                    "evidence": [{"id": "chunk", "documentId": "doc", "versionId": version, "snippet": "private source"}],
                    "citations": citations}
    report = {"queries": [], "published": {"doc": "current"}}
    with pytest.raises(runner.AcceptanceFailure):
        runner.query(Client(), "question", report, expected="doc")
    assert "private source" not in json.dumps(report)


@pytest.mark.parametrize("defect", ["missing_rerank", "failed_request", "wrong_vector_model"])
def test_provider_evidence_gate_rejects_incomplete_runtime_metadata(defect):
    models = {"control": "control-model", "embedding": "embedding-model", "rerank": "rerank-model", "generation": "generation-model"}
    report = {"phases": [], "provider_attempts": [], "published": {"doc": "v1"},
              "indexed_chunks": [{"document_id": "doc", "version_id": "v1", "embedding_model": models["embedding"], "embedding_dim": 1024}]}
    for label, slots in (("empty-index-query", ["control", "embedding"]),
                         ("source-ingestion", ["embedding"] * 3),
                         *((label, list(models)) for label in ("markdown-query", "csv-query", "docx-query"))):
        start = len(report["provider_attempts"])
        for slot in slots:
            report["provider_attempts"].append({"sequence": len(report["provider_attempts"]) + 1,
                "slot": slot, "model": models[slot], "state": "response", "http_status": 200,
                "error_code": None, "request_id_sha256": "hash", "usage_json": '{"input_tokens":1}'})
        report["phases"].append({"phase": label, "attempt_after": start, "attempt_through": len(report["provider_attempts"])})
    runner.validate_provider_evidence(report, models)
    if defect == "missing_rerank":
        report["provider_attempts"] = [a for a in report["provider_attempts"] if a["slot"] != "rerank"]
    elif defect == "failed_request":
        report["provider_attempts"][0]["http_status"] = 400
    else:
        report["indexed_chunks"][0]["embedding_model"] = "offline-model"
    with pytest.raises(runner.AcceptanceFailure):
        runner.validate_provider_evidence(report, models)
