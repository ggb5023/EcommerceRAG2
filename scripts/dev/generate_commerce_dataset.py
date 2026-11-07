#!/usr/bin/env python3
"""Generate the reproducible commerce-lab-v1 dataset (no network, no PII).

The dataset feeds the local commerce simulator (facts contract + dev REST) and
the knowledge package imported into the demo shop. Every file is deterministic
for the fixed seed and reference date; `--check` regenerates in memory and
compares bytes with the committed package.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import io
import json
import random
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "data" / "synthetic" / "commerce-lab-v1"
SEED = 20260929
REFERENCE_DATE = "2026-09-29"
TENANT_ID = "v2-demo-tenant"
SHOP_ID = "shop-demo"
CURRENCY = "CNY"

PRODUCTS = [
    # sku, title, category, color, size_label, size_chart_code, material, care_code, price_cents, attributes
    ("CLB-DRK-001", "晨雾保温杯 350ml", "drinkware", "雾蓝", "", "", "304不锈钢", "CUP-STEEL", 6900, {"capacity_ml": 350, "weight_g": 260}),
    ("CLB-DRK-002", "晨雾保温杯 480ml", "drinkware", "雾蓝", "", "", "304不锈钢", "CUP-STEEL", 8900, {"capacity_ml": 480, "weight_g": 300}),
    ("CLB-DRK-003", "晨雾保温杯 750ml", "drinkware", "墨绿", "", "", "304不锈钢", "CUP-STEEL", 10900, {"capacity_ml": 750, "weight_g": 420}),
    ("CLB-DRK-004", "轻氧随行杯 420ml", "drinkware", "奶油白", "", "", "Tritan", "CUP-TRITAN", 5900, {"capacity_ml": 420, "weight_g": 180}),
    ("CLB-DRK-005", "轻氧随行杯 600ml", "drinkware", "雾紫", "", "", "Tritan", "CUP-TRITAN", 6900, {"capacity_ml": 600, "weight_g": 220}),
    ("CLB-DRK-006", "焖烧罐 500ml", "drinkware", "石墨灰", "", "", "316不锈钢", "CUP-STEEL", 12900, {"capacity_ml": 500, "weight_g": 460}),
    ("CLB-DRK-007", "陶瓷内胆咖啡杯 380ml", "drinkware", "燕麦色", "", "", "陶瓷+304不锈钢", "CUP-CERAMIC", 9900, {"capacity_ml": 380, "weight_g": 340}),
    ("CLB-DRK-008", "折叠运动水壶 650ml", "drinkware", "海盐蓝", "", "", "食品级硅胶", "CUP-SILICONE", 4900, {"capacity_ml": 650, "weight_g": 150}),
    ("CLB-BAG-001", "城市通勤双肩包 20L", "bags", "炭黑", "", "", "再生尼龙", "BAG-NYLON", 26900, {"capacity_l": 20, "weight_g": 780}),
    ("CLB-BAG-002", "城市通勤双肩包 26L", "bags", "岩灰", "", "", "再生尼龙", "BAG-NYLON", 29900, {"capacity_l": 26, "weight_g": 880}),
    ("CLB-BAG-003", "轻量斜挎包 4L", "bags", "沙色", "", "", "防泼水涤纶", "BAG-POLYESTER", 12900, {"capacity_l": 4, "weight_g": 260}),
    ("CLB-BAG-004", "周末旅行袋 32L", "bags", "深海军蓝", "", "", "防泼水涤纶", "BAG-POLYESTER", 33900, {"capacity_l": 32, "weight_g": 950}),
    ("CLB-BAG-005", "通勤托特包 12L", "bags", "焦糖棕", "", "", "植鞣革", "BAG-LEATHER", 39900, {"capacity_l": 12, "weight_g": 820}),
    ("CLB-BAG-006", "运动腰包 2L", "bags", "荧光橙", "", "", "弹力锦纶", "BAG-NYLON", 8900, {"capacity_l": 2, "weight_g": 180}),
    ("CLB-BAG-007", "电脑内胆包 14 英寸", "bags", "深灰", "", "", "记忆海绵+尼龙", "BAG-NYLON", 9900, {"fit_inch": 14, "weight_g": 240}),
    ("CLB-BAG-008", "旅行收纳袋三件套", "bags", "浅灰", "", "", "防泼水涤纶", "BAG-POLYESTER", 7900, {"pieces": 3, "weight_g": 210}),
    ("CLB-BED-001", "全棉四件套 1.5m 床", "bedding", "暖白", "1.5m", "CLB-BEDDING-SIZES", "100%新疆长绒棉", "BED-COTTON", 45900, {"thread_count": 60, "pieces": 4}),
    ("CLB-BED-002", "全棉四件套 1.8m 床", "bedding", "暖白", "1.8m", "CLB-BEDDING-SIZES", "100%新疆长绒棉", "BED-COTTON", 49900, {"thread_count": 60, "pieces": 4}),
    ("CLB-BED-003", "天丝夏被 200x230cm", "bedding", "雾灰", "200x230", "CLB-BEDDING-SIZES", "莱赛尔天丝", "BED-TENCEL", 39900, {"fill_g": 300, "pieces": 1}),
    ("CLB-BED-004", "水洗棉夏被 150x200cm", "bedding", "米白", "150x200", "CLB-BEDDING-SIZES", "水洗棉", "BED-COTTON", 25900, {"fill_g": 250, "pieces": 1}),
    ("CLB-BED-005", "记忆棉枕 中高", "bedding", "本白", "中高", "CLB-BEDDING-SIZES", "记忆棉", "BED-FOAM", 19900, {"height_cm": 12, "pieces": 1}),
    ("CLB-BED-006", "乳胶枕 波浪形", "bedding", "本白", "波浪", "CLB-BEDDING-SIZES", "天然乳胶", "BED-LATEX", 29900, {"height_cm": 10, "pieces": 1}),
    ("CLB-BED-007", "全棉床笠 1.5m 床", "bedding", "浅灰", "1.5m", "CLB-BEDDING-SIZES", "100%新疆长绒棉", "BED-COTTON", 15900, {"thread_count": 60, "pieces": 1}),
    ("CLB-BED-008", "天丝枕套一对", "bedding", "银灰", "48x74", "CLB-BEDDING-SIZES", "莱赛尔天丝", "BED-TENCEL", 12900, {"pieces": 2}),
    ("CLB-APP-001", "轻量防晒衣 雾蓝 S", "apparel", "雾蓝", "S", "CLB-APPAREL-SIZES", "锦纶+氨纶", "APP-SYNTHETIC", 12900, {"upf": 50, "weight_g": 180}),
    ("CLB-APP-002", "轻量防晒衣 雾蓝 M", "apparel", "雾蓝", "M", "CLB-APPAREL-SIZES", "锦纶+氨纶", "APP-SYNTHETIC", 12900, {"upf": 50, "weight_g": 190}),
    ("CLB-APP-003", "轻量防晒衣 炭黑 L", "apparel", "炭黑", "L", "CLB-APPAREL-SIZES", "锦纶+氨纶", "APP-SYNTHETIC", 12900, {"upf": 50, "weight_g": 205}),
    ("CLB-APP-004", "速干短袖 T 恤 暖白 M", "apparel", "暖白", "M", "CLB-APPAREL-SIZES", "再生涤纶", "APP-SYNTHETIC", 7900, {"weight_g": 120}),
    ("CLB-APP-005", "速干短袖 T 恤 炭黑 L", "apparel", "炭黑", "L", "CLB-APPAREL-SIZES", "再生涤纶", "APP-SYNTHETIC", 7900, {"weight_g": 130}),
    ("CLB-APP-006", "运动长裤 深灰 M", "apparel", "深灰", "M", "CLB-APPAREL-SIZES", "锦纶+氨纶", "APP-SYNTHETIC", 19900, {"weight_g": 260}),
    ("CLB-APP-007", "运动长裤 深灰 L", "apparel", "深灰", "L", "CLB-APPAREL-SIZES", "锦纶+氨纶", "APP-SYNTHETIC", 19900, {"weight_g": 275}),
    ("CLB-APP-008", "轻量风衣 海盐蓝 M", "apparel", "海盐蓝", "M", "CLB-APPAREL-SIZES", "防泼水锦纶", "APP-SYNTHETIC", 25900, {"weight_g": 320}),
]

SIZE_CHART = [
    # chart_code, size_label, size_system, chest_cm, waist_cm, hip_cm, height_cm, fit_note
    ("CLB-APPAREL-SIZES", "S", "unisex", 88, 70, 92, 160, "常规版型"),
    ("CLB-APPAREL-SIZES", "M", "unisex", 94, 76, 98, 170, "常规版型"),
    ("CLB-APPAREL-SIZES", "L", "unisex", 100, 82, 104, 178, "常规版型"),
    ("CLB-APPAREL-SIZES", "XL", "unisex", 106, 88, 110, 183, "常规版型"),
    ("CLB-BEDDING-SIZES", "1.5m", "bedding", "", "", "", 200, "适配 150x200cm 床垫"),
    ("CLB-BEDDING-SIZES", "1.8m", "bedding", "", "", "", 200, "适配 180x200cm 床垫"),
    ("CLB-BEDDING-SIZES", "150x200", "bedding", "", "", "", 200, "夏被常规尺寸"),
    ("CLB-BEDDING-SIZES", "200x230", "bedding", "", "", "", 230, "双人夏被尺寸"),
    ("CLB-BEDDING-SIZES", "中高", "pillow", "", "", "", 12, "侧睡支撑"),
    ("CLB-BEDDING-SIZES", "波浪", "pillow", "", "", "", 10, "颈椎贴合"),
    ("CLB-BEDDING-SIZES", "48x74", "pillow", "", "", "", 74, "标准枕套尺寸"),
]

RETURN_REASONS = ["size_mismatch", "damaged", "not_as_described"]

KNOWLEDGE_DOCUMENTS = [
    ("commerce-returns", "退换政策", "knowledge/returns.md", "policy"),
    ("commerce-shipping", "发货与物流", "knowledge/shipping.md", "policy"),
    ("commerce-faq", "常见问题", "knowledge/faq.md", "faq"),
    ("commerce-care", "护理说明", "knowledge/care.md", "policy"),
    ("commerce-size-guide", "尺码指南", "knowledge/size-guide.md", "policy"),
]


def csv_text(header: list[str], rows: list[list[object]]) -> str:
    buffer = io.StringIO()
    writer = csv.writer(buffer, lineterminator="\n")
    writer.writerow(header)
    writer.writerows(rows)
    return buffer.getvalue()


def timestamp(day: int, hour: int, minute: int = 0) -> str:
    return datetime(2026, 9, day, hour, minute, tzinfo=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def build(rng: random.Random) -> dict[str, str]:
    files: dict[str, str] = {}
    skus = [row[0] for row in PRODUCTS]

    products_rows = []
    for sku, title, category, color, size, chart, material, care, price, attributes in PRODUCTS:
        products_rows.append([
            sku, title, category, color, size, chart, material, care, price, CURRENCY,
            json.dumps(attributes, ensure_ascii=False, sort_keys=True, separators=(",", ":")),
            "2026-06-01",
        ])
    files["products.csv"] = csv_text(
        ["sku", "title", "category", "color", "size_label", "size_chart_code", "material",
         "care_code", "price_cents", "currency", "attributes_json", "launched_on"],
        products_rows,
    )

    files["size-chart.csv"] = csv_text(
        ["chart_code", "size_label", "size_system", "chest_cm", "waist_cm", "hip_cm",
         "height_cm", "fit_note"],
        [list(row) for row in SIZE_CHART],
    )

    inventory_rows = []
    for index, sku in enumerate(skus):
        on_hand = 0 if index in {6, 17, 26} else rng.randint(3, 120)
        reserved = 0 if on_hand == 0 else rng.randint(0, min(5, on_hand))
        inventory_rows.append([sku, on_hand, reserved, "BJ-01" if index % 2 == 0 else "SH-02",
                               timestamp(29, rng.randint(0, 6))])
    files["inventory.csv"] = csv_text(
        ["sku", "on_hand", "reserved", "warehouse", "updated_at"], inventory_rows)

    statuses = ["paid"] * 4 + ["shipped"] * 6 + ["delivered"] * 12 + ["cancelled"] * 2
    rng.shuffle(statuses)
    order_rows: list[list[object]] = []
    item_rows: list[list[object]] = []
    shipment_rows: list[list[object]] = []
    event_rows: list[list[object]] = []
    return_rows: list[list[object]] = []
    delivered_orders: list[str] = []
    shipment_seq = 0
    event_seq = 0
    for index, status in enumerate(statuses, start=1):
        order_id = f"ORD-2609-{index:04d}"
        placed_day = rng.randint(1, 24)
        placed = timestamp(placed_day, rng.randint(1, 12))
        updated = timestamp(min(placed_day + rng.randint(0, 4), 28), rng.randint(13, 23))
        order_rows.append([order_id, SHOP_ID, status, f"CUST-{index:04d}", placed, updated, CURRENCY])
        item_count = rng.randint(1, 3)
        chosen = rng.sample(skus, item_count)
        for line, sku in enumerate(chosen, start=1):
            item_rows.append([order_id, line, sku, rng.randint(1, 2)])
        if status in {"shipped", "delivered"}:
            shipment_seq += 1
            shipment_id = f"SHP-2609-{shipment_seq:04d}"
            carrier = "CLB-EXPRESS" if index % 2 else "NORTHLINE"
            shipped = timestamp(min(placed_day + 1, 27), rng.randint(8, 18))
            delivered = timestamp(min(placed_day + 3, 29), rng.randint(9, 20)) if status == "delivered" else ""
            shipment_rows.append([shipment_id, order_id, carrier,
                                  f"TRK-2609-{shipment_seq:05d}",
                                  "delivered" if status == "delivered" else "in_transit",
                                  shipped, delivered])
            steps = ["picked_up", "in_transit", "out_for_delivery"]
            if status == "delivered":
                steps.append("delivered")
            for step_number, step in enumerate(steps, start=1):
                event_seq += 1
                event_rows.append([f"EVT-2609-{event_seq:05d}", shipment_id, step_number,
                                   timestamp(min(placed_day + 1 + step_number // 2, 29),
                                             min(8 + step_number * 2, 22)),
                                   rng.choice(["北京分拨中心", "上海分拨中心", "杭州转运中心", "末端站点"]),
                                   step])
            if status == "delivered":
                delivered_orders.append(order_id)
    files["orders.csv"] = csv_text(
        ["order_id", "shop_id", "status", "customer_ref", "placed_at", "updated_at", "currency"],
        order_rows)
    files["order-items.csv"] = csv_text(
        ["order_id", "line_no", "sku", "quantity"], item_rows)
    files["shipments.csv"] = csv_text(
        ["shipment_id", "order_id", "carrier", "tracking_no", "status", "shipped_at", "delivered_at"],
        shipment_rows)
    files["shipment-events.csv"] = csv_text(
        ["event_id", "shipment_id", "seq", "event_at", "location", "status"], event_rows)

    return_statuses = ["requested", "approved", "received", "closed"]
    for index, order_id in enumerate(delivered_orders[:6], start=1):
        status = return_statuses[(index - 1) % len(return_statuses)]
        requested = timestamp(rng.randint(20, 27), rng.randint(9, 17))
        resolved = timestamp(28, rng.randint(10, 18)) if status == "closed" else ""
        return_rows.append([f"RET-2609-{index:04d}", order_id, rng.choice(skus),
                            RETURN_REASONS[(index - 1) % len(RETURN_REASONS)], status,
                            requested, resolved])
    files["returns.csv"] = csv_text(
        ["return_id", "order_id", "sku", "reason_code", "status", "requested_at", "resolved_at"],
        return_rows)

    ticket_rows = []
    for index in range(1, 9):
        order = order_rows[(index * 3) % len(order_rows)][0]
        closed = index % 3 == 0
        created = timestamp(rng.randint(5, 26), rng.randint(9, 18))
        ticket_rows.append([f"SIM-TKT-{index:04d}", order,
                            RETURN_REASONS[index % len(RETURN_REASONS)],
                            "closed" if closed else "open", created,
                            timestamp(28, rng.randint(9, 18)) if closed else ""])
    files["tickets.csv"] = csv_text(
        ["ticket_id", "order_id", "reason_code", "status", "created_at", "closed_at"],
        ticket_rows)

    files["knowledge/returns.md"] = (
        "# 退换政策\n\n"
        "本店商品支持签收后 7 天内无理由退换，商品需保持未使用、吊牌完整、包装齐全。\n\n"
        "定制商品、贴身衣物与已拆封的个人护理类商品不支持无理由退换。\n\n"
        "质量问题请在签收后 15 天内联系售后，并提供订单号与问题照片；"
        "客服核实后安排换货或退货。\n\n"
        "退款与换货的最终处理由人工客服完成，智能客服只提供查询与引导。\n"
    )
    files["knowledge/shipping.md"] = (
        "# 发货与物流\n\n"
        "现货商品在付款后 48 小时内发出，节假日顺延。\n\n"
        "默认使用 CLB-EXPRESS 与 NORTHLINE 承运，偏远地区可能转交合作快递。\n\n"
        "订单发货后可在「我的订单」查看运单号与物流轨迹；"
        "物流信息由承运商回传，通常有 2 到 6 小时的延迟。\n\n"
        "订单状态含义：paid 为已付款待发货，shipped 为已发货，"
        "delivered 为已签收，cancelled 为已取消。\n"
    )
    files["knowledge/faq.md"] = (
        "# 常见问题\n\n"
        "## 可以修改收货地址吗？\n\n"
        "发货前可以联系人工客服修改；发货后只能由承运商中转，"
        "智能客服不能修改地址。\n\n"
        "## 可以取消订单吗？\n\n"
        "未发货订单可以申请取消，由人工客服确认；已发货订单需要走退货流程。\n\n"
        "## 尺码不合适怎么办？\n\n"
        "在签收后 7 天内申请换货，保持吊牌完整；可先对照尺码表确认。\n\n"
        "## 发票怎么开？\n\n"
        "下单后 30 天内可联系人工客服补开电子发票。\n"
    )
    files["knowledge/care.md"] = (
        "# 护理说明\n\n"
        "`CUP-STEEL`：304/316 不锈钢保温杯，手洗，避免长时间盛放碳酸饮料。\n\n"
        "`CUP-TRITAN`：Tritan 随行杯，可机洗上限 60 摄氏度，不可微波加热。\n\n"
        "`CUP-CERAMIC`：陶瓷内胆咖啡杯，手洗，避免骤冷骤热。\n\n"
        "`CUP-SILICONE`：食品级硅胶水壶，可沸水消毒，晾干后收纳。\n\n"
        "`BAG-NYLON` / `BAG-POLYESTER`：防泼水包袋，湿布擦拭，阴干，不可漂白。\n\n"
        "`BAG-LEATHER`：植鞣革托特包，避免长时间淋雨，定期使用皮革护理膏。\n\n"
        "`BED-COTTON`：全棉床品，30 摄氏度以下机洗，深浅色分开。\n\n"
        "`BED-TENCEL`：天丝床品，轻柔模式机洗，不可高温烘干。\n\n"
        "`BED-FOAM` / `BED-LATEX`：枕芯不可水洗，通风晾晒，避免暴晒。\n\n"
        "`APP-SYNTHETIC`：合成纤维运动服饰，冷水机洗，不可熨烫印花部位。\n"
    )
    files["knowledge/size-guide.md"] = (
        "# 尺码指南\n\n"
        "运动服饰按胸围选择：S 约 88cm，M 约 94cm，L 约 100cm，XL 约 106cm，"
        "版型为常规版型。\n\n"
        "床品按床垫尺寸选择：1.5m 床适配 150x200cm 床垫，1.8m 床适配 180x200cm 床垫。\n\n"
        "枕芯高度：中高适合侧睡，波浪形适合颈椎需要贴合的人群。\n\n"
        "如仍不确定，请提供身高体重与使用场景，售前客服可协助推荐。\n"
    )

    questions = []
    pre_sales = [
        ("售前", "CLB-DRK-002 保温杯的容量和材质是什么？", [], "products.csv"),
        ("售前", "防晒衣 M 码的胸围和身高范围是多少？", [], "size-chart.csv"),
        ("售前", "通勤双肩包 26L 可以放下 14 英寸电脑吗？", [], "products.csv"),
        ("售前", "天丝夏被可以机洗吗？", [], "care.md"),
        ("售前", "1.5m 床应该选哪个尺寸的四件套？", [], "size-guide.md"),
        ("售前", "你们的退换政策是怎样的？", [], "returns.md"),
        ("售前", "现货一般多久发货？", [], "shipping.md"),
        ("售前", "记忆棉枕和乳胶枕有什么区别？", [], "products.csv"),
        ("售前", "保温杯可以装碳酸饮料吗？", [], "care.md"),
        ("售前", "可以开发票吗？", [], "faq.md"),
    ]
    for index, (lane, query, tools, document) in enumerate(pre_sales, start=1):
        questions.append({"case_id": f"pre-{index:03d}", "service_type": "pre_sales",
                          "query": query, "order_id": "", "expected_tools": tools,
                          "expected_document": document})
    shipped_orders = [row[0] for row in order_rows if row[2] == "shipped"]
    delivered_orders_all = [row[0] for row in order_rows if row[2] == "delivered"]
    any_orders = [row[0] for row in order_rows]

    def pick(pool: list[str], offset: int) -> str:
        return pool[offset % len(pool)]

    after_sales = [
        ("订单 {order} 现在是什么状态？", "any", 0, ["get_order_status"]),
        ("帮我查一下 {order} 的物流到哪了？", "shipped", 0, ["get_logistics"]),
        ("订单 {order} 里的 CLB-DRK-001 还有库存吗？", "any", 1, ["get_stock"]),
        ("订单 {order} 的保温杯现在多少钱？", "any", 2, ["get_price"]),
        ("订单 {order} 显示已签收，但我没收到", "delivered", 0, ["get_order_status", "get_logistics"]),
        ("{order} 的包裹显示运输中，预计什么时候到？", "shipped", 1, ["get_logistics"]),
        ("我想退掉 {order} 里的商品，怎么操作？", "delivered", 1, []),
        ("{order} 的商品有破损，可以换货吗？", "delivered", 2, ["get_order_status"]),
        ("订单 {order} 可以修改收货地址吗？", "shipped", 2, ["get_order_status"]),
        ("{order} 可以取消吗？", "any", 3, ["get_order_status"]),
        ("订单 {order} 里的双肩包还有别的颜色吗？", "any", 4, ["get_stock"]),
        ("{order} 的物流为什么两天没更新？", "shipped", 3, ["get_logistics"]),
        ("订单 {order} 的枕芯可以水洗吗？", "any", 5, []),
        ("{order} 我想申请退款，需要什么材料？", "delivered", 3, ["get_order_status"]),
    ]
    pools = {"shipped": shipped_orders, "delivered": delivered_orders_all, "any": any_orders}
    for index, (template, pool_name, offset, tools) in enumerate(after_sales, start=11):
        order_id = pick(pools[pool_name], offset)
        questions.append({"case_id": f"after-{index:03d}", "service_type": "after_sales",
                          "query": template.format(order=order_id), "order_id": order_id,
                          "expected_tools": tools, "expected_document": ""})
    files["questions.jsonl"] = "".join(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n"
                                       for row in questions)

    files["README.md"] = (
        "# commerce-lab-v1\n\n"
        "拟真电商数据集，供本地 `commerce-sim`（facts 契约 + dev REST）与演示店铺使用。\n\n"
        "- 参考日期：2026-09-29；固定 seed，可复现。\n"
        "- 内容：32 个 SKU、库存、订单、订单行、物流与轨迹、退货、模拟工单、知识文档、示例问题。\n"
        "- 无真实个人信息；订单号与顾客编号均为合成值。\n"
        "- 订单/退货表不含金额汇总；价格仅作为 `get_price` 的单一事实来源。\n\n"
        "重新生成并校验：\n\n"
        "```bash\n"
        "python3 scripts/dev/generate_commerce_dataset.py --write\n"
        "python3 scripts/dev/generate_commerce_dataset.py --check\n"
        "```\n"
    )

    featured = ["CLB-DRK-002", "CLB-DRK-003"]
    document_entries = "".join(
        f"- document_id: {document_id}\n"
        f"  title: {title}\n"
        f"  path: {path}\n"
        "  format: markdown\n"
        f"  doc_type: {doc_type}\n"
        f"  tenant_id: {TENANT_ID}\n"
        f"  shop_id: {SHOP_ID}\n"
        "  disclosure_class: external_allowed\n"
        f"  effective_from: '{REFERENCE_DATE}'\n"
        "  effective_to: null\n"
        "  license: internal-generated\n"
        "  authorization:\n"
        "    reference: commerce-lab-v1\n"
        f"    checked_at: '{REFERENCE_DATE}'\n"
        f"  sha256: {hashlib.sha256(files[path].encode()).hexdigest()}\n"
        for document_id, title, path, doc_type in KNOWLEDGE_DOCUMENTS
    )
    files["manifest.yaml"] = (
        "schema_version: 1\n"
        "dataset_id: commerce-lab-v1\n"
        "generated_by: scripts/dev/generate_commerce_dataset.py\n"
        "source:\n"
        "  id: commerce-lab-v1\n"
        "  name: Commerce lab v1\n"
        "pipeline_version: commerce-lab-v1\n"
        f"business_date: '{REFERENCE_DATE}'\n"
        f"seed: {SEED}\n"
        f"reference_date: '{REFERENCE_DATE}'\n"
        "facts_version: v1\n"
        f"tenant_id: {TENANT_ID}\n"
        f"shop_id: {SHOP_ID}\n"
        f"currency: {CURRENCY}\n"
        "synthetic: true\n"
        "license: internal-generated\n"
        "tenants:\n"
        f"- tenant_id: {TENANT_ID}\n"
        "  shops:\n"
        f"  - {SHOP_ID}\n"
        "featured_comparison:\n"
        + "".join(f"- {sku}\n" for sku in featured)
        + "documents:\n"
        + document_entries
        + "files:\n"
        + "".join(
            f"- path: {name}\n  rows: {_row_count(name, text)}\n"
            f"  sha256: {hashlib.sha256(text.encode()).hexdigest()}\n"
            for name, text in sorted(files.items())
        )
        + "notes:\n"
        "- 本地生成，无外网抓取、无真实个人信息；customer_ref 为合成编号。\n"
        "- 订单与退货不含金额汇总；商品单价仅用于 get_price 事实查询。\n"
        "- 重新生成：python3 scripts/dev/generate_commerce_dataset.py --write\n"
    )
    return files


def _row_count(name: str, text: str) -> int:
    if name == "manifest.yaml":
        return 0
    if name.endswith(".jsonl"):
        return len([line for line in text.splitlines() if line.strip()])
    if name.endswith(".csv"):
        return max(len(text.splitlines()) - 1, 0)
    return 0


def write_package(output: Path, files: dict[str, str]) -> None:
    for name in sorted(files):
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(files[name], encoding="utf-8", newline="")


def check_package(output: Path) -> list[str]:
    expected = build(random.Random(SEED))
    problems = []
    existing = {path.relative_to(output).as_posix() for path in output.rglob("*") if path.is_file()}
    for name, text in sorted(expected.items()):
        path = output / name
        if not path.is_file():
            problems.append(f"missing {name}")
        elif path.read_text(encoding="utf-8") != text:
            problems.append(f"stale {name}")
    for name in sorted(existing - set(expected)):
        problems.append(f"unexpected {name}")
    return problems


def generated_package(directory: Path) -> bool:
    """Only a package produced by this generator may be wiped by --write."""
    try:
        text = (directory / "manifest.yaml").read_text(encoding="utf-8")
    except OSError:
        return False
    return ("dataset_id: commerce-lab-v1" in text
            and "generated_by: scripts/dev/generate_commerce_dataset.py" in text)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="write the package")
    parser.add_argument("--check", action="store_true", help="verify the committed bytes")
    parser.add_argument("--force", action="store_true", help="allow --write to replace a non-generated directory")
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    if not args.write and not args.check:
        parser.error("choose --write or --check")
    if args.write and args.check:
        parser.error("choose exactly one of --write or --check")
    if args.check:
        problems = check_package(args.output)
        for problem in problems:
            print(f"FAIL dataset {problem}", file=sys.stderr)
        if problems:
            return 1
        print(f"PASS dataset {args.output.name}: deterministic bytes match")
        return 0
    output = args.output
    if output.exists() and any(output.iterdir()) and not args.force and not generated_package(output):
        print(f"FAIL {output} is not a commerce-lab-v1 package; pass --force to replace it", file=sys.stderr)
        return 2
    files = build(random.Random(SEED))
    if output.exists():
        for path in sorted(output.rglob("*"), reverse=True):
            if path.is_file():
                path.unlink()
            elif path.is_dir():
                path.rmdir()
    output.mkdir(parents=True, exist_ok=True)
    write_package(output, files)
    print(f"PASS wrote {len(files)} files to {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
