"""Typed product-table and size-chart schema helpers.

The canonical column names and limits come from decision 0034.  Aliases only
map common merchant-export headers onto those canonical names; they do not
create a second schema.  Typed rows are written by the ingest worker in the
same transaction as the document version and its chunk projection.
"""
from __future__ import annotations

from collections.abc import Iterable
from typing import Any

PRODUCT_DOC_TYPE = "product_table"
SIZE_CHART_DOC_TYPE = "size_chart"
PRODUCT_TABLE_SCHEMA = "product_table_v1"
SIZE_CHART_SCHEMA = "size_chart_v1"
TYPED_SCHEMAS = frozenset({PRODUCT_TABLE_SCHEMA, SIZE_CHART_SCHEMA})
DOC_TYPE_SCHEMAS = {
    PRODUCT_DOC_TYPE: PRODUCT_TABLE_SCHEMA,
    SIZE_CHART_DOC_TYPE: SIZE_CHART_SCHEMA,
}

PRODUCT_BASE_FIELDS = frozenset({
    "sku_code", "spu_code", "title", "category", "brand", "color",
    "size_label", "size_chart_code", "list_price_cents", "currency",
    "price_observed_at",
})
PRODUCT_NUMERIC_ATTRIBUTES = frozenset({
    "capacity_ml", "length_cm", "width_cm", "height_cm", "weight_g",
})
PRODUCT_TEXT_ATTRIBUTES = frozenset({"material", "pattern"})
PRODUCT_ATTRIBUTES = PRODUCT_NUMERIC_ATTRIBUTES | PRODUCT_TEXT_ATTRIBUTES

SIZE_CHART_BASE_FIELDS = frozenset({
    "chart_code", "size_label", "size_system", "fit_note",
})
SIZE_CHART_MEASUREMENTS = frozenset({
    "chest_cm", "waist_cm", "hip_cm", "foot_length_cm", "height_cm",
    "weight_kg",
})
SIZE_SYSTEMS = frozenset({"cn_shoe", "cn_clothing", "cn_kids", "unisex", "custom"})

# Export control columns are filtered by the parser or ignored as metadata.
CONTROL_COLUMNS = frozenset({
    "tenant_id", "shop_id", "synthetic", "provisional", "license", "fact_type",
})

PRODUCT_ALIASES = {
    "sku": "sku_code",
    "sku_id": "sku_code",
    "merchant_sku": "sku_code",
    "商家编码": "sku_code",
    "货号": "sku_code",
    "outer_id": "sku_code",
    "product_name": "title",
    "name": "title",
    "商品名称": "title",
    "商品标题": "title",
    "标题": "title",
    "spu": "spu_code",
    "spu_id": "spu_code",
    "product_id": "spu_code",
    "商品id": "spu_code",
    "商品编码": "spu_code",
    "类目": "category",
    "分类": "category",
    "品牌": "brand",
    "颜色": "color",
    "尺码": "size_label",
    "尺寸": "size_label",
    "尺码表": "size_chart_code",
    "尺码表编码": "size_chart_code",
    "币种": "currency",
    "价格日期": "price_observed_at",
}
SIZE_CHART_ALIASES = {
    "chart": "chart_code",
    "尺码表": "chart_code",
    "尺码表编码": "chart_code",
    "size": "size_label",
    "尺码": "size_label",
    "尺码体系": "size_system",
    "胸围": "chest_cm",
    "腰围": "waist_cm",
    "臀围": "hip_cm",
    "脚长": "foot_length_cm",
    "身高": "height_cm",
    "体重": "weight_kg",
    "版型说明": "fit_note",
}


class CatalogValidationError(ValueError):
    """A product-table row cannot be normalized into the typed schema."""


def _text(value: Any) -> str:
    return "" if value is None else str(value).strip()


def normalize_headers(headers: Iterable[str], schema: str) -> list[str]:
    """Map export aliases to canonical headers, rejecting ambiguous duplicates."""
    if schema == PRODUCT_TABLE_SCHEMA:
        aliases = PRODUCT_ALIASES
        canonical_columns = PRODUCT_BASE_FIELDS | PRODUCT_ATTRIBUTES | CONTROL_COLUMNS
    elif schema == SIZE_CHART_SCHEMA:
        aliases = SIZE_CHART_ALIASES
        canonical_columns = SIZE_CHART_BASE_FIELDS | SIZE_CHART_MEASUREMENTS | CONTROL_COLUMNS
    else:
        raise CatalogValidationError(f"unsupported_table_schema:{schema}")
    normalized: list[str] = []
    seen: dict[str, str] = {}
    for value in headers:
        original = _text(value)
        key = original.lower()
        canonical = aliases.get(key, key if key in canonical_columns else original)
        if canonical in seen:
            raise CatalogValidationError(
                f"header_alias_conflict:{seen[canonical]}:{original}"
            )
        seen[canonical] = original
        normalized.append(canonical)
    return normalized


def normalize_row(headers: list[str], values: Iterable[Any]) -> dict[str, str]:
    return {header: _text(value) for header, value in zip(headers, values, strict=False)}


def catalog_row_key(schema: str, row: dict[str, str]) -> str:
    if schema == PRODUCT_TABLE_SCHEMA:
        sku = _text(row.get("sku_code"))
        return f"sku:{sku}" if sku else ""
    if schema == SIZE_CHART_SCHEMA:
        chart = _text(row.get("chart_code"))
        size = _text(row.get("size_label"))
        return f"chart:{chart}:{size}" if chart and size else ""
    return ""


def chunk_projection(schema: str, row: dict[str, str], source_row: int) -> dict[str, Any]:
    """Fields copied into chunk.meta_json for a typed row."""
    projection: dict[str, Any] = {
        "table_schema": schema,
        "source_row": source_row,
        "catalog_row_key": catalog_row_key(schema, row),
    }
    if schema == PRODUCT_TABLE_SCHEMA:
        projection["sku_code"] = _text(row.get("sku_code"))
    elif schema == SIZE_CHART_SCHEMA:
        projection["chart_code"] = _text(row.get("chart_code"))
    return projection
