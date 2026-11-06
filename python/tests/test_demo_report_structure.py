"""Lock the demo report's five-section shape and fail-closed semantics."""
import importlib.util
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("demo_report", ROOT / "scripts/dev/demo-report.py")
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)

SECTIONS = ("input", "operations", "handoff", "quality", "tokens")


def test_quality_missing_report_stays_not_measured(tmp_path):
    section = runner.read_quality(tmp_path / "missing.json")
    assert section["status"] == "NOT_MEASURED"
    assert section["wrong_answer_rate"] is None
    assert section["case_count"] is None
    assert section["reviewer_kind"] == "agent"
    assert section["human_review_completed"] is False
    assert "reason" in section


def test_zero_question_denominator_is_null_not_zero():
    tokens = runner.add_token_rates(
        {"status": "MEASURED", "input_tokens": 0, "output_tokens": 0, "calls": 0, "failed_calls": 0},
        0,
    )
    assert tokens["question_count"] == 0
    for field in runner.TOKEN_FIELDS:
        assert tokens[f"{field}_per_1000"] is None


def test_report_cli_emits_five_sections_and_fail_closed_values(tmp_path, monkeypatch, capsys):
    payloads = {
        "/admin/v1/merchant/reviews": {"items": []},
        "/admin/v1/merchant/outcomes": {
            "turns_total": 0, "scored_total": 0, "solved": 0, "clarified": 0,
            "abandoned": 0, "failed": 0, "cancelled": 0, "pending": 0, "unknown": 0,
            "auto_rate": None, "assisted_rate": None, "truncated": False, "metric_gap": False,
        },
        "/admin/v1/merchant/report/handoff": {
            "range": "30d", "window_start": "2026-11-01T00:00:00+00:00",
            "window_end": "2026-11-06T00:00:00+00:00", "handoff_conversations": 0,
            "customer_conversations": 0, "handoff_rate": None,
        },
        "/admin/v1/merchant/usage": {
            "input_tokens": 0, "output_tokens": 0, "calls": 0, "failed_calls": 0,
            "by_slot": [], "by_model": [],
        },
    }

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            body = payloads.get(urlparse(self.path).path)
            if body is None:
                self.send_error(404)
                return
            encoded = json.dumps(body).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *_args):
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        outdir = tmp_path / "report"
        monkeypatch.setattr(sys, "argv", [
            "demo-report.py", "--base-url", f"http://127.0.0.1:{server.server_port}",
            "--quality-report", str(tmp_path / "missing-quality.json"),
            "--output-dir", str(outdir),
        ])
        assert runner.main() == 2
        report = json.loads((outdir / "demo-report.json").read_text(encoding="utf-8"))
    finally:
        server.shutdown()
        thread.join(timeout=2)

    assert set(SECTIONS) <= set(report)
    assert report["status"] == "NOT_MEASURED"
    assert report["quality"]["status"] == "NOT_MEASURED"
    assert report["quality"]["wrong_answer_rate"] is None
    assert report["handoff"]["handoff_rate"] is None
    assert report["input"]["status"] == "NOT_MEASURED"
    serialized = json.dumps(report, ensure_ascii=False)
    for symbol in ("¥", "$", "€", "£", "currency", "price_cents", "金额", "汇率"):
        assert symbol not in serialized, symbol
    output = capsys.readouterr().out
    assert "REPORT status=NOT_MEASURED" in output
