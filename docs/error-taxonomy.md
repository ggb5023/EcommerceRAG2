# 错误码表（骨架）

[项目入口](../README.md) · [可观测性方案](archive/observability-plan.md) · [架构](architecture.md)

一行一个错误码。**骨架已建，随新增码持续补行**：新增 `finish(...)` / `WriteError(...)` 的错误码时必须同时补一行，
否则视为未完成（见 [决策 0025](decisions/0025-code-level-rules.md) 的 R1——同一件事只写在一处，本表是唯一出处）。

**术语约定**：本表的 `cause` 只放类别标识；HTTP 状态由 gRPC 码决定，不由 message 决定。
新增错误码时必须同时补：**gRPC 码 → HTTP 状态 → code → cause** 的完整一行。

列含义：

- **HTTP**：对外响应状态；`—` 表示只写进 SSE 事件或执行记录的 reason_code，不单独产生 HTTP 错误。
- **可重试**：调用方（浏览器/客服）是否可以原样重试。
- **顾客可见文案**：顾客端看到的中性提示；`不展示` 表示只进审计与执行记录。
- **出现位置**：`文件:函数`。

| 错误码 | HTTP | 可重试 | 顾客可见文案 | 出现位置 |
| --- | --- | --- | --- | --- |
| `permission_denied` | 403 | 否 | 该问题超出可回答范围 | `go/internal/gateway/main.go:runTurn`（请求策略拒绝） |
| `permission_changed` | 403 | 否 | 权限已变化，请刷新页面 | `go/internal/gateway/main.go:runTurn`、`go/internal/customer/ai.go:run` |
| `invalid_routing_response` | — | 是 | 系统繁忙，请稍后重试 | `go/internal/gateway/main.go:runTurn`（模型返回空路由） |
| `rag_search_failed` | — | 是 | 暂时无法检索资料 | `go/internal/gateway/turn_retrieve.go:retrieveEvidence` |
| `invalid_evidence_labels` | — | 否 | 证据排序异常，已转人工 | `go/internal/gateway/main.go:runTurn` |
| `evidence_contract_violated` | — | 否 | 检索结果异常，已转人工 | `go/internal/gateway/turn_retrieve.go:filterEvidence`（契约破坏，fail-closed） |
| `persistence_failed` | 503 | 是 | 保存失败，请稍后重试 | `go/internal/gateway/turn_retrieve.go:persistEvidence`、`go/internal/gateway/turn_generate.go`、`go/internal/gateway/turn_persist.go` |
| `persist_failed` | — | 否 | 导入未完成，请联系维护者 | `python/app/ingest/worker.py`（向量写入被数据库拒绝，含非法 `sparsevec` 字面量；仅 job 记录，不上响应） |
| `fact_evidence_persistence_failed` | — | 是 | 事实查询结果未能保存 | `go/internal/gateway/main.go:runTurn` |
| `rag_generate_failed` | — | 是 | 生成失败，请稍后重试 | `go/internal/gateway/turn_generate.go:generateAnswer` |
| `provider_profile_mismatch` | — | 否 | 服务配置异常 | `go/internal/gateway/turn_generate.go:generateAnswer`、`go/internal/gateway/turn_retrieve.go:retrieveEvidence` |
| `fact_tool_not_allowed` | — | 否 | 该问题需要人工确认 | `go/internal/facts/facts.go:FailureCode`（模型请求了未授权工具） |
| `fact_provider_unavailable` | — | 是 | 事实查询暂不可用 | `go/internal/facts/facts.go:FailureCode` |
| `fact_provider_unauthorized` | — | 否 | 事实查询无授权 | `go/internal/facts/facts.go:FailureCode` |
| `fact_provider_rate_limited` | — | 是 | 查询过于频繁，请稍后重试 | `go/internal/facts/facts.go:FailureCode` |
| `fact_provider_timeout` | — | 是 | 事实查询超时 | `go/internal/facts/facts.go:FailureCode` |
| `fact_provider_canceled` | — | 是 | 已取消 | `go/internal/facts/facts.go:FailureCode` |
| `fact_provider_invalid_response` | — | 是 | 事实查询返回异常 | `go/internal/facts/facts.go:FailureCode` |
| `fact_tool_failed` | — | 是 | 事实查询失败 | `go/internal/facts/facts.go:FailureCode` |
| `checkpoint_save_failed` | — | 是 | 请稍后重试 | `go/internal/gateway/main.go:runTurn`（澄清/快照回退保存检查点） |
| `checkpoint_resume_failed` | — | 是 | 请稍后重试 | `go/internal/gateway/main.go:runTurn`（父轮次续跑失败） |
| `cancelled` | — | 是 | 已取消 | `go/internal/gateway/turn_generate.go:generateAnswer`、`go/internal/gateway/turn_persist.go:finishTurn` |
| `knowledge_missing` | — | 否 | 当前店铺知识没有足够依据回答，已转人工 | `go/internal/customer/ai.go:run`（无授权文档）、`go/internal/auth/customer.go:IssueCustomerScope` |
| `provider_failed_search` | — | 是 | 检索服务暂不可用，已转人工 | `go/internal/customer/ai.go:run`（stage=search，含 mock 输出被拒） |
| `provider_failed_generation` | — | 是 | 生成服务暂不可用，已转人工 | `go/internal/customer/ai.go:run`（stage=generation） |
| `provider_failed_control` | — | 是 | 理解服务暂不可用，已转人工 | `go/internal/customer/ai.go:run`（stage=control） |
| `customer_requested` | — | 否 | 已转人工客服 | `go/internal/customer/ai.go:run`（顾客要求转人工） |
| `unsupported_request` | — | 否 | 当前问题需要人工确认 | `go/internal/customer/ai.go:run`（需要实时订单/物流事实） |
| `ingest_not_authorized` | 403 | 否 | 没有资料导入权限 | `go/internal/admin/queue_failure.go:queueFailureResponse`（gRPC `PERMISSION_DENIED`，含 `IngestAuthorizationError`），cause=`permission_denied` |
| `ingest_schema_incomplete` | 500 | 否 | 资料服务未就绪，请联系管理员 | `go/internal/admin/queue_failure.go:queueFailureResponse`（gRPC `FAILED_PRECONDITION`，由 Python `IngestSchemaIncompleteError` 映射），cause=`migration_incomplete` |
| `ingest_queue_failed` | 503 / 500 | 是 | 资料排队失败，请稍后重试 | `go/internal/admin/queue_failure.go:queueFailureResponse`（`UNAVAILABLE` → 503，其余 → 500），cause=出错类别（gRPC 码名或 Go 类型名，**不含 message**） |
| `batch_already_confirming` | 409 | 否 | 该批次正在确认中，请稍后刷新查看 | `go/internal/admin/store_imports.go:ClaimBatchForConfirm`（status 抢占失败，并发 confirm） |
| `confirm_queue_failed` | 503 / 500 | 是 | 排队失败，请重试 | `go/internal/admin/imports.go:adminConfirmImport`（排队失败后批次回补 `staged` 并写 `confirm_queue_failed` 审计），HTTP 状态沿用 `queueFailureResponse` |
| `confirm_settle_failed` | 500 | 否 | 确认未能落定，请联系管理员 | `go/internal/admin/store_imports.go:SettleConfirmSuccess`（status 已不是 `confirming`，多为超时回收抢占） |
| `version_conflict` | 409 | 否 | 该文档标识已被另一个来源占用；请使用新的标识，或撤回后先清理来源 | `go/internal/admin/import.go:adminImport`、`go/internal/admin/queue_failure.go:queueFailureResponse`（gRPC `ALREADY_EXISTS`，含"文档 id 已属于另一个来源"；或 `adminConflictError`），cause=`conflict` |

## 维护规则

- 新增错误码：先在本表登记（同一提交），再写代码。
- 改语义（HTTP 状态、可重试、文案）：同一提交改本表。
- 表中"原因未细分"的码不得再增加：先拆码，再使用。
- 带 `cause` 的错误码只允许放**类别标识**（gRPC 码名、Go 类型名或固定短标识）；message、SQL、DSN、路径、凭据一律不进响应体。
- 映射只读 gRPC 码，不读 message：Python 侧用不同的码表达类别（`PERMISSION_DENIED` / `FAILED_PRECONDITION` / `ALREADY_EXISTS` / `UNAVAILABLE`）。
