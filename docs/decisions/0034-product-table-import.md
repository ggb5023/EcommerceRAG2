# 0034 商品表导入：类型化商品行、尺码表与过滤

- 状态：提议，未实现
- 日期：2026-11-03
- 工作包：[30](../workpackages/30-m8-product-table-design.md)
- 调研：[product-table-import](../research/product-table-import.md)
- 关联：[0017](0017-m6-l1-import-precheck.md)、[0027](0027-document-source-ownership.md)、
  [0032](0032-intelligence-domain-and-crawling.md)、[0021](0021-usage-log-instead-of-budget.md)

## 1. 问题与边界

M8 要让商家导入商品表与尺码表，按 SKU 和规格过滤，并在预检时逐行列报错。当前 CSV
已能作为普通文档解析成每行一个 chunk，但没有类型化商品行、SKU 唯一性、尺码映射或
SQL 属性过滤；实测与缺口见工作包 §现状盘点。

本决策只定数据形态、预检扩展、检索过滤和 facts 边界。它不实现代码、不建表、不接线；
阶段 2 才写迁移与代码。实时库存/价格、平台 API、自动补全 SKU 不在本决策范围。

## 2. 决定

### 2.1 文档与清单类型

- 新增 manifest 的 `doc_type` 取值 `product_table` 与 `size_chart`，并要求同时给出
  `table_schema: product_table_v1` 或 `table_schema: size_chart_v1`。
- 导入格式为 `csv` 或 `xlsx`。CSV 继续使用标准库；XLSX 在阶段 2 采用 openpyxl 只读模式，
  只读第一个工作表，不执行公式/宏，合并单元格、隐藏工作表、超限行列直接报错。
- `doc_type` 必须落库到现有 `document.doc_type`，不能继续把 `format` 当业务类型。
  没有 `doc_type` 的旧 manifest 保持现有行为。
- 商品表与尺码表都仍生成 chunk；chunk 的 `meta_json` 增加 `table_schema`、
  `catalog_row_key`、`sku_code` 或 `chart_code`、`source_row`，用于引用定位。
- 商品表只来自商家自行上传的资料，属于 0032 的 answer 域自有/已授权内容；外部抓取或
  intelligence 共享层不得自动写入商品表，必须经商家显式导入、审核、发布。

### 2.2 类型化商品行

阶段 2 拟新增 `catalog_sku`，一行一个 SKU，挂在 `document_version` 上：

| 列 | 规则 |
| --- | --- |
| `tenant_id`, `shop_id`, `version_id` | 必填；`version_id` 关联 `document_version(tenant_id,id)` |
| `source_row` | 源文件物理行号，必须可复现 |
| `chunk_index` | 指向本版本 chunk，用于引用；一行多 chunk 时取首块 |
| `sku_code` | 必填；trim 后非空，长度 ≤128；同一 `(tenant_id,shop_id,version_id)` 内按大小写不敏感唯一 |
| `spu_code` | 可选；同款商品分组用 |
| `title` | 必填；商品标题 |
| `category` | 可选；类目文本 |
| `brand` | 可选；品牌文本 |
| `color` | 可选；颜色文本，过滤时 trim + 小写归一 |
| `size_label` | 可选；尺码文本（S、M、42、均码等） |
| `size_chart_code` | 可选；引用尺码表 `chart_code`，不复制尺码矩阵 |
| `attributes` | JSONB 对象；只允许已登记键和单位，未知键保留但只给 warning，不参与过滤 |
| `list_price_cents` | 可选目录价快照；≥0，必须同时有 `price_observed_at`，不是实时价 |
| `currency` | 可选，默认 CNY；与价格同时给出 |
| `price_observed_at` | 可选日期；价格快照的观测日 |

已登记数值属性：`capacity_ml`、`length_cm`、`width_cm`、`height_cm`、`weight_g`；
文本属性：`material`、`pattern`。单位写在键名里，不允许 `"480ml"` 这类混合单位字符串。
`stock` 不进入商品表；库存只由 facts provider 提供。

