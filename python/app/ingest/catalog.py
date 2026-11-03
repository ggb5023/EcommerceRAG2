"""Typed product-table and size-chart schema helpers.

The canonical column names and limits come from decision 0034.  Aliases only
map common merchant-export headers onto those canonical names; they do not
create a second schema.  Typed rows are written by the ingest worker in the
same transaction as the document version and its chunk projection.
"""
from __future__ import annotations

from collections.abc import Iterable
from datetime import date
from decimal import Decimal, InvalidOperation
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


def _number(value: str, field: str) -> int | float:
    try:
        number = Decimal(value)
    except (InvalidOperation, ValueError) as error:
        raise CatalogValidationError(f"invalid_number:{field}") from error
    if not number.is_finite() or number < 0:
        raise CatalogValidationError(f"invalid_number:{field}")
    return int(number) if number == number.to_integral_value() else float(number)


def _date_value(value: str, field: str) -> date:
    try:
        return date.fromisoformat(value)
    except ValueError as error:
        raise CatalogValidationError(f"invalid_date:{field}") from error


def _registered_attributes(row: dict[str, str], *, numeric: frozenset[str],
                           text: frozenset[str], known: frozenset[str],
                           warnings: list[str]) -> dict[str, Any]:
    attributes: dict[str, Any] = {}
    for key in numeric:
        value = _text(row.get(key))
        if value:
            attributes[key] = _number(value, key)
    for key in text:
        value = _text(row.get(key))
        if value:
            attributes[key] = value
    for key, raw in row.items():
        if key in known or key in numeric or key in text or key in CONTROL_COLUMNS or not _text(raw):
            continue
        attributes[key] = _text(raw)
        warnings.append(f"unknown_attribute:{key}")
    return attributes


def _chunk_indexes(chunks: Iterable[Any]) -> dict[str, int]:
    indexes: dict[str, int] = {}
    for chunk in chunks:
        metadata = getattr(chunk, "metadata", {}) or {}
        key = str(metadata.get("catalog_row_key") or "")
        if not key:
            continue
        index = int(chunk.chunk_index)
        if key not in indexes or index < indexes[key]:
            indexes[key] = index
    return indexes


def _source_row(element: Any) -> int:
    position = getattr(element, "source_position", {}) or {}
    value = position.get("line_start", position.get("record_number", 0))
    try:
        row = int(value)
    except (TypeError, ValueError) as error:
        raise CatalogValidationError("source_row_missing") from error
    if row < 1:
        raise CatalogValidationError("source_row_missing")
    return row


def _chunk_index(element: Any, indexes: dict[str, int], schema: str) -> int:
    row = getattr(element, "catalog_row", None) or {}
    key = catalog_row_key(schema, row)
    if not key or key not in indexes:
        raise CatalogValidationError(f"catalog_chunk_missing:{key}")
    return indexes[key]


def _product_row(element: Any, row: dict[str, str], indexes: dict[str, int],
                 warnings: list[str]) -> dict[str, Any]:
    sku = _text(row.get("sku_code"))
    title = _text(row.get("title"))
    if not sku or len(sku) > 128:
        raise CatalogValidationError(f"invalid_sku:{_source_row(element)}")
    if not title:
        raise CatalogValidationError(f"missing_title:{_source_row(element)}")
    price_text = _text(row.get("list_price_cents"))
    observed_text = _text(row.get("price_observed_at"))
    currency = _text(row.get("currency")).upper() or None
    price = None
    observed = None
    if price_text:
        price = _number(price_text, "list_price_cents")
        if not isinstance(price, int):
            raise CatalogValidationError("invalid_list_price_cents")
        if not observed_text:
            raise CatalogValidationError("price_observed_at_required")
        observed = _date_value(observed_text, "price_observed_at")
        if currency is None:
            currency = "CNY"
        if len(currency) != 3 or not currency.isalpha():
            raise CatalogValidationError("invalid_currency")
    else:
        if observed_text:
            raise CatalogValidationError("price_observed_at_requires_price")
        if currency is not None:
            raise CatalogValidationError("currency_requires_price")
    attributes = _registered_attributes(
        row, numeric=PRODUCT_NUMERIC_ATTRIBUTES, text=PRODUCT_TEXT_ATTRIBUTES,
        known=PRODUCT_BASE_FIELDS, warnings=warnings,
    )
    return {
        "source_row": _source_row(element),
        "chunk_index": _chunk_index(element, indexes, PRODUCT_TABLE_SCHEMA),
        "sku_code": sku,
        "spu_code": _text(row.get("spu_code")) or None,
        "title": title,
        "category": _text(row.get("category")) or None,
        "brand": _text(row.get("brand")) or None,
        "color": _text(row.get("color")) or None,
        "size_label": _text(row.get("size_label")) or None,
        "size_chart_code": _text(row.get("size_chart_code")) or None,
        "attributes": attributes,
        "list_price_cents": price,
        "currency": currency,
        "price_observed_at": observed,
    }


