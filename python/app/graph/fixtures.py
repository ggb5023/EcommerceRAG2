"""Small deterministic corpus used until the real retriever is connected."""
from __future__ import annotations

from .state import Evidence

FIXTURE_CORPUS: tuple[Evidence, ...] = (
    {
        "id": "v2-ev-shipping", "content": "配送通常在付款后 1-3 个工作日内发出。",
        "source_type": "v2_fixture", "source_ref": "fixture://v2/shipping",
        "document_id": "v2-doc-shipping", "version_id": "v2-shipping-v1",
        "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
        "disclosure_class": "external_allowed", "customer_eligible": True,
        "rank": 0, "raw_score": 0.0, "rrf_score": 0.0,
    },
    {
        "id": "v2-ev-bottle", "content": "保温杯容量为 480 毫升，杯口直径为 6 厘米。",
        "source_type": "v2_fixture", "source_ref": "fixture://v2/bottle",
        "document_id": "v2-doc-bottle", "version_id": "v2-bottle-v1",
        "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
        "disclosure_class": "external_allowed", "customer_eligible": True,
        "rank": 0, "raw_score": 0.0, "rrf_score": 0.0,
    },
    {
        "id": "v2-ev-policy-internal", "content": "内部审核备注，不得直接作为顾客回复。",
        "source_type": "v2_fixture", "source_ref": "fixture://v2/internal",
        "document_id": "v2-doc-internal", "version_id": "v2-internal-v1",
        "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
        "disclosure_class": "internal_only", "customer_eligible": False,
        "rank": 0, "raw_score": 0.0, "rrf_score": 0.0,
    },
)
