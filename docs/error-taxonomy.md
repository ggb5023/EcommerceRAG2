# 错误码表（骨架）

[项目入口](../README.md) · [可观测性方案](observability-plan.md) · [架构](architecture.md)

一行一个错误码。**骨架已建，随新增码持续补行**：新增 `finish(...)` / `WriteError(...)` 的错误码时必须同时补一行，
否则视为未完成（见 [决策 0025](decisions/0025-code-level-rules.md) 的 R1——同一件事只写在一处，本表是唯一出处）。

列含义：

- **HTTP**：对外响应状态；`—` 表示只写进 SSE 事件或执行记录的 reason_code，不单独产生 HTTP 错误。
- **可重试**：调用方（浏览器/客服）是否可以原样重试。
- **顾客可见文案**：顾客端看到的中性提示；`不展示` 表示只进审计与执行记录。
- **出现位置**：`文件:函数`。

| 错误码 | HTTP | 可重试 | 顾客可见文案 | 出现位置 |
| --- | --- | --- | --- | --- |
| `permission_denied` | 403 | 否 | 该问题超出可回答范围 | `gateway/main.go:runTurn`（请求策略拒绝） |
| `permission_changed` | 403 | 否 | 权限已变化，请刷新后重试 | `gateway/main.go:runTurn`、`customer/ai.go:run` |
| `invalid_routing_response` | — | 是 | 系统繁忙，请稍后重试 | `gateway/main.go:runTurn`（模型返回空路由） |
| `rag_search_failed` | — | 是 | 暂时无法检索资料 | `gateway/turn_retrieve.go:retrieveEvidence` |
| `invalid_evidence_labels` | — | 否 | 证据排序异常，已转人工 | `gateway/main.go:runTurn` |
| `evidence_contract_violated` | — | 否 | 检索结果异常，已转人工 | `gateway/turn_retrieve.go:filterEvidence`（契约破坏，fail-closed） |
| `persistence_failed` | 503 | 是 | 保存失败，请稍后重试 | `gateway/turn_retrieve.go:persistEvidence`、`turn_generate.go`、`turn_persist.go` |
| `fact_evidence_persistence_failed` | — | 是 | 事实查询结果未能保存 | `gateway/main.go:runTurn` |
| `rag_generate_failed` | — | 是 | 生成失败，请稍后重试 | `gateway/turn_generate.go:generateAnswer` |
| `provider_profile_mismatch` | — | 否 | 服务配置异常 | `gateway/turn_generate.go:generateAnswer`、`turn_retrieve.go:retrieveEvidence` |
| `checkpoint_save_failed` | — | 是 | 请稍后重试 | `gateway/main.go:runTurn`（澄清/快照回退保存检查点） |
| `checkpoint_resume_failed` | — | 是 | 请稍后重试 | `gateway/main.go:runTurn`（父轮次续跑失败） |
| `cancelled` | — | 是 | 已取消 | `gateway/turn_generate.go:generateAnswer`、`turn_persist.go:finishTurn` |
| `knowledge_missing` | — | 否 | 当前店铺知识没有足够依据回答，已转人工 | `customer/ai.go:run`（无授权文档）、`auth/customer.go:IssueCustomerScope` |
| `provider_failed_search` | — | 是 | 检索服务暂不可用，已转人工 | `customer/ai.go:run`（stage=search，含 mock 输出被拒） |
| `provider_failed_generation` | — | 是 | 生成服务暂不可用，已转人工 | `customer/ai.go:run`（stage=generation） |
| `provider_failed_control` | — | 是 | 理解服务暂不可用，已转人工 | `customer/ai.go:run`（stage=control） |
| `customer_requested` | — | 否 | 已转人工客服 | `customer/ai.go:run`（顾客要求转人工） |
| `unsupported_request` | — | 否 | 当前问题需要人工确认 | `customer/ai.go:run`（需要实时订单/物流事实） |
| `ingestion_queue_failed` | 503 | **原因未细分，待修** | 资料排队失败，请稍后重试 | `admin/imports.go:adminConfirmImport`、`admin/import.go`（把授权错误、迁移不完整、撤回后重导入混为一谈，见 `docs/refactor-input.md` 第 9 条） |

## 维护规则

- 新增错误码：先在本表登记（同一提交），再写代码。
- 改语义（HTTP 状态、可重试、文案）：同一提交改本表。
- 表中"原因未细分"的码不得再增加：先拆码，再使用。
