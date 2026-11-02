#!/usr/bin/env python3
"""LLM semantic judge for the 53 real batch answers (WP24)."""
from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import os
import re
import stat
import tempfile
import time
from collections import Counter, deque
from pathlib import Path
from typing import Any, Mapping

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT / "python") not in __import__("sys").path:
    __import__("sys").path.insert(0, str(ROOT / "python"))

from app.providers import build_provider  # noqa: E402
from app.providers.config import (  # noqa: E402
    ensure_secure_config_file,
    load_provider_config,
)
from app.providers.usage import UsageRecorder  # noqa: E402

DEFAULT_REPORT = ROOT / ".local/wp22/bailian-batch.json"
DEFAULT_CASES = Path("/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/synthetic-m2-v1-revision-1.jsonl")
DEFAULT_CAPTURE_ROOT = Path("/var/lib/ecommerce-rag/real-docs")
DEFAULT_OUTPUT = ROOT / ".local/wp24/answer-quality.json"
DEFAULT_PROVIDER_ENV = Path("/etc/ecommerce-rag/providers.env")
DEFAULT_RUNTIME_ENV = Path("/etc/ecommerce-rag/eval-runtime.env")
VERDICTS = {"correct", "partial", "wrong"}
REQUIRED_CASES = 53

SYSTEM_PROMPT = """你是一个严格的中文问答语义评审员。只判断模型答案是否满足参考要点，不看措辞是否相同。
判定：
- correct：关键事实与参考要点一致，没有矛盾或遗漏；
- partial：部分正确，但遗漏了参考要点中的关键信息，或表述含糊；
- wrong：关键事实错误、与参考要点矛盾，或在有资料依据时错误拒答。
只输出 JSON：{"verdict":"correct|partial|wrong","reason":"一句中文理由，不超过80字"}。"""
USER_TEMPLATE = """问题：
{question}

参考要点：
{answer_points}

模型答案：
{answer}
"""
JUDGE_SCHEMA = {
    "name": "answer_quality_judge",
    "schema": {
        "type": "object",
        "properties": {
            "verdict": {"type": "string", "enum": sorted(VERDICTS)},
            "reason": {"type": "string", "minLength": 1, "maxLength": 240},
        },
        "required": ["verdict", "reason"],
        "additionalProperties": False,
    },
}


