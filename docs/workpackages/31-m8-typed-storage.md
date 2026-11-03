# 31 M8 阶段 2（一）：XLSX 解析与类型化商品存储

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0034](../decisions/0034-product-table-import.md) §2.1–2.4、
> [0017](../decisions/0017-m6-l1-import-precheck.md)、
> [data-ownership](../data-ownership.md)、[environment-facts](../environment-facts.md)

## 目标

按 0034 §2.1–2.4 实现 XLSX 解析、商品表/尺码表的 manifest 类型、chunk 投影字段、
迁移 0049 与类型化写入、发布校验。预检/模板/逐行错误（WP32）、检索过滤（WP33）、
facts 接线不在本轮。

## 实施顺序

1. 工作包与 0034 状态：0034 提议改为已采纳；INDEX/CURRENT 同步。
2. openpyxl 依赖：`read_only` 模式，记录 MIT 许可；XLSX 解析器只读第一个工作表。
3. manifest 与投影：`doc_type`/`table_schema` 校验并落 `document.doc_type`；chunk
   `meta_json` 增加 `table_schema`、`catalog_row_key`、`sku_code`/`chart_code`、`source_row`。
4. 迁移与写入：先登记 data-ownership，再写 0049 两张表；ingest 在同一事务写 chunk
   与类型化行；发布时校验 active 商品表唯一性和尺码引用。

## 口径（照 0034，不自创）

- 商品表：`catalog_sku`，一行一个 SKU；`sku_code` 必填且同版本内大小写不敏感唯一；
  `title` 必填；`size_chart_code` 引用尺码表；价格快照必须有 `price_observed_at`。
- 尺码表：`catalog_size_chart`，`(tenant_id,shop_id,version_id,chart_code,size_label)`
  唯一；`size_system` 五枚举；测量值为已登记键且非负。
- 已登记数值属性：`capacity_ml`、`length_cm`、`width_cm`、`height_cm`、`weight_g`；
  文本属性：`material`、`pattern`；未知属性键保留并 warning，不参与过滤。
- 商品行引用不存在/未 active 的尺码表或尺码：预检 warning，发布时仍缺则拒绝。
- 商品表写入方仍是 Python `ingest`；Go 只读；发布/审核/回滚/撤回复用现有链路。

## XLSX 边界

- 只读第一个工作表；公式、宏、合并单元格、隐藏工作表、超限行列必须明确报错，
  不执行、不静默跳过。
- CSV 与 XLSX 共用同一行规范化和 typed-row 构造；解析结果必须是同一 `ParsedElement` 形状。
- 旧 manifest 无 `doc_type` 保持原行为；只有 `product_table`/`size_chart` 进入类型化写入。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：from-zero 含 0049 PASS；dev/test-integration/eval 三库应用 0049 后逐文件 sha256
一致；重启 ai 后进程启动时间晚于最后提交；用合成 `products.csv` 导入一个
`product_table` 版本，SQL 断言 `catalog_sku` 行数、chunk `meta_json` 字段与唯一约束；
data-ownership 与 migration-grants 守卫通过。

## 不做

- 不做预检/模板/逐行错误（WP32）；不做检索过滤（WP33）；不改 facts provider。
- 不自动发布/下架；不调棘轮/基线；不重采 smoke；不修改已执行迁移。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（实现与验证完成后填写；未完成项必须在汇报第 4 节点名。）

## 未覆盖路径

（实现与验证完成后填写。）