def _size_chart_row(element: Any, row: dict[str, str], indexes: dict[str, int],
                    warnings: list[str]) -> dict[str, Any]:
    chart = _text(row.get("chart_code"))
    size = _text(row.get("size_label"))
    system = _text(row.get("size_system"))
    if not chart:
        raise CatalogValidationError(f"missing_chart_code:{_source_row(element)}")
    if not size:
        raise CatalogValidationError(f"missing_size_label:{_source_row(element)}")
    if system not in SIZE_SYSTEMS:
        raise CatalogValidationError(f"invalid_size_system:{system or 'empty'}")
    measurements = _registered_attributes(
        row, numeric=SIZE_CHART_MEASUREMENTS, text=frozenset(),
        known=SIZE_CHART_BASE_FIELDS, warnings=warnings,
    )
    return {
        "source_row": _source_row(element),
        "chunk_index": _chunk_index(element, indexes, SIZE_CHART_SCHEMA),
        "chart_code": chart,
        "size_label": size,
        "size_system": system,
        "measurements": measurements,
        "fit_note": _text(row.get("fit_note")) or None,
    }


def _row_issue(row: int, column: str, code: str, severity: str, value: Any,
               message: str, suggested_action: str) -> dict[str, Any]:
    return {
        "row": int(row),
        "column": column,
        "code": code,
        "severity": severity,
        "value": _text(value),
        "message": message,
        "suggested_action": suggested_action,
    }


def _row_error_issue(error: CatalogValidationError, element: Any,
                     row: dict[str, str]) -> dict[str, Any]:
    text = str(error)
    code, _, payload = text.partition(":")
    column = ""
    value = ""
    if code == "invalid_sku":
        column, value = "sku_code", row.get("sku_code", "")
    elif code == "missing_title":
        column, value = "title", row.get("title", "")
    elif code in {"price_observed_at_required", "price_observed_at_requires_price"}:
        column, value = "price_observed_at", row.get("price_observed_at", "")
    elif code in {"currency_requires_price", "invalid_currency"}:
        column, value = "currency", row.get("currency", "")
    elif code in {"invalid_number", "invalid_date"}:
        column = payload or "attributes"
        value = row.get(column, "")
    elif code == "missing_chart_code":
        column, value = "chart_code", row.get("chart_code", "")
    elif code == "missing_size_label":
        column, value = "size_label", row.get("size_label", "")
    elif code == "invalid_size_system":
        column, value = "size_system", row.get("size_system", "")
    elif code == "invalid_list_price_cents":
        code, column, value = "invalid_number", "list_price_cents", row.get("list_price_cents", "")
    elif code == "catalog_chunk_missing":
        column = "catalog_row_key"
    actions = {
        "invalid_sku": "填写非空且不超过 128 字符的 SKU 编码",
        "missing_title": "填写商品标题",
        "price_observed_at_required": "为目录价填写 price_observed_at",
        "price_observed_at_requires_price": "删除价格观测日或补上 list_price_cents",
        "currency_requires_price": "删除币种或补上 list_price_cents",
        "invalid_currency": "使用三位大写币种代码",
        "invalid_number": "填写非负数值，单位写在列名中",
        "invalid_date": "使用 YYYY-MM-DD",
        "missing_chart_code": "填写尺码表编码",
        "missing_size_label": "填写尺码",
        "invalid_size_system": "使用已登记的尺码体系枚举",
        "catalog_chunk_missing": "检查该行是否能生成 chunk",
    }
    return _row_issue(
        _source_row(element), column, code, "block", value,
        text or code, actions.get(code, "修正该行后重新预检"),
    )


