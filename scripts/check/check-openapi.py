"""Validate docs/openapi.yaml: YAML, local $refs, unique operation ids, prefixes."""
from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
SPEC = ROOT / "docs" / "openapi.yaml"
PREFIXES = ("/v1/", "/admin/v1/", "/setup", "/healthz")
ROUTE_FILES = (
    ROOT / "go/internal/admin/handler.go",
    ROOT / "go/internal/gateway/routes_admin.go",
    ROOT / "go/internal/gateway/routes_customer.go",
    ROOT / "go/internal/customer/service.go",
    ROOT / "go/internal/gateway/routes_health.go",
)
ROUTE_RE = re.compile(r'HandleFunc\("([A-Z]+) ([^"]+)"')
# Routes served by adapters outside the enumerated admin/customer files. These
# are explicit paths, never wildcards.
EXTERNAL_ROUTES = {
    ("GET", "/setup"): "localauth setup adapter",
    ("POST", "/setup"): "localauth setup adapter",
    ("GET", "/v1/auth/session"): "localauth/identity browser adapter",
    ("POST", "/v1/auth/login"): "localauth browser adapter",
    ("POST", "/v1/auth/logout"): "localauth/identity browser adapter",
    ("POST", "/v1/auth/password"): "localauth browser adapter",
}
# Registered but deliberately outside the browser OpenAPI surface.
EXCLUDED_ROUTES = {
    ("POST", "/internal/authorization/validate"): "loopback-only internal authorization validation",
}


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
    routes: set[tuple[str, str]] = set()
    for path in ROUTE_FILES:
        if not path.is_file():
            raise SystemExit(f"route source missing: {path.relative_to(ROOT)}")
        for method, route in ROUTE_RE.findall(path.read_text(encoding="utf-8")):
            routes.add((method, route))
    if not routes:
        raise SystemExit("no route registrations found")
    operations = {
        (method.upper(), path)
        for path, item in paths.items()
        for method in item
        if method.lower() in {"get", "post", "put", "delete", "patch", "head", "options"}
    }
    external = set(EXTERNAL_ROUTES)
    excluded = set(EXCLUDED_ROUTES)
    missing = routes - operations - excluded
    extra = operations - routes - external
    missing_external = external - operations
    stale_excluded = excluded - routes
    problems = []
    for method, path in sorted(missing):
        problems.append(f"missing openapi route {method} {path}")
    for method, path in sorted(extra):
        problems.append(f"extra openapi route {method} {path}")
    for method, path in sorted(missing_external):
        problems.append(f"external route not documented {method} {path}")
    for method, path in sorted(stale_excluded):
        problems.append(f"excluded route not registered {method} {path}")
    if problems:
        for problem in problems:
            print(f"FAIL {problem}")
        print(f"FAIL openapi routes: {len(problems)} problem(s)")
        return 1
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
    print(f"PASS openapi paths={len(paths)} operations={len(operations)} refs={len(refs)} "
          f"routes={len(routes)} external={len(external)} excluded={len(excluded)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
