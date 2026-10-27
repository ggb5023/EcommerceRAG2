import pytest


@pytest.fixture(autouse=True)
def _default_provider_scope(monkeypatch):
    """Existing unit fixtures predate mandatory tenant payloads; scoped() still
    rejects missing scope in production. Chain-level tests exercise the guard."""
    from app.graph import workflow

    original = workflow.scoped

    def with_default(state, payload):
        if not (state.get("scope") or {}).get("tenant_id"):
            state = {**state, "scope": {**(state.get("scope") or {}), "tenant_id": 1, "shop_id": "test-shop"}}
        return original(state, payload)

    monkeypatch.setattr(workflow, "scoped", with_default)