def validate_typed_document(elements: Iterable[Any], chunks: Iterable[Any],
                            spec: dict[str, Any], *,
                            known_size_charts: set[tuple[str, str]] | None = None,
                            ) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    """Validate typed rows without writing or stopping at the first bad row.

    Row construction still goes through the same `_product_row` /
    `_size_chart_row` functions as the ingest worker; this function only
    changes error handling to collect issues for the precheck response.
    """
    schema = str(spec.get("table_schema") or "")
    if schema not in TYPED_SCHEMAS:
        return [], [], []
    materialized = list(elements)
    issues: list[dict[str, Any]] = []
    if not materialized:
        issues.append(_row_issue(1, "", "empty_table", "block", "", "表格没有数据行", "补充至少一行数据"))
        return [], [], issues
    headers = set(materialized[0].catalog_row or {})
    required = ({"sku_code", "title"} if schema == PRODUCT_TABLE_SCHEMA
                else {"chart_code", "size_label", "size_system"})
    for column in sorted(required - headers):
        issues.append(_row_issue(1, column, "missing_required_column", "block", "",
                                 f"缺少必填列 {column}", "在模板首行补上该列"))
    known = ((PRODUCT_BASE_FIELDS | PRODUCT_ATTRIBUTES | CONTROL_COLUMNS)
             if schema == PRODUCT_TABLE_SCHEMA
             else (SIZE_CHART_BASE_FIELDS | SIZE_CHART_MEASUREMENTS | CONTROL_COLUMNS))
    for column in sorted(headers - known):
        issues.append(_row_issue(1, column, "unknown_column", "warn", "",
                                 f"未登记列 {column}", "保留为属性，不参与过滤"))
    indexes = _chunk_indexes(chunks)
    sku_rows: list[dict[str, Any]] = []
    chart_rows: list[dict[str, Any]] = []
    seen_sku: set[str] = set()
    seen_chart: set[tuple[str, str]] = set()
    for element in materialized:
        row = getattr(element, "catalog_row", None) or {}
        try:
            if schema == PRODUCT_TABLE_SCHEMA:
                built = _product_row(element, row, indexes, [])
                key = built["sku_code"].lower()
                if key in seen_sku:
                    issues.append(_row_issue(
                        _source_row(element), "sku_code", "duplicate_sku", "block",
                        built["sku_code"], "同一版本内 SKU 重复（大小写不敏感）", "改用唯一 SKU 或合并重复行",
                    ))
                    continue
                seen_sku.add(key)
                sku_rows.append(built)
            else:
                built = _size_chart_row(element, row, indexes, [])
                key = (built["chart_code"], built["size_label"])
                if key in seen_chart:
                    issues.append(_row_issue(
                        _source_row(element), "chart_code", "duplicate_size_chart", "block",
                        f"{key[0]}:{key[1]}", "同一版本内尺码表行重复", "删除重复行",
                    ))
                    continue
                seen_chart.add(key)
                chart_rows.append(built)
        except CatalogValidationError as error:
            issues.append(_row_error_issue(error, element, row))
    if schema == PRODUCT_TABLE_SCHEMA:
        references = known_size_charts or set()
        for built in sku_rows:
            chart = built.get("size_chart_code") or ""
            size = built.get("size_label") or ""
            if not chart:
                continue
            if (chart, size) not in references:
                chart_exists = any(item[0] == chart for item in references)
                code = "unknown_size" if chart_exists else "unknown_size_chart"
                column = "size_label" if chart_exists else "size_chart_code"
                issues.append(_row_issue(
                    built["source_row"], column, code, "warn",
                    f"{chart}:{size}", "预检未找到对应的 active 尺码表/尺码", "补充尺码表后再发布",
                ))
    return sku_rows, chart_rows, issues


def limit_row_errors(issues: Iterable[dict[str, Any]], *, max_errors: int = 200,
                     max_chars: int = 200) -> tuple[list[dict[str, Any]], int, bool]:
    materialized = list(issues)
    truncated = len(materialized) > max_errors
    limited: list[dict[str, Any]] = []
    for issue in materialized[:max_errors]:
        clean = dict(issue)
        for key, value in clean.items():
            if isinstance(value, str) and len(value) > max_chars:
                clean[key] = value[:max_chars]
                truncated = True
        limited.append(clean)
    return limited, len(materialized), truncated


def build_typed_rows(elements: Iterable[Any], chunks: Iterable[Any],
                     spec: dict[str, Any]) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[str]]:
    """Normalize typed rows and attach their first chunk index.

    The caller writes the returned rows and the chunks in one database
    transaction.  Unknown columns are retained as attributes and reported,
    never silently discarded.
    """
    schema = str(spec.get("table_schema") or "")
    if schema not in TYPED_SCHEMAS:
        return [], [], []
    indexes = _chunk_indexes(chunks)
    sku_rows: list[dict[str, Any]] = []
    chart_rows: list[dict[str, Any]] = []
    warnings: list[str] = []
    seen_sku: set[str] = set()
    seen_chart: set[tuple[str, str]] = set()
    for element in elements:
        row = getattr(element, "catalog_row", None)
        if not row:
            continue
        if schema == PRODUCT_TABLE_SCHEMA:
            built = _product_row(element, row, indexes, warnings)
            key = built["sku_code"].lower()
            if key in seen_sku:
                raise CatalogValidationError(f"duplicate_sku:{built['sku_code']}")
            seen_sku.add(key)
            sku_rows.append(built)
        else:
            built = _size_chart_row(element, row, indexes, warnings)
            key = (built["chart_code"], built["size_label"])
            if key in seen_chart:
                raise CatalogValidationError(
                    f"duplicate_size_chart:{built['chart_code']}:{built['size_label']}"
                )
            seen_chart.add(key)
            chart_rows.append(built)
    return sku_rows, chart_rows, warnings
