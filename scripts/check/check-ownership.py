#!/usr/bin/env python3
"""Assert that SQL writers in Go and Python match docs/data-ownership.md.

The document is the source of truth: every table row declares the packages or
modules allowed to INSERT/UPDATE/DELETE it. The scan fails when code writes a
declared table from an undeclared writer, and warns when a declared writer has
no write site (the document may be stale) or when a written table is missing
from the document.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DOCUMENT = ROOT / "docs" / "data-ownership.md"

WRITE = re.compile(r"(?i)\b(insert\s+into|update|delete\s+from)\s+([a-z_][a-z0-9_]*)")
# Row-locking clauses ("FOR UPDATE OF x") and prose are not table writes.
CREATE = re.compile(r"(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)")
# Ratchet: 36 tables are written but not declared yet. Registering them lowers
# this number; raising it is a policy change that needs a review, not a fix.
UNDECLARED_BUDGET = 36
# Frozen at the moment the ratchet was introduced, only used to name what moved.
UNDECLARED_BASELINE = frozenset({
    "chunk", "citation", "control_command", "conversation", "conversation_execution",
    "customer_command", "customer_conversation", "customer_event", "customer_execution",
    "customer_identity", "customer_message", "customer_reply", "customer_retrieval_trace",
    "evidence_snapshot", "import_batch", "import_item", "ingest_artifact_object",
    "ingest_command_result", "ingest_event", "ingest_job", "ingest_job_item",
    "ingest_payload", "ingest_publication", "ingest_worker_heartbeat", "local_auth_event",
    "message", "retrieval_trace", "review_suggestion", "shop", "source", "sse_event",
    "tenant_agent_review", "tool_call_trace", "turn", "turn_idempotency",
    "v2_asking_checkpoint",
})

ROW = re.compile(r"^\|\s*`([a-z_][a-z0-9_]*)`\s*\|(.*?)\|\s*$")
NAME = re.compile(r"`([A-Za-z_][A-Za-z0-9_]*)`")


def known_tables(declared: set[str]) -> set[str]:
    """Real table names come from the migrations; the document may add planned ones."""
    names = set(declared)
    for path in (ROOT / "sql" / "migrations").glob("*.sql"):
        names.update(name.lower() for name in CREATE.findall(path.read_text(encoding="utf-8")))
    return names


def declared_tables() -> dict[str, tuple[set[str], bool]]:
    tables: dict[str, tuple[set[str], bool]] = {}
    for line in DOCUMENT.read_text(encoding="utf-8").splitlines():
        match = ROW.match(line.strip())
        if not match:
            continue
        table, rest = match.group(1), match.group(2)
        cell = rest.split("|")[0]
        tables[table] = (set(NAME.findall(cell)), "预留" in cell)
    return tables


def writer_of(path: Path) -> str:
    parts = path.relative_to(ROOT).as_posix().split("/")
    if parts[0] == "go" and parts[1] == "internal":
        return parts[2]
    if parts[0] == "go":
        return "go-" + parts[-2]
    if parts[0] == "python" and parts[1] == "app":
        return parts[2] if len(parts) > 3 else "app"
    return parts[-2]


def sources() -> list[Path]:
    go = [p for p in (ROOT / "go").rglob("*.go")
          if not p.name.endswith("_test.go") and "pb" not in p.parts]
    python = [p for p in (ROOT / "python" / "app").rglob("*.py") if "__pycache__" not in p.parts]
    return sorted(go + python)


def scan(known: set[str]) -> dict[str, list[tuple[str, str]]]:
    found: dict[str, list[tuple[str, str]]] = {}
    for path in sources():
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except OSError:
            continue
        writer = writer_of(path)
        for number, line in enumerate(lines, 1):
            lowered = line.lower()
            for match in WRITE.finditer(line):
                table = match.group(2).lower()
                if table not in known or lowered[max(0, match.start() - 4):match.start()] == "for ":
                    continue
                location = f"{path.relative_to(ROOT).as_posix()}:{number}"
                found.setdefault(table, []).append((writer, location))
    return found


def main() -> int:
    tables = declared_tables()
    if not tables:
        print("FAIL data ownership: docs/data-ownership.md declares no table")
        return 1
    found = scan(known_tables(set(tables)))
    failures: list[str] = []
    warnings: list[str] = []
    undeclared: list[str] = []
    undeclared_names: set[str] = set()
    for table, sites in sorted(found.items()):
        if table not in tables:
            undeclared_names.add(table)
            undeclared.append(f"{table} ({', '.join(sorted({writer for writer, _ in sites}))})")
            continue
        allowed, _ = tables[table]
        for writer, location in sites:
            if writer not in allowed:
                failures.append(
                    f"{location}: {table} written by {writer}, allowed only {', '.join(sorted(allowed)) or '-'}"
                )
    for table, (allowed, reserved) in sorted(tables.items()):
        if reserved:
            continue
        present = {writer for writer, _ in found.get(table, [])}
        for writer in sorted(allowed - present):
            warnings.append(f"{table}: declares {writer} but no write site found (document may be stale)")
    if undeclared:
        warnings.append(f"{len(undeclared)} written tables are not declared yet: {', '.join(undeclared)}")
    if len(undeclared_names) > UNDECLARED_BUDGET:
        appeared = sorted(undeclared_names - UNDECLARED_BASELINE)
        failures.append(
            f"undeclared tables {len(undeclared_names)} exceed the budget {UNDECLARED_BUDGET}"
            + (f"; new: {', '.join(appeared)}" if appeared else f"; declared fewer: {', '.join(sorted(UNDECLARED_BASELINE - undeclared_names)) or '-'}")
        )
    elif len(undeclared_names) < UNDECLARED_BUDGET:
        warnings.append(
            f"undeclared tables dropped to {len(undeclared_names)}; UNDECLARED_BUDGET can be lowered to {len(undeclared_names)}"
        )
    for warning in warnings:
        print(f"WARN {warning}")
    if failures:
        for failure in failures:
            print(f"FAIL {failure}")
        print(f"FAIL data ownership: {len(failures)} problem(s)")
        return 1
    sites = sum(len(value) for value in found.values())
    print(f"PASS data ownership: {len(tables)} tables declared, {sites} write sites checked")
    return 0


if __name__ == "__main__":
    sys.exit(main())
