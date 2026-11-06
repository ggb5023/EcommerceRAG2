"""The committed commerce-lab-v1 dataset is reproducible and internally sound."""
from __future__ import annotations

import csv
import hashlib
import io
import json
import re
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / "data" / "synthetic" / "commerce-lab-v1"


def read_csv(name: str) -> list[dict[str, str]]:
    text = (PACKAGE / name).read_text(encoding="utf-8")
    return list(csv.DictReader(io.StringIO(text)))


def test_manifest_hashes_and_rows_match_every_file():
    manifest = yaml.safe_load((PACKAGE / "manifest.yaml").read_text(encoding="utf-8"))
    assert manifest["dataset_id"] == "commerce-lab-v1"
    assert manifest["shop_id"] == "shop-demo"
    assert manifest["tenant_id"] == "v2-demo-tenant"
    listed = {entry["path"]: entry for entry in manifest["files"]}
    actual = {path.relative_to(PACKAGE).as_posix() for path in PACKAGE.rglob("*") if path.is_file()}
    assert set(listed) == actual - {"manifest.yaml"}
    for name, entry in listed.items():
        data = (PACKAGE / name).read_bytes()
        assert hashlib.sha256(data).hexdigest() == entry["sha256"], name
        if name.endswith(".csv"):
            assert len(data.decode().splitlines()) - 1 == entry["rows"], name
        if name.endswith(".jsonl"):
            assert len(data.decode().splitlines()) == entry["rows"], name


def test_references_between_orders_items_shipments_returns_and_tickets():
    products = {row["sku"] for row in read_csv("products.csv")}
    inventory = {row["sku"] for row in read_csv("inventory.csv")}
    orders = {row["order_id"]: row for row in read_csv("orders.csv")}
    items = read_csv("order-items.csv")
    shipments = read_csv("shipments.csv")
    events = read_csv("shipment-events.csv")
    returns = read_csv("returns.csv")
    tickets = read_csv("tickets.csv")
    assert inventory == products
    for item in items:
        assert item["sku"] in products
        assert item["order_id"] in orders
    shipment_ids = {row["shipment_id"] for row in shipments}
    for shipment in shipments:
        assert shipment["order_id"] in orders
        assert orders[shipment["order_id"]]["status"] in {"shipped", "delivered"}
    for event in events:
        assert event["shipment_id"] in shipment_ids
    for row in returns:
        assert row["order_id"] in orders
        assert orders[row["order_id"]]["status"] == "delivered"
        assert row["sku"] in products
    for row in tickets:
        assert row["order_id"] in orders
    charts = {(row["chart_code"], row["size_label"]) for row in read_csv("size-chart.csv")}
    for row in read_csv("products.csv"):
        if row["size_chart_code"]:
            assert (row["size_chart_code"], row["size_label"]) in charts


def test_care_codes_and_questions_point_at_existing_content():
    products = read_csv("products.csv")
    care = (PACKAGE / "knowledge" / "care.md").read_text(encoding="utf-8")
    for code in {row["care_code"] for row in products}:
        assert f"`{code}`" in care
    orders = {row["order_id"] for row in read_csv("orders.csv")}
    allowed_tools = {"get_stock", "get_price", "get_order_status", "get_logistics", "compare_products"}
    questions = [json.loads(line) for line in
                 (PACKAGE / "questions.jsonl").read_text(encoding="utf-8").splitlines() if line]
    assert questions
    for question in questions:
        assert set(question["expected_tools"]) <= allowed_tools
        if question["service_type"] == "pre_sales":
            assert question["order_id"] == ""
        else:
            assert question["service_type"] == "after_sales"
            assert question["order_id"] in orders
            ids = set(re.findall(r"ORD-2609-\d{4}", question["query"]))
            assert ids <= {question["order_id"]}, question["case_id"]


def test_dataset_has_no_personal_data_and_no_money_summaries():
    text = "\n".join(path.read_text(encoding="utf-8") for path in PACKAGE.rglob("*") if path.is_file())
    assert not re.search(r"1[3-9]\d{9}", text), "mobile number"
    assert not re.search(r"[\w.+-]+@[\w-]+\.[\w.]+", text), "email address"
    assert not re.search(r"\b\d{17}[\dXx]\b", text), "national id"
    forbidden = {"total", "total_cents", "refund", "refund_amount", "amount", "amount_cents",
                 "subtotal", "grand_total"}
    for name in ("orders.csv", "order-items.csv", "shipments.csv", "returns.csv", "tickets.csv"):
        header = set(read_csv(name)[0])
        assert not header & forbidden, (name, header & forbidden)
    assert not re.search(r"(退款金额|订单总额|合计金额)", text)
