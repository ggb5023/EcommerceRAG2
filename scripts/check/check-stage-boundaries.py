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
            for label, text in (("signature", signature), ("receiver", receiver)):
                for found in FORBIDDEN_TYPE.finditer(text):
                    problems.append(f"{relative}:{line} {name}: {label} carries {found.group(0)}")
            field = FORBIDDEN_FIELD.search(body)
            if field:
                problems.append(f"{relative}:{line} {name}: touches gateway fields via g.")
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL stage boundaries: {len(problems)} violation(s)")
        return 1
    print(f"PASS stage boundaries: {checked} stage function(s) in {len(files)} file(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
