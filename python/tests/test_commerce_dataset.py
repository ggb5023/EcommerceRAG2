"""The committed commerce-lab-v1 dataset is reproducible and internally sound."""
from __future__ import annotations

import csv
import hashlib
import io
import json
import re
from datetime import UTC, datetime
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
    skus_by_order: dict[str, set[str]] = {}
    for item in items:
        assert item["sku"] in products
        assert item["order_id"] in orders
        skus_by_order.setdefault(item["order_id"], set()).add(item["sku"])
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
        assert row["sku"] in skus_by_order[row["order_id"]], row["return_id"]
    for row in tickets:
        assert row["order_id"] in orders
    charts = {(row["chart_code"], row["size_label"]) for row in read_csv("size-chart.csv")}
    for row in read_csv("products.csv"):
        if row["size_chart_code"]:
            assert (row["size_chart_code"], row["size_label"]) in charts


def parse_timestamp(value: str) -> datetime:
    return datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=UTC)


def test_timestamps_follow_the_order_lifecycle():
    orders = {row["order_id"]: row for row in read_csv("orders.csv")}
    shipments = read_csv("shipments.csv")
    events = read_csv("shipment-events.csv")
    returns = read_csv("returns.csv")
    tickets = read_csv("tickets.csv")
    shipments_by_order = {row["order_id"]: row for row in shipments}
    shipments_by_id = {row["shipment_id"]: row for row in shipments}
    reference = datetime(2026, 9, 29, 23, 59, 59, tzinfo=UTC)

    for order in orders.values():
        placed = parse_timestamp(order["placed_at"])
        updated = parse_timestamp(order["updated_at"])
        latest = placed
        shipment = shipments_by_order.get(order["order_id"])
        if shipment:
            latest = max(latest, parse_timestamp(shipment["shipped_at"]))
            if shipment["delivered_at"]:
                latest = max(latest, parse_timestamp(shipment["delivered_at"]))
        assert updated >= latest, order["order_id"]
        assert updated <= reference

    for shipment in shipments:
        placed = parse_timestamp(orders[shipment["order_id"]]["placed_at"])
        shipped = parse_timestamp(shipment["shipped_at"])
        assert shipped >= placed, shipment["shipment_id"]
        assert shipped <= reference
        if shipment["delivered_at"]:
            assert parse_timestamp(shipment["delivered_at"]) >= shipped

    events_by_shipment: dict[str, list[dict[str, str]]] = {}
    for event in events:
        events_by_shipment.setdefault(event["shipment_id"], []).append(event)
    for shipment_id, shipment_events in events_by_shipment.items():
        shipment = shipments_by_id[shipment_id]
        shipped = parse_timestamp(shipment["shipped_at"])
        delivered = parse_timestamp(shipment["delivered_at"]) if shipment["delivered_at"] else None
        previous = shipped
        for event in sorted(shipment_events, key=lambda row: int(row["seq"])):
            moment = parse_timestamp(event["event_at"])
            assert moment >= previous, event["event_id"]
            if delivered is not None:
                assert moment <= delivered, event["event_id"]
            assert moment <= reference
            previous = moment

    for row in returns:
        placed = parse_timestamp(orders[row["order_id"]]["placed_at"])
        delivered_at = shipments_by_order[row["order_id"]]["delivered_at"]
        assert delivered_at, row["return_id"]
        requested = parse_timestamp(row["requested_at"])
        assert requested >= parse_timestamp(delivered_at), row["return_id"]
        assert requested >= placed
        if row["resolved_at"]:
            assert parse_timestamp(row["resolved_at"]) >= requested
        assert requested <= reference

    for row in tickets:
        placed = parse_timestamp(orders[row["order_id"]]["placed_at"])
        created = parse_timestamp(row["created_at"])
        assert created >= placed, row["ticket_id"]
        if row["closed_at"]:
            assert parse_timestamp(row["closed_at"]) >= created
        assert created <= reference


def test_care_codes_and_questions_point_at_existing_content():
    products = read_csv("products.csv")
    product_skus = {row["sku"] for row in products}
    skus_by_order: dict[str, set[str]] = {}
    for item in read_csv("order-items.csv"):
        skus_by_order.setdefault(item["order_id"], set()).add(item["sku"])
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
            if set(question["expected_tools"]) & {"get_stock", "get_price"}:
                found = re.findall(r"\bCLB-[A-Z]{3}-\d{3}\b", question["query"])
                assert len(found) == 1, question["case_id"]
                assert found[0] in product_skus, question["case_id"]
                assert found[0] in skus_by_order[question["order_id"]], question["case_id"]


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
