# 26 M7 阶段 1：资料时效调研与决策提议（不实现）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[ROADMAP](../../ROADMAP.md)、[product](../product.md)、
> [0027](../decisions/0027-document-source-ownership.md)、[0016](../decisions/0016-content-inspection.md)、
> [0033 提议](../decisions/0033-document-validity.md)

## 目标

只调研和提议“资料何时生效/失效、过期如何提示”，不改代码、不加迁移、不建表、不接定时任务。
结论写入 [0033](../decisions/0033-document-validity.md)，本文件只写现状证据与阶段 2 边界。

## 现状盘点（实测）

### 1. 字段与索引

- `chunk.effective_from DATE` / `chunk.effective_to DATE` 在
  `sql/migrations/0001_init.sql` 的 chunk 建表段；
- 同文件有 `CHECK (effective_to IS NULL OR effective_from IS NULL OR effective_to >= effective_from)`；
- 同文件创建 `idx_chunk_policy_time (tenant_id, effective_from, effective_to)
  WHERE effective_from IS NOT NULL`；
- `document_version` 没有 effective 字段。日期目前只存在于 chunk 和导入 manifest 的
  `documents[].effective_from/effective_to`。

### 2. 检索过滤

- `python/app/retrieval/postgres.py` 的 `_date_clause` 在 `business_date` 非空时追加：
  `effective_from IS NULL OR effective_from <= business_date` 且
  `effective_to IS NULL OR effective_to > business_date`；
- 因 `>`，`effective_to` 当天已经不参与，日期语义是“失效日（不含当天）”；设计必须
  显式写清，不能把“有效至”当含当天。
- `business_date` 为空时不过滤。当前 `LangGraphRagService` 总会给出日期，但直接调用
  `PostgresRetriever.search` 的代码若不传日期会绕过过滤。

### 3. chunk 写入是否落日期

- `python/app/ingest/worker.py` 的 `_insert_chunks` 已从 manifest spec 写
  `chunk.effective_from` / `effective_to`，并同时写入 `meta_json`；
- 导入 manifest 校验（`go/internal/admin/manifest.go`、`python/app/ingest/cli.py`）已接受
  日期并校验 `effective_to` 晚于 `effective_from`；
- 因此**导入路径的 chunk 日期传播已经存在**。缺口不是“worker 不写日期”，而是
  “版本级管理入口 + 版本级存储 + 过期提示”。

### 4. 运行时 business_date

- `python/app/graph/service.py` 的 `LangGraphRagService` 按
  `显式参数 > SYNTHETIC_BUSINESS_DATE > datetime.now(UTC).date()` 取值；
- `python/app/rpc/rag.py` 构造 service 时没有传显式值，因此 dev/eval 使用环境文件中的
  `SYNTHETIC_BUSINESS_DATE`，生产未设置时使用 UTC 当天；
- Go 侧当前没有把 merchant 当地业务日期作为 scope 字段传给 Python，跨时区边界需要阶段 2
  决定（这不是本轮实现）。

### 5. 管理台入口

- `go/internal/admin/handler.go` 有版本 review/publish/rollback/revoke、版本列表与详情；
- review/publish/revoke 请求没有 effective 日期字段；
- `ReviewDetail` 的 chunk JSON 没有 effective_from/to，管理台目前看不到日期；
- 资料来源导入 manifest 可以带日期，但没有“导入后修改版本有效期”的 API/表单。

## 结论

缺口是**版本级日期设置/存储、导入后传播入口、过期与即将过期提示**；检索过滤和导入时
chunk 写入本身已具备。阶段 2 若不改版本级写者，直接让 admin 写 chunk 会违反
`document_version` 的 ingest 单写者约束。

## 阶段 2 不做

- 不自动下架，不改已执行迁移，不引入 cron/定时任务；
- 不在“没有业务日期来源”时假装时区正确；不把过期提示当自动发布状态变更。

## 验收命令

运行文档链接、文档漂移、文档路径、决策索引四项守卫，再跑快速层 40 项
（`scripts/check.sh`）。
