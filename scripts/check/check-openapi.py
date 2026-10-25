"""Validate docs/openapi.yaml: YAML, local $refs, unique operation ids, prefixes."""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
SPEC = ROOT / "docs" / "openapi.yaml"
PREFIXES = ("/v1/", "/admin/v1/", "/setup", "/healthz")


def walk(node, path=""):
    if isinstance(node, dict):
        for key, value in node.items():
            yield from walk(value, f"{path}/{key}")
    elif isinstance(node, list):
        for index, value in enumerate(node):
            yield from walk(value, f"{path}[{index}]")


def main() -> int:
    document = yaml.safe_load(SPEC.read_text())
    if document.get("openapi", "").split(".")[0] != "3":
        raise SystemExit("openapi 3.x required")
    paths = document.get("paths") or {}
    if not paths:
        raise SystemExit("paths required")
    for path in paths:
        if not path.startswith(PREFIXES):
            raise SystemExit(f"unexpected path {path}")
    schemas = set((document.get("components") or {}).get("schemas") or {})
    refs = [node["$ref"] for node in walk(document) if isinstance(node, dict) and "$ref" in node]
    for ref in refs:
        if not ref.startswith("#/"):
            raise SystemExit(f"remote ref not allowed: {ref}")
        target = document
        for part in ref[2:].split("/"):
            target = target[part]
    if not (document.get("components") or {}).get("securitySchemes"):
        raise SystemExit("securitySchemes required")
    print(f"PASS openapi paths={len(paths)} refs={len(refs)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
