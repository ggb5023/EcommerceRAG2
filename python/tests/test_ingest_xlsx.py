from pathlib import Path
from zipfile import ZipFile

import pytest
from openpyxl import Workbook

from app.ingest import pipeline
from app.ingest.catalog import PRODUCT_TABLE_SCHEMA
from app.ingest.pipeline import CSVParser, XLSXParser, chunk_elements_v2


def metadata() -> dict[str, str | None]:
    return {
        "tenant_id": "tenant-a",
        "shop_id": "shop-a",
        "title": "products",
        "disclosure_class": "external_allowed",
        "effective_from": "2026-01-01",
        "effective_to": None,
    }


def write_workbook(path: Path, rows: list[list[object]]) -> None:
    workbook = Workbook()
    worksheet = workbook.active
    for row in rows:
        worksheet.append(row)
    workbook.save(path)
    workbook.close()


def test_xlsx_matches_csv_output_shape(tmp_path: Path):
    rows = [["tenant_id", "shop_id", "sku", "title"], ["tenant-a", "shop-a", "A", "商品A"]]
    xlsx = tmp_path / "products.xlsx"
    write_workbook(xlsx, rows)
    csv_path = tmp_path / "products.csv"
    csv_path.write_text("\n".join(",".join(str(value) for value in row) for row in rows) + "\n", encoding="utf-8")
    xlsx_elements = XLSXParser().parse(xlsx, document_id="doc", version_id="v1", metadata=metadata())
    csv_elements = CSVParser().parse(csv_path, document_id="doc", version_id="v1", metadata=metadata())
    assert [element.content for element in xlsx_elements] == [element.content for element in csv_elements]
    assert [element.table_body for element in xlsx_elements] == [element.table_body for element in csv_elements]
    assert xlsx_elements[0].source_position == {"line_start": 2, "line_end": 2, "record_number": 1}


def test_xlsx_typed_rows_carry_chunk_projection(tmp_path: Path):
    path = tmp_path / "products.xlsx"
    write_workbook(path, [["sku", "product_name"], ["SKU-1", "商品一"]])
    typed_metadata = {**metadata(), "table_schema": PRODUCT_TABLE_SCHEMA}
    chunks = chunk_elements_v2(XLSXParser().parse(
        path, document_id="doc", version_id="v1", metadata=typed_metadata,
    ))
    assert chunks[0].metadata["table_schema"] == PRODUCT_TABLE_SCHEMA
    assert chunks[0].metadata["sku_code"] == "SKU-1"
    assert chunks[0].metadata["catalog_row_key"] == "sku:SKU-1"
    assert chunks[0].metadata["source_row"] == 2


def test_xlsx_filters_rows_outside_tenant_and_shop(tmp_path: Path):
    path = tmp_path / "products.xlsx"
    write_workbook(path, [
        ["tenant_id", "shop_id", "sku"],
        ["tenant-a", "shop-a", "OWN"],
        ["tenant-b", "shop-b", "OTHER"],
    ])
    elements = XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())
    assert len(elements) == 1
    assert "OWN" in elements[0].content
    assert all("OTHER" not in element.content for element in elements)


def test_xlsx_rejects_formula_before_reading_values(tmp_path: Path):
    path = tmp_path / "formula.xlsx"
    write_workbook(path, [["sku", "title"], ["A", "=1+1"]])
    with pytest.raises(ValueError, match="xlsx_formula_not_allowed"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())


def test_xlsx_rejects_merged_cells(tmp_path: Path):
    path = tmp_path / "merged.xlsx"
    workbook = Workbook()
    worksheet = workbook.active
    worksheet.append(["sku", "title"])
    worksheet.append(["A", "merged"])
    worksheet.merge_cells("A2:B2")
    workbook.save(path)
    workbook.close()
    with pytest.raises(ValueError, match="xlsx_merged_cell"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())


def test_xlsx_rejects_hidden_worksheets(tmp_path: Path):
    path = tmp_path / "hidden.xlsx"
    workbook = Workbook()
    workbook.active.append(["sku", "title"])
    hidden = workbook.create_sheet("hidden")
    hidden.append(["A", "hidden"])
    hidden.sheet_state = "hidden"
    workbook.save(path)
    workbook.close()
    with pytest.raises(ValueError, match="xlsx_hidden_sheet"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())


def test_xlsx_rejects_macros(tmp_path: Path):
    path = tmp_path / "macro.xlsx"
    write_workbook(path, [["sku", "title"], ["A", "商品"]])
    with ZipFile(path, "a") as archive:
        archive.writestr("xl/vbaProject.bin", b"not-a-real-macro")
    with pytest.raises(ValueError, match="xlsx_macro_not_allowed"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())


def test_xlsx_rejects_row_and_column_limits(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    path = tmp_path / "limits.xlsx"
    write_workbook(path, [["sku", "title"], ["A", "a"], ["B", "b"], ["C", "c"]])
    monkeypatch.setattr(pipeline, "MAX_XLSX_ROWS", 2)
    with pytest.raises(ValueError, match="xlsx_row_limit"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())
    monkeypatch.setattr(pipeline, "MAX_XLSX_ROWS", 100)
    monkeypatch.setattr(pipeline, "MAX_XLSX_COLUMNS", 1)
    with pytest.raises(ValueError, match="xlsx_column_limit"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())


def test_xlsx_rejects_empty_sheet(tmp_path: Path):
    path = tmp_path / "empty.xlsx"
    write_workbook(path, [])
    with pytest.raises(ValueError, match="xlsx_empty_sheet"):
        XLSXParser().parse(path, document_id="doc", version_id="v1", metadata=metadata())
