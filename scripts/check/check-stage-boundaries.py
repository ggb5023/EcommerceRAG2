#!/usr/bin/env python3
"""Guard the stage-function boundary inside the gateway package (decision 0025 R3).

Stage functions live in ``go/internal/gateway/turn_*.go`` once the turn engine is
split out. They must not receive framework objects and must not reach into the
gateway struct:

* no ``*Handler`` / ``*Server`` / ``*Gateway`` / ``*gateway`` in the signature;
* no ``g.<field>`` access inside the body.

Before those files exist the check prints SKIP and succeeds, so it can be wired
into the gate ahead of the split.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GATEWAY = ROOT / "go" / "internal" / "gateway"

FUNC = re.compile(r"^func\s+(?:\(([^)]*)\)\s*)?([A-Za-z0-9_]+)\s*\(", re.MULTILINE)
FORBIDDEN_TYPE = re.compile(r"\*(?:Handler|Server|Gateway|gateway)\b")
# runTurn must stay an orchestrator: SQL, SSE writes and answer qualifiers belong
# to the stage files (decision 0025 R2 / P-A acceptance).
# Only decreasing: lower this after every split. Raising it needs a review.
RUN_TURN_BUDGET = 262
ORCHESTRATOR = "go/internal/gateway/main.go"
ORCHESTRATOR_FUNC = "runTurn"
FORBIDDEN_PARAM = re.compile(r"\*http\.Request\b")
ORCHESTRATOR_FORBIDDEN = {
    "SQL": re.compile(r"\b(?:SELECT|INSERT INTO|UPDATE|DELETE FROM)\b"),
    "SSE write": re.compile(r"fmt\.Fprintf\(w,"),
    "answer qualifier": re.compile(r"withSnapshotQualifier"),
}
FORBIDDEN_FIELD = re.compile(r"(?<![\w.])g\.[A-Za-z_]")


def function_blocks(source: str):
    """Yield (name, receiver, signature, body, line) for every top-level function."""
    for match in FUNC.finditer(source):
        start = match.end() - 1  # at the opening parenthesis of the parameter list
        depth, index = 0, start
        while index < len(source):
            char = source[index]
            if char == "(":
                depth += 1
            elif char == ")":
                depth -= 1
                if depth == 0:
                    break
            index += 1
        signature = source[start:index + 1]
        body_start = source.find("{", index)
        if body_start == -1:
            continue
        depth, body_end = 0, body_start
        while body_end < len(source):
            char = source[body_end]
            if char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    break
            body_end += 1
        line = source.count("\n", 0, match.start()) + 1
        yield match.group(2), (match.group(1) or ""), signature, source[body_start:body_end + 1], line


def orchestrator_lines() -> int:
    path = ROOT / ORCHESTRATOR
    if not path.is_file():
        return 0
    source = path.read_text(encoding="utf-8")
    match = re.search(r"^func \(g \*gateway\) " + ORCHESTRATOR_FUNC + r"\(", source, re.MULTILINE)
    if not match:
        return 0
    end = source.find("\n}\n", match.start())
    return source[match.start():end if end != -1 else len(source)].count("\n") + 1


def orchestrator_problems() -> list[str]:
    path = ROOT / ORCHESTRATOR
    if not path.is_file():
        return []
    source = path.read_text(encoding="utf-8")
    pattern = re.compile(r"^func \(g \*gateway\) " + ORCHESTRATOR_FUNC + r"\(", re.MULTILINE)
    match = pattern.search(source)
    if not match:
        return []
    end = source.find("\n}\n", match.start())
    body = source[match.start():end if end != -1 else len(source)]
    problems = []
    for label, forbidden in ORCHESTRATOR_FORBIDDEN.items():
        found = forbidden.search(body)
        if found:
            problems.append(f"{ORCHESTRATOR}: {ORCHESTRATOR_FUNC} still contains {label} ({found.group(0)!r})")
    return problems


def main() -> int:
    files = sorted(GATEWAY.glob("turn_*.go"))
    files = [path for path in files if not path.name.endswith("_test.go")]
    if not files:
        print("SKIP stage boundaries: no go/internal/gateway/turn_*.go yet")
        return 0
    problems: list[str] = []
    checked = 0
    for path in files:
        relative = path.relative_to(ROOT).as_posix()
        for name, receiver, signature, body, line in function_blocks(path.read_text(encoding="utf-8")):
            if name.startswith("Test"):
                continue
            checked += 1
            if FORBIDDEN_PARAM.search(signature):
                problems.append(f"{relative}:{line} {name}: signature carries *http.Request (pass request data instead)")
            for label, text in (("signature", signature), ("receiver", receiver)):
                for found in FORBIDDEN_TYPE.finditer(text):
                    problems.append(f"{relative}:{line} {name}: {label} carries {found.group(0)}")
            field = FORBIDDEN_FIELD.search(body)
            if field:
                problems.append(f"{relative}:{line} {name}: touches gateway fields via g.")
    problems.extend(orchestrator_problems())
    run_turn = orchestrator_lines()
    if run_turn > RUN_TURN_BUDGET:
        problems.append(f"{ORCHESTRATOR}: {ORCHESTRATOR_FUNC} is {run_turn} lines, budget is {RUN_TURN_BUDGET}")
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL stage boundaries: {len(problems)} violation(s)")
        return 1
    print(f"PASS stage boundaries: {checked} stage function(s) in {len(files)} file(s); "
          f"{ORCHESTRATOR_FUNC} {orchestrator_lines()}/{RUN_TURN_BUDGET} lines")
    return 0


if __name__ == "__main__":
    sys.exit(main())
