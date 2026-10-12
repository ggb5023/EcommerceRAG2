"""Framework-free read-only fact routing shared by offline AI boundaries.

Go owns tool execution and authorization. These names only constrain AI
routing output and validate facts already supplied by the control plane.
"""

import re

ALLOWED_FACT_TOOLS = frozenset({
    "get_stock", "get_price", "get_order_status", "get_logistics", "compare_products",
})


def extract_order_entities(query: str) -> list[dict[str, str | float]]:
    """Extract literal identifiers for offline routing, never infer ownership."""
    return [
        {"name": "order_id", "value": match.group(1), "confidence": 0.99}
        for match in re.finditer(
            r"(?:订单(?:号|编号)|order(?:\s*id)?)\s*(?:是|为|[:：=#])?\s*([A-Za-z0-9][A-Za-z0-9_-]*)",
            query,
            re.IGNORECASE,
        )
    ]


def infer_fact_tools(query: str) -> list[str]:
    """Route explicit development requests to the Go-owned tool registry."""
    lowered = query.lower()
    routes = (
        ("get_price", ("价格", "多少钱", "price")),
        ("get_stock", ("库存", "有货", "stock")),
        ("get_order_status", ("订单", "order")),
        ("get_logistics", ("物流", "配送状态", "logistics", "shipping status")),
        ("compare_products", ("比较", "对比", "compare")),
    )
    matched = []
    for name, words in routes:
        positions = [lowered.index(word) for word in words if word in lowered]
        if positions:
            matched.append((min(positions), name))
    return [name for _, name in sorted(matched)]
