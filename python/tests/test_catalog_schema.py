import hashlib
from pathlib import Path

from app.ingest.catalog import (
    PRODUCT_TABLE_SCHEMA,
    SIZE_CHART_SCHEMA,
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
