import hashlib
from pathlib import Path

import pytest

from app.ingest.catalog import (
    PRODUCT_ATTRIBUTES,
    PRODUCT_BASE_FIELDS,
    PRODUCT_TABLE_SCHEMA,
    SIZE_CHART_BASE_FIELDS,
    SIZE_CHART_MEASUREMENTS,
    SIZE_CHART_SCHEMA,
    CatalogValidationError,
    build_typed_rows,
    chunk_projection,
    normalize_headers,
)
from app.ingest.package_validation import _inspect_package
from app.ingest.pipeline import CSVParser, chunk_elements_v2
from rag.v1 import rag_pb2


class AbortContext:
    def abort(self, code, details):
        raise ValueError(details)


def test_product_headers_use_aliases_without_a_second_schema():
    headers = normalize_headers(
        ["sku", "product_name", "price_cents", "tenant_id", "shop_id", "synthetic"],
        PRODUCT_TABLE_SCHEMA,
    )
    assert headers == [
        "sku_code", "title", "price_cents", "tenant_id", "shop_id", "synthetic",
    ]


def test_size_chart_headers_use_canonical_measurements():
    headers = normalize_headers(["尺码表", "尺码", "胸围", "腰围"], SIZE_CHART_SCHEMA)
    assert headers == ["chart_code", "size_label", "chest_cm", "waist_cm"]


def test_chunk_projection_carries_catalog_row_fields():
    projection = chunk_projection(
        PRODUCT_TABLE_SCHEMA,
        {"sku_code": "SKU-1", "chart_code": ""},
        7,
    )
    assert projection == {
        "table_schema": "product_table_v1",
        "source_row": 7,
        "catalog_row_key": "sku:SKU-1",
        "sku_code": "SKU-1",
    }


def test_typed_csv_chunks_carry_projection_and_use_aliases(tmp_path: Path):
    path = tmp_path / "products.csv"
    path.write_text(
        "sku,product_name,tenant_id,shop_id\nSKU-1,商品一,tenant-a,shop-a\n",
        encoding="utf-8",
    )
    metadata = {
        "tenant_id": "tenant-a",
        "shop_id": "shop-a",
        "title": "products",
        "disclosure_class": "external_allowed",
        "effective_from": "2026-01-01",
        "effective_to": None,
        "table_schema": PRODUCT_TABLE_SCHEMA,
    }
    chunks = chunk_elements_v2(CSVParser().parse(
        path, document_id="products", version_id="v1", metadata=metadata,
    ))
    assert chunks[0].metadata["table_schema"] == PRODUCT_TABLE_SCHEMA
    assert chunks[0].metadata["catalog_row_key"] == "sku:SKU-1"
    assert chunks[0].metadata["sku_code"] == "SKU-1"
    assert chunks[0].metadata["source_row"] == 2


def test_package_validation_accepts_typed_product_manifest():
    content = b"sku,title\nSKU-1,\xe5\x95\x86\xe5\x93\x81\n"
    sha = hashlib.sha256(content).hexdigest()
    manifest = (
        "schema_version: 1\npipeline_version: typed-test\nsource:\n  id: products\n  name: Products\n"
        f"documents:\n  - document_id: products\n    title: Products\n    path: products.csv\n"
        f"    format: csv\n    doc_type: product_table\n    table_schema: product_table_v1\n"
        f"    sha256: {sha}\n    disclosure_class: external_allowed\n"
    )
    request = rag_pb2.ValidatePackageRequest(
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="products.csv", content=content)],
        tenant_id="tenant-a", shop_id="shop-a",
    )
    spec = _inspect_package(request, AbortContext())["documents"][0]
    assert spec["doc_type"] == "product_table"
    assert spec["table_schema"] == "product_table_v1"


def test_package_validation_keeps_old_doc_type_behavior():
    content = b"sku,title\nSKU-1,Item\n"
    sha = hashlib.sha256(content).hexdigest()
    manifest = (
        "schema_version: 1\npipeline_version: old-test\nsource:\n  id: products\n  name: Products\n"
        f"documents:\n  - document_id: products\n    title: Products\n    path: products.csv\n"
        f"    format: csv\n    doc_type: product\n    sha256: {sha}\n"
    )
    request = rag_pb2.ValidatePackageRequest(
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="products.csv", content=content)],
        tenant_id="tenant-a", shop_id="shop-a",
    )
    spec = _inspect_package(request, AbortContext())["documents"][0]
    assert spec["doc_type"] == "csv"
    assert spec["table_schema"] == ""


