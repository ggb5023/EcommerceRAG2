#!/usr/bin/env python3
"""Build a reproducible five-section report for the synthetic demo store.

The report reads only existing admin endpoints and local frozen evaluation
artifacts. It never writes business tables, customer text, prompts, credentials,
DSNs, or monetary values. Output defaults to .local/wp36 with 0700/0600 modes.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from datetime import UTC, datetime, timedelta
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parents[2]
PACKAGE = REPO / "data" / "synthetic" / "demo-store-v1"
DEFAULT_QUALITY_REPORT = REPO / ".local" / "wp25" / "answer-quality.json"
TOKEN_FIELDS = ("input_tokens", "output_tokens", "calls", "failed_calls")


class ReportDataError(RuntimeError):
    """A read failed; the report keeps the section as NOT_MEASURED."""


def sha256_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def package_digest(package: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(value for value in package.rglob("*") if value.is_file()):
        digest.update(path.relative_to(package).as_posix().encode() + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def git_commit() -> str | None:
    try:
        result = subprocess.run(
            ["git", "-C", str(REPO), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
            timeout=10,
        )
        return result.stdout.strip() or None
    except (OSError, subprocess.SubprocessError):
        return None


def api_get(base_url: str, path: str, params: dict[str, str] | None = None) -> dict:
    url = base_url.rstrip("/") + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read()
    except urllib.error.HTTPError as exc:
        raise ReportDataError(f"GET {path} -> HTTP {exc.code}") from exc
    except urllib.error.URLError as exc:
        raise ReportDataError(f"GET {path} -> {exc.reason}") from exc
    try:
        body = json.loads(raw or b"{}")
    except json.JSONDecodeError as exc:
        raise ReportDataError(f"GET {path} returned non-JSON") from exc
    if not isinstance(body, dict):
        raise ReportDataError(f"GET {path} returned a non-object")
    return body


def read_quality(path: Path) -> dict:
    section = {
        "status": "NOT_MEASURED",
        "wrong_answer_rate": None,
        "case_count": None,
        "correct_count": None,
        "partial_count": None,
        "wrong_count": None,
        "judge_version": None,
        "reviewer_kind": "agent",
        "human_review_completed": False,
        "source_sha256": None,
    }
    try:
        raw = path.read_bytes()
    except OSError:
        section["reason"] = "frozen judge report is missing"
        return section
    section["source_sha256"] = sha256_bytes(raw)
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        section["reason"] = "frozen judge report is not valid JSON"
        return section
    if not isinstance(data, dict) or data.get("answer_quality_status") != "MEASURED":
        section["reason"] = "frozen judge report is not measured"
        return section
    rate = data.get("wrong_answer_rate")
    case_count = data.get("case_count")
    if not isinstance(rate, (int, float)) or isinstance(rate, bool) or not isinstance(case_count, int) or case_count <= 0:
        section["reason"] = "frozen judge report lacks a usable rate"
        return section
    counts = data.get("verdict_counts") if isinstance(data.get("verdict_counts"), dict) else {}
    if not counts and isinstance(data.get("results"), list):
        counts = dict(Counter(row.get("verdict") for row in data["results"] if isinstance(row, dict)))
    section.update(
        {
            "status": "MEASURED",
            "wrong_answer_rate": float(rate),
            "case_count": case_count,
            "correct_count": counts.get("correct"),
            "partial_count": counts.get("partial"),
            "wrong_count": counts.get("wrong"),
            "judge_version": data.get("judge_version"),
        }
    )
    return section


def read_package(base_url: str) -> tuple[dict, list[dict], int]:
    try:
        manifest_bytes = (PACKAGE / "manifest.yaml").read_bytes()
        manifest = yaml.safe_load(manifest_bytes)
    except (OSError, yaml.YAMLError) as exc:
        raise ReportDataError(f"demo package unavailable: {exc.__class__.__name__}") from exc
    if not isinstance(manifest, dict) or not isinstance(manifest.get("documents"), list):
        raise ReportDataError("demo manifest lacks documents")
    docs = [doc for doc in manifest["documents"] if isinstance(doc, dict) and doc.get("document_id")]
    business_date = str(manifest.get("business_date") or "").strip()
    questions_path = PACKAGE / "questions.jsonl"
    try:
        questions_raw = questions_path.read_bytes()
        question_count = sum(1 for line in questions_raw.splitlines() if line.strip())
    except OSError as exc:
        raise ReportDataError("demo question set is unavailable") from exc
    version_ids, version_jobs, missing = {}, {}, []
    try:
        items = api_get(base_url, "/admin/v1/merchant/reviews").get("items") or []
    except ReportDataError:
        items = []
    for doc in docs:
        item = next(
            (
                value
                for value in items
                if value.get("document_id") == doc.get("document_id")
                and value.get("source_hash") == doc.get("sha256")
                and value.get("status") == "active"
                and value.get("review_status") == "approved"
            ),
            None,
        )
        if item is None:
            missing.append(doc["document_id"])
        else:
            version_ids[doc["document_id"]] = str(item.get("version_id"))
            version_jobs[doc["document_id"]] = item.get("job_id")
    inputs = {
        "status": "MEASURED" if business_date and not missing else "NOT_MEASURED",
        "git_commit": git_commit(),
        "package_sha256": package_digest(PACKAGE),
        "manifest_sha256": sha256_bytes(manifest_bytes),
        "questions_sha256": sha256_bytes(questions_raw),
        "question_count": question_count,
        "business_date": business_date or None,
        "cohorts": ["workspace", "customer"],
        "version_ids": version_ids or None,
        "version_job_ids": version_jobs or None,
        "missing_version_documents": missing or None,
    }
    return inputs, docs, question_count


def measured(section: dict) -> bool:
    return section.get("status") == "MEASURED"


def get_sections(base_url: str, range_value: str) -> tuple[dict, dict, dict]:
    try:
        operations = {"status": "MEASURED", **api_get(base_url, "/admin/v1/merchant/outcomes", {"range": range_value})}
    except ReportDataError as exc:
        operations = {"status": "NOT_MEASURED", "reason": str(exc)}
    try:
        handoff = {"status": "MEASURED", **api_get(base_url, "/admin/v1/merchant/report/handoff", {"range": range_value})}
    except ReportDataError as exc:
        handoff = {"status": "NOT_MEASURED", "reason": str(exc)}
    try:
        tokens = {"status": "MEASURED", **api_get(base_url, "/admin/v1/merchant/usage", {"range": range_value})}
    except ReportDataError as exc:
        tokens = {"status": "NOT_MEASURED", "reason": str(exc)}
    return operations, handoff, tokens


def add_token_rates(tokens: dict, question_count: int) -> dict:
    tokens["question_count"] = question_count

    def with_rates(row: dict) -> dict:
        value = dict(row)
        for field in TOKEN_FIELDS:
            amount = value.get(field)
            value[f"{field}_per_1000"] = (amount / question_count * 1000) if question_count else None
        return value

    for key in ("by_slot", "by_model"):
        rows = tokens.get(key)
        tokens[key] = [with_rates(row) for row in rows] if isinstance(rows, list) else rows
    for field in TOKEN_FIELDS:
        amount = tokens.get(field)
        tokens[f"{field}_per_1000"] = (amount / question_count * 1000) if question_count else None
    return tokens


def window_for(range_value: str, handoff: dict, generated_at: str) -> tuple[str, str]:
    if measured(handoff) and handoff.get("window_start") and handoff.get("window_end"):
        return str(handoff["window_start"]), str(handoff["window_end"])
    end = datetime.fromisoformat(generated_at)
    days = 7 if range_value == "7d" else 30
    return (end - timedelta(days=days)).isoformat(), end.isoformat()


def reproducibility(previous_path: Path | None, report: dict) -> dict | None:
    if previous_path is None:
        return None
    try:
        raw = previous_path.read_bytes()
        previous = json.loads(raw)
    except (OSError, json.JSONDecodeError):
        return {"status": "NOT_MEASURED", "reason": "previous report is unavailable"}
    current_ops = report["operations"]
    previous_ops = previous.get("operations") or {}
    current_handoff = report["handoff"]
    previous_handoff = previous.get("handoff") or {}
    current_quality = report["quality"]
    previous_quality = previous.get("quality") or {}
    op_fields = ("turns_total", "scored_total", "solved", "clarified", "abandoned", "failed",
                 "cancelled", "pending", "unknown", "auto_rate", "assisted_rate")
    operations_equal = all(current_ops.get(field) == previous_ops.get(field) for field in op_fields)
    handoff_equal = all(
        current_handoff.get(field) == previous_handoff.get(field)
        for field in ("handoff_conversations", "customer_conversations", "handoff_rate")
    )
    quality_equal = (
        current_quality.get("wrong_answer_rate") == previous_quality.get("wrong_answer_rate")
        and current_quality.get("case_count") == previous_quality.get("case_count")
    )
    token_delta = {}
    for field in TOKEN_FIELDS:
        before = (previous.get("tokens") or {}).get(field)
        after = report["tokens"].get(field)
        if isinstance(before, (int, float)) and isinstance(after, (int, float)):
            token_delta[field] = after - before
    changed = any(value != 0 for value in token_delta.values())
    return {
        "status": "MEASURED" if operations_equal and handoff_equal and quality_equal else "NOT_MEASURED",
        "previous_report_sha256": sha256_bytes(raw),
        "counts_equal": operations_equal,
        "handoff_counts_equal": handoff_equal,
        "quality_equal": quality_equal,
        "token_delta": token_delta,
        "token_difference_explained": (
            "token counts changed because usage_log includes provider calls between reports"
            if changed else "no token difference observed"
        ),
    }


def render_value(value) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    return str(value)


def render_markdown(report: dict) -> str:
    inp, ops, quality, handoff, tokens = (
        report["input"], report["operations"], report["quality"], report["handoff"], report["tokens"]
    )
    lines = [
        "# 演示店铺评测报告",
        "",
        f"- 状态：{report['status']}",
        "- real_service_acceptance=false",
        f"- 生成时间：{report['generated_at']}",
        "",
        "## 1. 输入",
        "",
        f"- git commit：{inp.get('git_commit')}",
        f"- 数据包 sha256：{inp.get('package_sha256')}",
        f"- manifest sha256：{inp.get('manifest_sha256')}",
        f"- 问题集 sha256：{inp.get('questions_sha256')}",
        f"- business_date：{inp.get('business_date')}",
        f"- 人群：{', '.join(inp.get('cohorts') or [])}",
        f"- 窗口：{inp.get('window_start')} 至 {inp.get('window_end')}",
        f"- 冻结评测报告 sha256：{quality.get('source_sha256')}",
        f"- active 版本：{json.dumps(inp.get('version_ids'), ensure_ascii=False)}",
        "",
        "## 2. 运营结果",
        "",
        f"- 状态：{ops.get('status')}",
        f"- outcomes：{json.dumps({k: v for k, v in ops.items() if k != 'status'}, ensure_ascii=False, sort_keys=True)}",
        "",
        "## 3. 质量",
        "",
        f"- 状态：{quality.get('status')}",
        f"- wrong_answer_rate：{render_value(quality.get('wrong_answer_rate'))}",
        f"- case_count：{render_value(quality.get('case_count'))}",
        f"- reviewer_kind：{quality.get('reviewer_kind')}",
        f"- human_review_completed：{render_value(quality.get('human_review_completed'))}",
        f"- 来源 sha256：{render_value(quality.get('source_sha256'))}",
        "",
        "## 4. 转人工",
        "",
        f"- 状态：{handoff.get('status')}",
        f"- 分子（WAITING_HUMAN 转移 conversation）：{render_value(handoff.get('handoff_conversations'))}",
        f"- 分母（至少一条 customer 消息 conversation）：{render_value(handoff.get('customer_conversations'))}",
        f"- handoff_rate：{render_value(handoff.get('handoff_rate'))}",
        "",
        "## 5. token",
        "",
        f"- 状态：{tokens.get('status')}",
        f"- 问题数：{render_value(tokens.get('question_count'))}",
        f"- 输入 tokens：{render_value(tokens.get('input_tokens'))}（每千次 {render_value(tokens.get('input_tokens_per_1000'))}）",
        f"- 输出 tokens：{render_value(tokens.get('output_tokens'))}（每千次 {render_value(tokens.get('output_tokens_per_1000'))}）",
        f"- 调用次数：{render_value(tokens.get('calls'))}（每千次 {render_value(tokens.get('calls_per_1000'))}）",
        f"- 失败次数：{render_value(tokens.get('failed_calls'))}（每千次 {render_value(tokens.get('failed_calls_per_1000'))}）",
        f"- by_slot：{json.dumps(tokens.get('by_slot'), ensure_ascii=False, sort_keys=True)}",
        f"- by_model：{json.dumps(tokens.get('by_model'), ensure_ascii=False, sort_keys=True)}",
        "",
    ]
    return "\n".join(lines)


def write_outputs(report: dict, outdir: Path) -> tuple[Path, Path]:
    outdir.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(outdir, 0o700)
    json_path = outdir / "demo-report.json"
    markdown_path = outdir / "demo-report.md"
    json_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    markdown_path.write_text(render_markdown(report), encoding="utf-8")
    os.chmod(json_path, 0o600)
    os.chmod(markdown_path, 0o600)
    return json_path, markdown_path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("ECR_DEMO_BASE_URL", "http://127.0.0.1:26081"))
    parser.add_argument("--range", choices=("7d", "30d"), default="30d")
    parser.add_argument("--quality-report", type=Path, default=DEFAULT_QUALITY_REPORT)
    parser.add_argument("--output-dir", type=Path, default=REPO / ".local" / "wp36")
    parser.add_argument("--previous", type=Path, default=None)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    generated_at = datetime.now(UTC).isoformat()
    try:
        inputs, _, question_count = read_package(args.base_url)
    except ReportDataError as exc:
        inputs, question_count = {
            "status": "NOT_MEASURED",
            "reason": str(exc),
            "git_commit": git_commit(),
            "cohorts": ["workspace", "customer"],
        }, 0
    quality = read_quality(args.quality_report)
    operations, handoff, tokens = get_sections(args.base_url, args.range)
    tokens = add_token_rates(tokens, question_count) if measured(tokens) else tokens
    window_start, window_end = window_for(args.range, handoff, generated_at)
    inputs.update({"range": args.range, "window_start": window_start, "window_end": window_end,
                   "quality_report_sha256": quality.get("source_sha256")})
    report = {
        "report_version": "demo-report-v1",
        "status": "MEASURED" if all(measured(section) for section in (inputs, operations, quality, handoff, tokens)) else "NOT_MEASURED",
        "real_service_acceptance": False,
        "generated_at": generated_at,
        "input": inputs,
        "operations": operations,
        "quality": quality,
        "handoff": handoff,
        "tokens": tokens,
    }
    repro = reproducibility(args.previous, report)
    if repro is not None:
        report["input"]["reproducibility"] = repro
    report["not_measured"] = [
        name for name, section in (
            ("input", inputs), ("operations", operations), ("quality", quality),
            ("handoff", handoff), ("tokens", tokens),
        ) if not measured(section)
    ]
    json_path, markdown_path = write_outputs(report, args.output_dir)
    print(f"REPORT status={report['status']} json={json_path} markdown={markdown_path}")
    print(f"HANDOFF numerator={render_value(handoff.get('handoff_conversations'))} denominator={render_value(handoff.get('customer_conversations'))} rate={render_value(handoff.get('handoff_rate'))}")
    print(f"QUALITY wrong_answer_rate={quality.get('wrong_answer_rate')} source_sha256={quality.get('source_sha256')}")
    if repro is not None:
        print(f"REPRODUCIBILITY counts_equal={repro['counts_equal']} handoff_counts_equal={repro['handoff_counts_equal']} token_delta={repro['token_delta']}")
    return 0 if report["status"] == "MEASURED" else 2


if __name__ == "__main__":
    sys.exit(main())
