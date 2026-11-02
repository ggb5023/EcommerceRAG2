import asyncio
import importlib.util
from pathlib import Path

import pytest

SPEC = importlib.util.spec_from_file_location(
    "judge_answer_quality", Path(__file__).with_name("judge_answer_quality.py")
)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(MODULE)


def test_judgement_parser_accepts_only_three_verdicts_and_requires_reason():
    assert MODULE._parse_judgement({"verdict": "correct", "reason": " 关键事实一致 "}, "c") == ("correct", "关键事实一致")
    for value in (
        {"verdict": "unknown", "reason": "x"},
        {"verdict": "correct", "reason": ""},
        {"verdict": "wrong"},
    ):
        with pytest.raises(ValueError):
            MODULE._parse_judgement(value, "c")


def test_summary_keeps_partial_out_of_correct_and_wrong():
    summary = MODULE.summarize([
        {"verdict": "correct"}, {"verdict": "partial"},
        {"verdict": "wrong"}, {"verdict": "wrong"},
    ] + [{"verdict": "correct"} for _ in range(49)])
    assert summary["verdict_counts"] == {"correct": 50, "partial": 1, "wrong": 2}
    assert summary["wrong_answer_rate"] == round(2 / 53, 6)
    assert summary["partial_rate"] == round(1 / 53, 6)


def test_request_gate_enforces_total_limit():
    gate = MODULE.RequestGate(1, 30)
    asyncio.run(gate.acquire())
    with pytest.raises(RuntimeError, match="limit"):
        asyncio.run(gate.acquire())