唯一性与发布约束：SKU 在单个版本内唯一；同一店铺同一时刻只允许一个 active
`product_table` 文档，发布前检查，发现第二个 active 商品表则拒绝发布。旧版本行保留不删，
由 `document_version` 状态与 `active_version_id` 决定可见性。

### 2.3 尺码表

阶段 2 拟新增 `catalog_size_chart`，一行一个尺码：

| 列 | 规则 |
| --- | --- |
| `tenant_id`, `shop_id`, `version_id` | 必填，关联版本 |
| `source_row`, `chunk_index` | 源行号与引用块 |
| `chart_code` | 必填；同一版本内标识一张尺码表 |
| `size_label` | 必填；与商品行 `size_label` 精确匹配 |
| `size_system` | 枚举：`cn_shoe`、`cn_clothing`、`cn_kids`、`unisex`、`custom` |
| `measurements` | JSONB；只允许 `chest_cm`、`waist_cm`、`hip_cm`、`foot_length_cm`、`height_cm`、`weight_kg` 等已登记键，数值非负 |
| `fit_note` | 可选；版型/偏大偏小说明 |

`(tenant_id,shop_id,version_id,chart_code,size_label)` 唯一。商品行引用不存在的
`size_chart_code` 或尺码不在表中：预检 warning，确认可继续；发布时仍缺表则拒绝，避免
回答时临时猜测。尺码表可以与商品表同批导入，发布顺序要求尺码表先 active。

### 2.4 写入与生命周期

- 新表写入方仍是 Python `ingest`；与 `document`、`document_version`、`chunk` 在同一摄取
  事务内写入。Go/admin 只读，不新增 SQL 写路径。
- 复用现有导入、审核、发布、回滚、撤回。只有 `document.active_version_id` 指向、
  `status='active'`、`review_status='approved'` 且在有效期内的版本参与检索。
- 回滚/撤回不删类型化行；旧行随版本状态失效保留，保证审计可追溯。
- 发布商品表时校验 active 商品表唯一性；发布尺码表不改变商品表状态。

### 2.5 预检、模板与逐行错误

复用 [0017](0017-m6-l1-import-precheck.md) 的 `import_batch` / `import_item`，不新建
上传批次、审核类型或发布状态机。当前 `ValidatePackage` RPC 只在 proto 中声明，
Python/Go 都没有实现；阶段 2 实现并扩展它，作为“staged 文件预检”入口：

- 扩展 `ValidatedPackageDocument`：`table_schema`、`row_count`、`error_count`、
  `row_errors[]`；错误结构为
  `{row,column,code,severity,value,message,suggested_action}`。
- Go 在创建批次时调用该 RPC，把逐行结果写入 `import_item.precheck_detail` JSONB；
  `import_item` 仍按文档一行，不按商品行拆表。错误总数与单行长度设上限（例如 200 条 /
  200 字），超出截断并给 `truncated`。
- 预检规则至少覆盖：缺必填列、未知列（warn）、空/重复表头、列数不符、SKU 重复（大小写
  不敏感）、非法数字/单位、非法 `size_system`、未知尺码表/尺码、价格缺观测日、
  XLSX 公式/合并单元格/超限。block 错误存在时不得确认。
- 模板由同一份 schema 生成，不提交第二份手写模板。阶段 2 提供只读模板下载
  （商品表与尺码表两种），CSV 使用 UTF-8 BOM；不生成 XLSX 模板，避免引入模板写出路径。
- 确认仍走现有 `persistParsed` → Submit → worker；不改变单写者与审核边界。

### 2.6 检索与过滤

- Go 从用户显式 SKU、颜色、尺码或规格中提取候选过滤条件；先在 scope 内用只读 SQL
  校验 active 商品行，再把过滤条件放入 Go 签发的 scope 新字段（暂名
  `catalog_filter`）。Python 只校验 scope 并用参数化 SQL 收窄，不得自行扩权或从原始
  查询任意拼条件。
- 过滤 SQL 以 active/reviewed 版本的商品行为准，按 `sku_code`、`color`、`size_label`、
  `size_chart_code` 和已登记 `attributes` 键过滤；商品行与 chunk 通过
  `(tenant_id,version_id,chunk_index)` 关联。无匹配时失败关闭，返回资源不存在/澄清，
  不让模型猜。
