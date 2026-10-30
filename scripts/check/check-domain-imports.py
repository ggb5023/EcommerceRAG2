#!/usr/bin/env python3
"""Keep go/internal/domain free of infrastructure.

The package holds pure decision rules: importing the gateway, the admin package or
a driver would make them context-dependent again, which is exactly what S2 removed.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FORBIDDEN_TARGET = re.compile(r"internal/(gateway|admin|httpapi)|pgxpool|google\.golang\.org/grpc")
IMPORT_LINE = re.compile(r"^\s*(?:[\w.]+\s+)?\"?([\w./-]+)\"?\s*$")
# context is allowed for future cancellation checks, but no function here uses it
# yet; the signature check keeps the "pure" definition honest.
CTX_IN_SIGNATURE = re.compile(r"func [A-Z]\w*\([^)]*context\.Context")


def main() -> int:
    package = ROOT / "go" / "internal" / "domain"
    if not package.is_dir():
        print("SKIP domain imports: no domain package")
        return 0
    # Transitive grpc imports are unavoidable (the generated protobuf package
    # carries RPC stubs), so the boundary is enforced on the domain package's own
    # import statements instead of `go list -deps`.
    imports = sorted(path for path in package.glob("*.go") if not path.name.endswith("_test.go"))
    problems: list[str] = []
    for path in imports:
        for line in path.read_text(encoding="utf-8").splitlines():
            found = FORBIDDEN_TARGET.search(line)
            if found:
                problems.append(f"{path.name}: forbidden reference {found.group(0)}")
    for path in sorted(package.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for match in SIGNATURE_CTX(path):
            problems.append(f"{path.name}: exported function takes context.Context ({match})")
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL domain imports: {len(problems)} violation(s)")
        return 1
    print(f"PASS domain imports: {len(imports)} file(s), no infrastructure imports")
    return 0


def SIGNATURE_CTX(path: Path) -> list[str]:
    calls: list[str] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        found = CTX_IN_SIGNATURE.search(line)
        if found:
            calls.append(line.strip())
    return calls


if __name__ == "__main__":
    sys.exit(main())
