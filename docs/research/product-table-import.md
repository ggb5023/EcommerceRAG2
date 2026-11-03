# 商品表导入：成熟方案对照与本仓库盘点

> 权威等级：context　日期：2026-11-03　工作包：[30](../workpackages/30-m8-product-table-design.md)
> 结论落在 [0034](../decisions/0034-product-table-import.md)。外部产品按名称检索官方文档；
> 正文不写外链 URL，来源链接集中在本文件最后一节，沿用 [frontend-references](frontend-references.md) 的约定。

## 1. 结论摘要

商品表不应继续只以“每行一个 chunk”存在。推荐同时保留两层：

1. **chunk 投影**：每个商品行或尺码行仍生成可引用的 chunk，保留现有检索、证据、审核、
   发布、撤回与引用链路；
2. **类型化商品行**：新增 SKU 行与尺码行，负责 SKU 唯一性、规格枚举、尺码映射、精确
   SQL 过滤和数值读取。向量负责“可能是哪一行”，SQL 负责“这一行的确定值”。

尺码表单独成类型，商品行用 `size_chart_code` 引用；模板与逐行错误放回 [0017](../decisions/0017-m6-l1-import-precheck.md)
的预检流程扩展，不另建上传、审核或发布流程。实时库存/价格仍由 Go 的 facts provider
负责，商品表不成为 `get_stock`/`get_price` 的实时数据源。

## 2. 成熟方案对照

| 候选（检索名） | 可借鉴的约束 | 不采用为依赖的理由 |
| --- | --- | --- |
| RAGFlow | Table 模式支持 CSV/Excel，按行生成 chunk，列可进入正文或元数据；有 chunk 列表与解析预览 | 它是完整 RAG 平台，不是可嵌入的商品表导入组件；表格仍主要作为文本 chunk，不能提供 SKU 唯一性或 SQL 属性过滤。只借“表头感知的行 chunk + 预览”，不引入平台 |
| Dify | 数据集上传支持 CSV/XLSX，文件列表、分段预览、命中测试 | 通用知识库分段模型，没有商品/SKU/尺码 schema、逐行列校验或版本审核语义；作为产品依赖会把首版拖进通用知识库配置 |
| openpyxl | 纯 Python、`read_only` 模式读取 XLSX、无需 pandas、可按单元格读取；官方要求显式关闭工作簿 | 不是本轮实现；阶段 2 需要 XLSX 时作为首选解析库。它不负责列语义、公式审计或模板校验，这些必须由我们的 schema 层做 |
| python-calamine | 读取 XLS/XLSX 很快，Rust 实现，支持多格式 | 引入新的原生 wheel 依赖；商家商品表通常很小，读取速度不是瓶颈。首版用 openpyxl 足够，失败时再评估 |
| pandas | 表格聚合、缺失值处理、列类型推断 | 依赖重、内存占用高，且自动类型推断会把 SKU 前导零、尺码文本和空值改义；商品表校验要确定性，不用 DataFrame 语义 |
| SheetJS / ExcelJS | 浏览器内解析 XLSX、生成模板、即时预览 | 浏览器不能做权威校验；服务端仍要重验，前端解析只会形成两套规则。前端只做文件选择与预览展示 |
| Shopify 商品 CSV / WooCommerce 商品 CSV | 商品行与 variant 行的组织方式：商品列 + SKU/选项/价格/库存列；导入预览与逐行失败提示 | 不引入平台 API 或平台数据模型；只借鉴“商品行 + 变体行”的列组织，平台专用字段不进入本仓库契约 |

说明：RAGFlow、Dify 的价值在交互与表格处理约束，不在其数据模型；openpyxl 是候选依赖，
其余是明确不采用的路线。商品表解析必须由服务端（Python ingest）单点完成，避免浏览器与
服务端两套规则。

## 3. 国内电商后台导出的典型列

平台导出文件不是稳定 API，列名、顺序和空值会随平台、类目、店铺版本变化。可归纳的
稳定语义如下（不是某平台的固定契约）：

