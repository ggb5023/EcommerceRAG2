import hashlib
from io import BytesIO

from openpyxl import Workbook

from app.ingest.catalog import limit_row_errors
from app.rpc.ingest import IngestService
from rag.v1 import rag_pb2


class AbortContext:
    def abort(self, code, details):
        raise ValueError(details)


def package(content: str, *, doc_type: str = "product_table",
            table_schema: str = "product_table_v1") -> rag_pb2.ValidatePackageRequest:
    body = content.encode()
    sha = hashlib.sha256(body).hexdigest()
    manifest = (
        "schema_version: 1\npipeline_version: precheck-test\nsource:\n  id: products\n  name: Products\n"
        f"documents:\n  - document_id: products\n    title: Products\n    path: products.csv\n"
        f"    format: csv\n    doc_type: {doc_type}\n    table_schema: {table_schema}\n"
        f"    sha256: {sha}\n    disclosure_class: external_allowed\n"
    )
    return rag_pb2.ValidatePackageRequest(
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="products.csv", content=body)],
        tenant_id="tenant-a", shop_id="shop-a",
    )


def validate(request):
    return object.__new__(IngestService).ValidatePackage(request, AbortContext())


def test_precheck_collects_duplicate_sku_and_unknown_size_chart():
    response = validate(package(
        "sku_code,title,size_label,size_chart_code\n"
        "SKU-1,One,M,C1\n"
        "sku-1,Two,M,C1\n"
    ))
    document = response.documents[0]
    codes = {issue.code for issue in document.row_errors}
    assert document.table_schema == "product_table_v1"
    assert document.row_count == 2
    assert document.error_count >= 2
    assert "duplicate_sku" in codes
    assert "unknown_size_chart" in codes
    assert document.truncated is False


def test_precheck_collects_price_and_attribute_errors():
    response = validate(package(
        "sku_code,title,capacity_ml,list_price_cents\n"
        "SKU-1,One,not-a-number,\n"
        "SKU-2,Two,,8900\n"
    ))
    issues = response.documents[0].row_errors
    codes = {issue.code for issue in issues}
    assert "invalid_number" in codes
    assert "price_observed_at_required" in codes
    assert all(issue.severity == "block" for issue in issues if issue.code != "unknown_column")


def test_precheck_reports_unknown_columns_as_warnings():
    response = validate(package(
        "sku_code,title,price_cents,specification\n"
        "SKU-1,One,8900,480ml\n"
    ))
    issues = response.documents[0].row_errors
    unknown = [issue for issue in issues if issue.code == "unknown_column"]
    assert {issue.column for issue in unknown} == {"price_cents", "specification"}
    assert all(issue.severity == "warn" for issue in unknown)


def test_precheck_reports_parser_header_errors_as_row_errors():
    response = validate(package("sku_code,sku_code\nSKU-1,SKU-2\n"))
    issues = response.documents[0].row_errors
    assert issues[0].code == "duplicate_header"
    assert issues[0].severity == "block"


def test_limit_row_errors_truncates_count_and_fields():
    issues = [{
        "row": 2, "column": "sku_code", "code": "invalid_sku", "severity": "block",
        "value": "x" * 300, "message": "m" * 300, "suggested_action": "a" * 300,
    } for _ in range(205)]
    limited, total, truncated = limit_row_errors(issues)
    assert len(limited) == 200
    assert total == 205
    assert truncated is True
    assert all(len(value) <= 200 for value in limited[0].values() if isinstance(value, str))


def test_precheck_reports_missing_required_column_and_column_mismatch():
    missing = validate(package("sku_code\nSKU-1\n"))
    codes = {issue.code for issue in missing.documents[0].row_errors}
    assert "missing_required_column" in codes
    mismatch = validate(package("sku_code,title\nSKU-1,One\nSKU-2\n"))
    assert any(issue.code == "column_mismatch" for issue in mismatch.documents[0].row_errors)


def test_precheck_reports_invalid_size_system():
    response = validate(package(
        "chart_code,size_label,size_system\nC1,M,not-a-system\n",
        doc_type="size_chart", table_schema="size_chart_v1",
    ))
    issues = response.documents[0].row_errors
    assert any(issue.code == "invalid_size_system" and issue.column == "size_system" for issue in issues)


def test_precheck_maps_xlsx_formula_to_row_error():
    workbook = Workbook()
    worksheet = workbook.active
    worksheet.append(["sku_code", "title"])
    worksheet.append(["SKU-1", "=1+1"])
    stream = BytesIO()
    workbook.save(stream)
    workbook.close()
    body = stream.getvalue()
    sha = hashlib.sha256(body).hexdigest()
    manifest = (
        "schema_version: 1\npipeline_version: precheck-test\nsource:\n  id: products\n  name: Products\n"
        f"documents:\n  - document_id: products\n    title: Products\n    path: products.xlsx\n"
        f"    format: xlsx\n    doc_type: product_table\n    table_schema: product_table_v1\n"
        f"    sha256: {sha}\n    disclosure_class: external_allowed\n"
    )
    request = rag_pb2.ValidatePackageRequest(
        manifest_yaml=manifest,
        files=[rag_pb2.PackageFile(path="products.xlsx", content=body)],
        tenant_id="tenant-a", shop_id="shop-a",
    )
    response = validate(request)
    assert any(issue.code == "formula_not_allowed" for issue in response.documents[0].row_errors)
