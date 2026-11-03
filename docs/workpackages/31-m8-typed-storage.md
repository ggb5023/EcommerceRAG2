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

## 依赖审查

- 依据 `AGENTS.md` 的依赖审查规则：新依赖必须有 ROADMAP 或决策记录依据；ROADMAP 的
  M8 明确要求 CSV/XLSX，因此允许引入 XLSX 解析依赖。
- 选择 openpyxl 3.1.5（MIT）及传递依赖 et-xmlfile 2.0.0（MIT），使用 `read_only`
  模式；不引入 pandas、calamine 或浏览器解析库。锁文件随依赖提交冻结。

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

- XLSX：openpyxl 3.1.5 + et-xmlfile 2.0.0（MIT），`read_only`；只读第一个工作表，
  公式/宏/合并单元格/隐藏工作表/超限行列均明确报错；CSV 与 XLSX 共用行元素构造。
- manifest：支持 `product_table`/`size_chart` + `product_table_v1`/`size_chart_v1`；
  `document.doc_type` 落库；旧 manifest 保持 format 行为；chunk `meta_json` 带
  `table_schema`、`catalog_row_key`、`sku_code`/`chart_code`、`source_row`。
- 迁移 0049：`catalog_sku`、`catalog_size_chart` 全部列/约束/索引/GRANT；
  from-zero **PASS 49 files**；dev/test-integration/eval 三库均 49 行，0049 与文件
  sha256 无差异。
- ingest：解析后构造类型化行，与 chunk/版本同一事务写两张表；未知属性保留并 warning；
  SKU 大小写不敏感唯一、尺码表复合唯一、价格缺观测日拒绝。
- 发布：active 商品表唯一性检查；商品行引用未 active 尺码表/尺码时拒绝发布。
- 验证：`pytest -q` **440 passed / 33 skipped**；隔离集成套件 **28 passed**；
  `check.sh` **40 PASS**；`--smoke` **45 PASS**，五条 diff 无输出。
- 实测 dev 导入合成 `products.csv` 的 7 行本店子集：版本 93，`doc_type=product_table`，
  `catalog_sku` 7 行、chunk 7 行；`meta_json` 四个投影字段均存在；重复 SKU 触发
  `UniqueViolation`，价格缺 `price_observed_at` 触发 `CheckViolation`（均回滚）。
- dev 进程已重启；最终启动时间在最终提交之后，见交付汇报。
- 验收命令已实跑通过，等待主 agent 复核后再转 `done`。

## 未覆盖路径

- WP32 的预检/模板/逐行错误与 WP33 的检索过滤未开始；本轮没有模板文件或前端入口。
- 没有做真实 XLSX 端到端导入（只做解析/包校验/单元测试）；没有真实平台导出列。
- 没有在 dev 发布商品表（避免污染 smoke）；发布校验由隔离集成测试覆盖。
- `catalog_size_chart` 的 dev 实库只验证了表和空计数，尺码映射/发布顺序由集成测试覆盖。
- 未运行 `--db`、`--e2e`、`--all`；未改 facts provider、检索 SQL 或自动发布/下架。
- 合成 `products.csv` 的 `price_cents` 作为未知属性保留并 warning，不冒充
  `list_price_cents`（缺少观测日）。