| 语义 | 常见列名 | 规范化字段 |
| --- | --- | --- |
| 商品标识 | 商品ID、商品编码、SPU ID、商家编码 | `spu_code` / `sku_code` |
| 商品名称 | 商品标题、商品名称、品名 | `title` |
| 类目/品牌 | 类目、分类、品牌 | `category` / `brand` |
| 销售规格 | 规格1、规格2、颜色、尺码、款式 | `attributes`（颜色、尺码、规格键值） |
| SKU | SKU ID、SKU编码、商家编码、货号、outer_id | `sku_code` |
| 价格 | 售价、价格、一口价、吊牌价 | `list_price_cents`（目录价，非实时价） |
| 库存 | 库存、可售库存、数量 | 不作为商品表实时事实；只可作为可选快照字段 |
| 尺码 | 尺码、尺码表、身高、体重、胸围、腰围、臀围、脚长 | `size_label` / 尺码表行 |

因此模板需要“标准列 + 常见别名”，对未知列给 warning 而不是直接失败；对缺失必填列、
重复 SKU、非法数值和未知尺码表给 block。平台专用列不能成为检索契约。

## 4. 解析库取舍

| 路线 | 结论 | 理由 |
| --- | --- | --- |
| Python 标准库 `csv` | 采用 | 已能处理 BOM、引号、多行字段和列数错误；无新依赖 |
| openpyxl `read_only` | 阶段 2 候选 | 纯 Python，支持 XLSX 基础单元格；只读不执行公式；需要新增一个依赖并锁定版本 |
| python-calamine | 暂不采用 | 原生依赖和 wheel 平台成本，首版商品表规模不需要 |
| pandas | 不采用 | 依赖重、类型推断不可控，会把 SKU/尺码文本改义 |
| 浏览器 SheetJS/ExcelJS | 不采用为权威解析 | 服务端必须重验；只允许用于前端预览，不进入导入判定 |
| 自写 ZIP/XML XLSX 最小解析器 | 暂不采用 | 共享字符串、日期、合并单元格和样式边界多，维护成本高于一个受控依赖 |

XLSX 读取边界应在决策 0034 中写死：只读第一个工作表、不执行公式/宏、合并单元格报错、
日期按文本或显式日期列处理、空行忽略、最大行列与单元数受限。CSV 与 XLSX 共用同一套
规范化行和校验器，不能有两个解析结果。

## 5. chunk 与结构化表的关系

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| 继续每行一个 chunk，靠 `meta_json` 过滤 | 复用现有链路最快 | 无唯一约束、无类型化数值、错误行难定位；JSONB 过滤和索引弱 | 只作为证据投影，不作为权威表 |
| 只建结构化表，不生成 chunk | SQL 精确、数据干净 | 失去现有证据引用、语义召回和审核预览；商品描述类文本仍需要向量 | 不采用 |
| 结构化表 + 每行 chunk 投影 | 精确过滤与证据引用兼得；复用发布/回滚 | 需要保证两者同事务、同一行键 | **推荐** |
| 外部 PIM/平台 API 作为商品主数据 | 数据实时、字段标准 | 首版明确不接平台 API，且商家自部署要能离线导入 | 不采用 |

## 6. 对 0034 的输入

- 商品表是 `product_table` 文档类型，尺码表是 `size_chart` 文档类型；商品行通过
  `size_chart_code` 引用尺码表，不把尺码矩阵复制到每个 SKU。
- 类型化行与 chunk 必须在同一摄取事务中写入；只读检索按 active/reviewed 版本读取，
  不复制发布状态。
- SKU 过滤和属性过滤由 Go 签发结构化过滤条件，Python 只做 scope 内 SQL 收窄；
  数值由 SQL 取值，向量不直接产生数值结论。
- 预检复用 0017 的 `import_batch` / `import_item`，行级错误扩展在 `precheck_detail`；
  不新建上传批次或审核状态机。
- 实时库存/价格继续由 facts provider 提供；商品表只用于 SKU 识别、静态规格和目录价
  快照，不能冒充 `get_stock`/`get_price` 的实时结果。

## 7. 外部来源（按名称检索）

| 名称 | 链接 |
| --- | --- |
| RAGFlow | https://github.com/infiniflow/ragflow |
| Dify | https://docs.dify.ai/ |
| openpyxl | https://openpyxl.readthedocs.io/en/stable/optimized.html |
| python-calamine | https://github.com/dimastbk/python-calamine |
| pandas | https://pandas.pydata.org/docs/ |
| SheetJS | https://docs.sheetjs.com/ |
| Shopify product CSV | https://help.shopify.com/en/manual/products/import-export/using-csv |
| WooCommerce product CSV importer | https://woocommerce.com/document/product-csv-import-suite/ |

这些链接只用于核对名称对应的官方文档；正文与决策不复制外部 URL，避免仓库出现无意义的
外部路径和版本漂移。