def _sha256(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def _load_cases(path: Path) -> dict[str, dict[str, Any]]:
    result: dict[str, dict[str, Any]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            case = json.loads(line)
            if not isinstance(case, dict) or not isinstance(case.get("case_id"), str):
                raise ValueError("case input is invalid")
            result[case["case_id"]] = case
    return result


def _load_runtime_dsn(path: Path) -> None:
    if not path.is_file():
        return
    for line in path.read_text(encoding="utf-8").splitlines():
        if "=" not in line or line.lstrip().startswith("#"):
            continue
        key, value = line.split("=", 1)
        if key.strip() in {"V2_AI_DSN", "V2_USAGE_DSN"}:
            os.environ.setdefault(key.strip(), value.strip().strip("\"'"))


def _find_capture_dir(root: Path, first_capture: str) -> Path:
    candidates = sorted(root.glob("captures-wp22-*"), reverse=True)
    for candidate in candidates:
        if (candidate / first_capture).is_file():
            return candidate
    raise FileNotFoundError(f"answer captures not found for {first_capture}")


def _generated_answer(capture: Mapping[str, Any], case_id: str) -> list[str]:
    structured = capture.get("structured")
    points = structured.get("answer_points") if isinstance(structured, Mapping) else None
    if not isinstance(points, list) or not points or any(not isinstance(point, str) or not point.strip() for point in points):
        raise ValueError(f"{case_id}: generated answer is missing")
    return points


def _prompt_for(question: str, answer_points: list[str], generated: list[str]) -> list[dict[str, str]]:
    answer = "\n".join(f"- {point}" for point in generated)
    expected = "\n".join(f"- {point}" for point in answer_points)
    return [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": USER_TEMPLATE.format(question=question, answer_points=expected, answer=answer)},
    ]


def _parse_judgement(value: Mapping[str, Any], case_id: str) -> tuple[str, str]:
    verdict = value.get("verdict")
    reason = value.get("reason")
    if verdict not in VERDICTS or not isinstance(reason, str) or not reason.strip():
        raise ValueError(f"{case_id}: judge response is invalid")
    return str(verdict), re.sub(r"\s+", " ", reason).strip()


class RequestGate:
    def __init__(self, max_requests: int, max_requests_per_minute: int) -> None:
        if max_requests <= 0 or max_requests_per_minute <= 0:
            raise ValueError("judge limits must be positive")
        self.max_requests = max_requests
        self.max_requests_per_minute = max_requests_per_minute
        self.requests = 0
        self.timestamps: deque[float] = deque()

    async def acquire(self) -> None:
        if self.requests + 1 > self.max_requests:
            raise RuntimeError("judge request limit exceeded")
        now = time.monotonic()
        while self.timestamps and self.timestamps[0] <= now - 60:
            self.timestamps.popleft()
        if len(self.timestamps) >= self.max_requests_per_minute:
            await asyncio.sleep(max(0.0, self.timestamps[0] + 60 - now))
            now = time.monotonic()
        self.timestamps.append(now)
        self.requests += 1


def summarize(results: list[Mapping[str, Any]]) -> dict[str, Any]:
    counts = Counter(row["verdict"] for row in results)
    wrong = counts.get("wrong", 0)
    return {
        "verdict_counts": {name: counts.get(name, 0) for name in ("correct", "partial", "wrong")},
        "wrong_answer_rate": round(wrong / REQUIRED_CASES, 6),
        "partial_rate": round(counts.get("partial", 0) / REQUIRED_CASES, 6),
    }


def _write_once(path: Path, value: Mapping[str, Any]) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.parent.stat().st_mode & 0o077:
        raise ValueError("output directory must be 0700")
    if path.exists():
        raise FileExistsError(f"output already exists: {path}")
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


async def run(args: argparse.Namespace) -> dict[str, Any]:
    _load_runtime_dsn(args.runtime_env)
    if UsageRecorder.from_environment(role="ai") is None:
        raise RuntimeError("usage DSN is required for the judge")
    ensure_secure_config_file(args.provider_env)
    config = load_provider_config(args.provider_env)
    if config.profile != "aliyun-bailian":
        raise RuntimeError("judge requires the real aliyun-bailian profile")

    report = _load_json(args.report)
    rows = report.get("case_results")
    if report.get("status") != "PASS" or not isinstance(rows, list) or len(rows) != REQUIRED_CASES:
        raise ValueError("batch report is not a complete 53-case PASS")
    cases = _load_cases(args.cases)
    report_ids = {row.get("case_id") for row in rows}
    if len(report_ids) != REQUIRED_CASES or not report_ids.issubset(cases):
        raise ValueError("case set does not match the frozen 53 cases")
    first_capture = rows[0].get("capture_id")
    capture_dir = args.captures or _find_capture_dir(args.capture_root, str(first_capture))

    provider = build_provider(config, record_usage=True)
    if getattr(provider, "usage", None) is None:
        raise RuntimeError("provider usage recorder is unavailable")
    gate = RequestGate(args.max_requests, args.max_requests_per_minute)
    results: list[dict[str, Any]] = []
    for row in rows:
        case_id = row["case_id"]
        case = cases[case_id]
        capture_path = capture_dir / row["capture_id"]
        capture = _load_json(capture_path)
        if _sha256(capture_path.read_bytes()) != row.get("capture_sha256"):
            raise ValueError(f"{case_id}: capture hash changed")
        expected = case.get("expected_answer_points")
        if not isinstance(expected, list) or not expected:
            raise ValueError(f"{case_id}: expected answer points are missing")
        generated = _generated_answer(capture, case_id)
        if capture.get("query_sha256") != _sha256(str(case.get("query", "")).encode()):
            raise ValueError(f"{case_id}: query binding changed")
        await gate.acquire()
        result = await provider.generate(
            _prompt_for(str(case.get("query", "")), expected, generated),
            response_schema=JUDGE_SCHEMA,
            temperature=0,
            tenant_id=None,
        )
        verdict, reason = _parse_judgement(result.structured or {}, case_id)
        results.append({"case_id": case_id, "verdict": verdict, "reason": reason,
                        "answer_point_count": len(expected)})
    summary = summarize(results)
    return {
        "judge_version": "answer-quality-llm-judge-v1",
        "review_method": "delegated_agent_semantic_review_v1",
        "model": config.generation_model,
        "temperature": 0,
        "prompt_sha256": _sha256((SYSTEM_PROMPT + USER_TEMPLATE).encode()),
        "case_count": len(results),
        **summary,
        "requests_used": gate.requests,
        "limits": {"max_requests": args.max_requests,
                   "max_requests_per_minute": args.max_requests_per_minute},
        "tenant_id": None,
        "answer_quality_status": "MEASURED",
        "real_service_acceptance": False,
        "results": results,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--cases", type=Path, default=DEFAULT_CASES)
    parser.add_argument("--captures", type=Path)
    parser.add_argument("--capture-root", type=Path, default=DEFAULT_CAPTURE_ROOT)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--provider-env", type=Path, default=DEFAULT_PROVIDER_ENV)
    parser.add_argument("--runtime-env", type=Path, default=DEFAULT_RUNTIME_ENV)
    parser.add_argument("--max-requests", type=int, default=300)
    parser.add_argument("--max-requests-per-minute", type=int, default=30)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        report = asyncio.run(run(args))
        _write_once(args.output, report)
    except (OSError, ValueError, RuntimeError, json.JSONDecodeError) as error:
        print(json.dumps({"status": "FAIL", "issues": [type(error).__name__]}, ensure_ascii=False))
        return 2
    print(json.dumps({key: report[key] for key in (
        "case_count", "verdict_counts", "wrong_answer_rate", "partial_rate", "requests_used"
    )}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