def _typed_rows(path: Path, schema: str, *, tenant_id: str = "tenant-a", shop_id: str = "shop-a"):
    metadata = {
        "tenant_id": tenant_id, "shop_id": shop_id, "title": "typed",
        "disclosure_class": "external_allowed", "effective_from": "2026-01-01",
        "effective_to": None, "table_schema": schema,
    }
    elements = CSVParser().parse(path, document_id="typed", version_id="v1", metadata=metadata)
    chunks = chunk_elements_v2(elements)
    return build_typed_rows(elements, chunks, {"table_schema": schema})


def test_synthetic_product_csv_builds_typed_rows_and_keeps_unknown_attributes():
    root = Path(__file__).resolve().parents[2]
    rows, charts, warnings = _typed_rows(
        root / "data/synthetic/ecommerce-demo-v1/products.csv", PRODUCT_TABLE_SCHEMA,
        tenant_id="demo-tenant-a", shop_id="demo-shop-east",
    )
    assert len(rows) == 7 and charts == []
    first = rows[0]
    assert first["sku_code"] == "SKU-CUP-480"
    assert first["title"] == "晨雾保温杯"
    assert first["chunk_index"] == 0
    assert first["attributes"]["specification"] == "480ml;杯口6cm;304不锈钢"
    assert first["attributes"]["price_cents"] == "8900"
    assert any(warning == "unknown_attribute:price_cents" for warning in warnings)


def test_typed_product_rows_reject_missing_observed_price(tmp_path: Path):
    path = tmp_path / "products.csv"
    path.write_text(
        "sku_code,title,list_price_cents\nSKU-1,Item,8900\n", encoding="utf-8",
    )
    with pytest.raises(CatalogValidationError, match="price_observed_at_required"):
        _typed_rows(path, PRODUCT_TABLE_SCHEMA)


def test_typed_product_rows_reject_case_insensitive_duplicate_sku(tmp_path: Path):
    path = tmp_path / "products.csv"
    path.write_text(
        "sku_code,title\nSKU-1,One\nsku-1,Two\n", encoding="utf-8",
    )
    with pytest.raises(CatalogValidationError, match="duplicate_sku"):
        _typed_rows(path, PRODUCT_TABLE_SCHEMA)


def test_size_chart_builds_mapping_and_rejects_unknown_system(tmp_path: Path):
    path = tmp_path / "sizes.csv"
    path.write_text(
        "chart_code,size_label,size_system,chest_cm,unknown_note\n"
        "C1,M,unisex,90,keep\n",
        encoding="utf-8",
    )
    _rows, charts, warnings = _typed_rows(path, SIZE_CHART_SCHEMA)
    assert charts[0]["chart_code"] == "C1"
    assert charts[0]["size_label"] == "M"
    assert charts[0]["measurements"] == {"chest_cm": 90, "unknown_note": "keep"}
    assert "unknown_attribute:unknown_note" in warnings
    path.write_text(
        "chart_code,size_label,size_system\nC1,M,not-a-system\n", encoding="utf-8",
    )
    with pytest.raises(CatalogValidationError, match="invalid_size_system"):
        _typed_rows(path, SIZE_CHART_SCHEMA)


def test_product_schema_matches_template_columns():
    template_columns = {
        "sku_code", "spu_code", "title", "category", "brand", "color",
        "size_label", "size_chart_code", "capacity_ml", "length_cm",
        "width_cm", "height_cm", "weight_g", "material", "pattern",
        "list_price_cents", "currency", "price_observed_at",
    }
    assert PRODUCT_BASE_FIELDS | PRODUCT_ATTRIBUTES == template_columns


def test_size_chart_schema_matches_template_columns():
    template_columns = {
        "chart_code", "size_label", "size_system", "chest_cm", "waist_cm",
        "hip_cm", "foot_length_cm", "height_cm", "weight_kg", "fit_note",
    }
    assert SIZE_CHART_BASE_FIELDS | SIZE_CHART_MEASUREMENTS == template_columns