- 向量只负责“可能匹配哪个商品/行”；`capacity_ml`、尺寸、价格快照等具体值由 SQL 读取，
  不把数值交给向量或模型生成。回答引用仍绑定原 chunk。
- 没有显式过滤时维持现有检索路径，不强行把每轮问题都转成商品查询。

### 2.7 facts 边界

- `get_stock`/`get_price` 继续由 Go 的 facts provider 提供，商品表**不是**它们的实时数据源。
- 商品表只用于 SKU 识别、静态规格和目录价快照；`list_price_cents` 必须带
  `price_observed_at`，展示时说明“资料快照”，不能回答“现在多少钱”。
- 库存没有快照字段；没有 facts provider 时只能说明无法确认实时库存，不能把商品表或
  chunk 里的旧数字当现价/现货。
- 未来若要让商品表成为只读 facts 源，必须另写决策，并明确观测时间、失效和覆盖规则。

### 2.8 明确不做

- 不做实时库存/价格对接、平台 API、自动补全 SKU 数据。
- 不做 OCR、图片尺码表、PDF 解析、任意公式/宏、多工作表合并。
- 不自动发布、不自动下架、不跳过审核、不新增第二写者。
- 不改冻结评测集、棘轮、基线或已执行迁移；不在本阶段写迁移、代码或模板文件。

## 3. 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 继续每行一个 chunk，只用 `meta_json` 过滤（RAGFlow 式） | [RAGFlow](https://github.com/infiniflow/ragflow) | 无 SKU 唯一约束、无类型化数值、无逐行错误；JSONB 过滤和索引弱，不能作为权威商品行 |
| 只把字段塞进 `chunk.meta_json`，不建类型化表 | [PostgreSQL JSONB](https://www.postgresql.org/docs/current/datatype-json.html) | 少了列约束、枚举、唯一索引和跨表尺码映射；错误行无法在写入前稳定定位。JSONB 仍在 chunk 中作投影，但不做权威表 |
| 浏览器 SheetJS/ExcelJS 解析并校验 | [SheetJS](https://docs.sheetjs.com/) | 浏览器不能做权威校验；服务端仍要重验，会形成两套规则和两套错误码 |
| 直接接外部 PIM 或平台商品 API | [Shopify product API](https://shopify.dev/docs/api/admin-rest/latest/resources/product) | 首版明确不依赖平台接口，且平台字段/库存价不是商家自有导入数据；与自部署、离线导入冲突 |
| 单独引入搜索/商品中台服务 | [OpenSearch](https://opensearch.org/docs/latest/) | 多一个必须运维的服务，与“轻量自部署”冲突；本仓库 PostgreSQL 已能承担小规模 SKU 过滤 |

## 4. 后果

- 阶段 2 需要一条新迁移（下一个可用编号，预计 0049）建两张表、索引和 GRANT；写入方仍为
  Python `ingest`。新增表前先更新 `docs/data-ownership.md`。
- 需要扩展 proto 的 `ValidatePackage` 响应与 scope 过滤字段，登记
  `docs/contracts.md`，Go/Python 两侧各有测试；未完成前不得把过滤当作已实现。
- 需要新增导入预检的 schema 与逐行错误测试、CSV/XLSX 解析测试、SKU 唯一性/尺码映射测试、
  active 商品表唯一性测试、检索过滤测试；不修改冻结评测集。
- 现有 CSV 文档继续可用；没有 `doc_type` 的旧 manifest 保持原行为，避免破坏已发布资料。

## 5. 责任与状态

- **Python ingest**：解析、规范化、逐行校验、写 chunk 与两张类型化表（同一事务）。
- **Go**：授权、显式过滤条件签名、只读校验、模板与预检结果返回；不直接写商品表。
- **商家**：确认预检、审核、发布、回滚、撤回；不自动改变发布状态。
- **本记录**：提议，未实现。阶段 2 开工前需用户确认 schema、模板形态和迁移编号。
