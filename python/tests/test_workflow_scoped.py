import re
from pathlib import Path

from app.graph.workflow import scoped


def test_all_provider_ainvoke_calls_are_scoped():
    source = (Path(__file__).resolve().parents[1] / "app/graph/workflow.py").read_text()
    calls = len(re.findall(r"chains\.(control|embedding|rerank|generation)\.ainvoke\(", source))
    wrapped = len(re.findall(r"chains\.(control|embedding|rerank|generation)\.ainvoke\(scoped\(state,", source))
    assert calls > 0 and calls == wrapped


def test_scoped_uses_numeric_tenant_for_provider_usage():
    state = {"scope": {"tenant_id": "v2-demo-tenant", "tenant_db_id": "42", "shop_id": "shop-demo"}}
    assert scoped(state, {})["tenant_id"] == "42"
    fallback = {"scope": {"tenant_id": "v2-demo-tenant", "shop_id": "shop-demo"}}
    assert scoped(fallback, {})["tenant_id"] == "v2-demo-tenant"
