#!/usr/bin/env python3
"""Keep the Go CSV template headers and the Python catalog schema in lockstep.

The template columns are a cross-process contract: Go renders the downloadable
CSV, Python validates typed rows. This guard is static and does not import either
side. It reports the exact columns that differ.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GO_TEMPLATES = ROOT / "go/internal/admin/import_templates.go"
PY_CATALOG = ROOT / "python/app/ingest/catalog.py"

EXPECTED_GO = {
    "product_table_v1": re.compile(
        r'"(?P<name>product_table_v1)"\s*:\s*\{.*?Headers:\s*\[\]string\{(?P<body>.*?)\}',
        re.S,
    ),
    "size_chart_v1": re.compile(
        r'"(?P<name>size_chart_v1)"\s*:\s*\{.*?Headers:\s*\[\]string\{(?P<body>.*?)\}',
        re.S,
    ),
}


def quoted(body: str) -> list[str]:
    return re.findall(r'"([^"]+)"', body)


def go_headers() -> dict[str, list[str]]:
    text = GO_TEMPLATES.read_text(encoding="utf-8")
    result: dict[str, list[str]] = {}
    for name, pattern in EXPECTED_GO.items():
        match = pattern.search(text)
        if match is None:
            raise ValueError(f"cannot find {name} Headers")
        result[name] = quoted(match.group("body"))
    return result


def py_frozenset(text: str, name: str) -> set[str]:
    match = re.search(rf"{name}\s*=\s*frozenset\s*\(\s*\{{(.*?)\}}\s*\)", text, re.S)
    if match is None:
        raise ValueError(f"cannot find {name}")
    return set(re.findall(r'"([^"]+)"', match.group(1)))


def python_columns() -> dict[str, set[str]]:
    text = PY_CATALOG.read_text(encoding="utf-8")
    product = (
        py_frozenset(text, "PRODUCT_BASE_FIELDS")
        | py_frozenset(text, "PRODUCT_NUMERIC_ATTRIBUTES")
        | py_frozenset(text, "PRODUCT_TEXT_ATTRIBUTES")
    )
    size_chart = py_frozenset(text, "SIZE_CHART_BASE_FIELDS") | py_frozenset(text, "SIZE_CHART_MEASUREMENTS")
    return {"product_table_v1": product, "size_chart_v1": size_chart}


def main() -> int:
    try:
        go = go_headers()
        python = python_columns()
    except (OSError, ValueError) as error:
        print(f"FAIL import-schema: {error}")
        return 1

    problems = []
    for name in ("product_table_v1", "size_chart_v1"):
        go_set, py_set = set(go[name]), python[name]
        if go_set != py_set:
            missing_in_python = sorted(go_set - py_set)
            missing_in_go = sorted(py_set - go_set)
            problems.append(
                f"{name}: Go-only={missing_in_python} Python-only={missing_in_go}"
            )
    if problems:
        print("FAIL import-schema: " + "; ".join(problems))
        return 1
    print("PASS import-schema parity")
    return 0


if __name__ == "__main__":
    sys.exit(main())
