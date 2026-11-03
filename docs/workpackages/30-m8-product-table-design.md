# 30 M8 阶段 1：商品表导入调研与决策提议（不实现）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[ROADMAP](../../ROADMAP.md)、[product](../product.md)、
> [0017](../decisions/0017-m6-l1-import-precheck.md)、
> [0027](../decisions/0027-document-source-ownership.md)、
> [0032](../decisions/0032-intelligence-domain-and-crawling.md)、
> [0016](../decisions/0016-content-inspection.md)

## 目标

只做 M8 的调研与提议：查清现有 CSV 导入、0017 预检、facts 工具和检索过滤的现状，
确定商品表/尺码表的数据形态、过滤方式、预检错误行和 facts 边界。本轮不写实现代码、
不加迁移/表/列、不接线、不建模板文件、不做阶段 2。

## 现状盘点（先查再写）

### 1. CSV 已经能导入，但没有类型

- Go `parseManifest` 与 Python `package_validation` 都允许 `format: csv`；当前允许的格式是
  CSV、Markdown、DOCX，XLSX 不在其中；`parser_for("xlsx")` 直接 `KeyError`。
- `python/app/ingest/pipeline.py` 的 `CSVParser` 已处理 UTF-8 BOM、空行、空/重复表头、
  列数不一致（带物理行号），并按 `tenant_id`/`shop_id` 列过滤非本店行。
- 每一数据行变成一个 `table` 元素，`chunk_elements_v2` 保留行边界；实测合成
  `products.csv` 的本店行是 **7 个元素 / 7 个 chunk**，即当前“每行一个 chunk”。
- 缺点不是“不能读 CSV”，而是没有列语义、没有 SKU 唯一性、没有类型化数值；
  `document.doc_type` 目前由 Python 写成 `document["format"]`（例如 `csv`），manifest 里的
  业务 `doc_type` 没有被校验或持久化。

### 2. 0017 预检模型已部分落地

- 迁移 0032/0036 已建 `import_batch` 与 `import_item`：批次含 manifest、business_date、
  状态（staged/confirmed/revoked/expired）、24 小时过期与 confirm 租约；item 按**文档**保存
  source_hash、临时路径、建议披露级别/生效日、`precheck_result` 与 `precheck_detail` JSONB。
- 现有预检 R1–R4 是文档级规则（缺来源、过期、重复导入、缺披露依据），不检查商品表的
  列、行、SKU 重复或尺码映射。
- `precheck_detail` 是可扩展 JSONB，适合承载行级错误；但当前没有行级 schema、模板、
  预览错误表或“下载模板”入口。CSV 解析错误会中止整个文档，而不是收集为逐行问题。
- proto 中声明了 `ValidatePackage`，但 Python/Go 都没有实现或调用它；当前确认直接走
  现有 `persistParsed` → Submit → 摄取 worker → pending 版本，不改变单写者。

### 3. facts 工具不读商品表

- `go/internal/facts` 只有 Go 拥有的 `Provider`：`HTTPProvider` 调外部
  `GET /v1/facts/{tool}`，开发用 fake server 返回固定库存/价格；当前没有数据库或商品表实现。
- `get_stock`/`get_price` 必须绑定显式 SKU；Go 从理解结果/历史中解析 SKU，provider 回包
  必须与请求 SKU 一致，否则失败关闭。
- 合成 `products.csv` 已作为普通 chunk 入库；facts fake 的价格/库存是独立测试 double，
  不是从该 CSV 读取的商品事实（至少一个 SKU 的价格刻意与文档快照不同）。

### 4. 检索没有 SKU/属性过滤

- `python/app/retrieval/postgres.py` 当前按 tenant、allowed shops、allowed documents、
  active/approved 版本、披露级别、business_date 和 `content_type <> 'heading'` 过滤。
- 检索会返回 `meta_json`，但 SQL 没有按 `sku`、颜色、尺码或表类型过滤；本地索引只把
  `fact_type` 拼进可搜索文本。没有类型化商品行时，SKU/规格只能靠 chunk 文本匹配。
- 因此“精确 SKU/规格”与“向量找谁”混在同一条文本召回里；数值也以文本形式进入上下文，
  尚无 SQL 精确读取路径。

## 结论摘要

- 商品表继续保留每行 chunk，作为证据与引用投影；但新增结构化的 SKU 行与尺码行作为
  **过滤和数值的权威来源**。只靠 chunk 不足以做唯一性、属性过滤和逐行错误。
- 尺码表单独成类型，商品行通过 `size_chart_code` 引用；不把尺码矩阵塞回商品行。
- 模板与逐行错误放回 0017 的预检流程扩展，不另建上传/审核流程。
- 实时库存/价格仍走既有 facts provider；商品表不成为 `get_stock`/`get_price` 的数据源，
  只用于 SKU 识别、静态规格与（可选）目录价快照。
- XLSX 是阶段 2 的输入格式需求；解析库与列映射在决策 0034 中定，不在本轮引入依赖。

完整调研见 [product-table-import](../research/product-table-import.md)，数据形态与边界见
[0034](../decisions/0034-product-table-import.md)。

## 阶段 2 边界

- 只新增一份迁移（下一个可用编号），不改 0001–0048；新表写入方仍是 Python ingest。
- 复用 `document` / `document_version` / `chunk` / review / publish / rollback / revoke；
  admin 只读查询，不直接写商品表。
- 新增跨进程过滤字段时必须先登记 `docs/contracts.md`，Go 签发、Python 校验，两侧各一测试。
- 逐行错误只进入预检响应和 `import_item.precheck_detail`，不改发布状态。

## 不做

- 不实现商品表/尺码表；不建表、不加列、不建模板文件、不写 XLSX 解析器。
- 不做实时库存/价格对接、平台 API、自动补全 SKU 数据、自动发布/下架。
- 不把数值交给向量或模型猜；不改冻结评测集、棘轮、基线或已执行迁移。
- 不处理 OCR、图片尺码表、任意公式/宏、跨租户共享商品库。

## 验收命令

运行 `check.sh` 的文档守卫四项（docs-links、docs-drift、doc-paths、decisions-index），
再跑快速层 40 项。另需确认 0034 编号不撞车，工作包、0034、决策索引与调研文档互相链接。

## 实际交付

- 调研与不采用理由：[product-table-import](../research/product-table-import.md)，含 7 个候选、
  外部来源链接、国内电商导出列清单和解析库取舍；结论是不把商品表继续做成纯 chunk。
- 决策：[0034](../decisions/0034-product-table-import.md)，状态“提议”；含商品表/尺码表
  schema 草案、检索过滤、facts 边界、预检模板与备选方案。
- 实测：合成 `products.csv` 的本店行是 7 个元素/7 个 chunk；当前 `parser_for("xlsx")`
  抛 `KeyError`，`ALLOWED_FORMATS` 无 xlsx；`ValidatePackage` 只有 proto，无实现/调用。
- 文档四项守卫 PASS；快速层 **PASSED 40 / UNCONFIGURED 0 / FAILED 0**。
- 未改代码、迁移、表、列、模板或依赖；未引入 XLSX 解析器和新依赖。

## 未覆盖路径

- 阶段 2 的商品表/尺码表 schema、迁移、预检 RPC、模板、检索过滤、XLSX 解析均未实现。
- 未运行 `--db`、`--e2e`、`--all`；本轮没有数据库或浏览器行为。
- 未验证真实平台导出文件的列差异；模板和别名映射需在阶段 2 用真实样本校准。
- 未接实时库存/价格和平台 API；商品表不改变现有 facts provider。
