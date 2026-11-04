# 跨进程契约表

[项目入口](../README.md) · [架构](architecture.md) · [0025](decisions/0025-code-level-rules.md)

一条契约一行。**跨进程契约缺一侧测试，等于该契约实际上没有防护**；两侧都有测试才算覆盖（✅），
缺一侧标 ⚠️，未实现标 ❌。同侧契约（Go 内部）只有一条测试即可。
`scripts/check/check-contracts.py` 统计 ✅ 的行数并设棘轮，规则见文末。

| 契约 | 类型 | Go 侧 | Python/前端侧 | 两侧测试 | 状态 |
| --- | --- | --- | --- | --- | --- |
| gRPC 码 → HTTP 状态 | 跨进程 | `go/internal/admin/queue_failure.go:queueFailureResponse` | `python/app/rpc/ingest.py`（`context.abort` 的码） | `go/internal/admin/queue_failure_test.go` / `python/tests/test_ingest_abort_codes.py`（三类错误 → 三个码，且 message 不含表名/DSN） | ✅ |
| ingest 提交幂等键 | 跨进程 | `go/internal/admin/ingestion.go:ingestIdempotencyKey`（`"admin:"+shopID+":"+sha256(manifest)`） | `python/app/rpc/ingest.py:Submit` → `database.submit(idempotency_key=…)` | `go/internal/admin/ingestion_key_test.go`（规则 + 稳定 + 店铺隔离）/ `python/tests/test_v2_ingest_database.py:test_duplicate_submission_conflict_and_stale_scope`（同 key 复用同一 job、换 key 产生新 job、同 key 只有 1 行） | ✅ |
| ValidatePackage 逐行预检 | 跨进程 | `go/internal/admin/import_precheck.go:validatePackage` / `applyPackageValidation`（创建批次调用并写 precheck_detail） | `python/app/rpc/ingest.py:ValidatePackage` → `catalog.validate_typed_document`（只读逐行结果） | `go/internal/admin/import_precheck_test.go:TestApplyPackageValidationMapsRowErrors` / `python/tests/test_ingest_precheck.py`（重复 SKU、非法价格/属性、未知列、表头错误、截断） | ✅ |
| scope 传递 | 跨进程 | `go/internal/auth/authority.go:IssueScopeForShop` | `python/app/chains/provider.py`（`require_scope`） | `go/internal/auth/authority_test.go`（正向 + 篡改 tenant/店铺被拒）/ `python/tests/test_provider_scope.py` | ✅ |
| scope catalog_filter | 跨进程 | `go/internal/domain/catalog.go:CatalogFilterCandidates` / `go/internal/gateway/turn_catalog.go:applyCatalogFilter`（显式实体 + active 商品行校验后重新签名） | `python/app/rpc/rag.py:_catalog_filter` / `python/app/retrieval/postgres.py:_catalog_filter`（结构校验 + 参数化 `EXISTS`） | `go/internal/domain/catalog_test.go` / `python/tests/test_postgres_retriever.py:test_catalog_filter_is_normalized_and_parameterized` | ✅ |
| validity 命令 | 跨进程 | `go/internal/admin/validity.go:adminValidityVersion`（admin 校验/确认/审计，`ingestclient` 发命令） | `python/app/ingest/commands.py:_version` / `_validity`（ingest 事务写版本 + 全部 chunk） | `go/internal/admin/validity_test.go:TestValidValidityDates`、`go/internal/ingestclient/commands_test.go:TestValidityActionIsAVersionCommand` / `python/tests/test_ingest_validity.py:test_validity_action_and_dates` | ✅ |
| usage_log 轮次归因 | 跨进程 | `go/internal/auth/authority.go:IssueScopeForTurn` / `IssueCustomerScope`（签名 `RequestContext` 的 `request_id` / `turn_id` / `execution_id`） | `python/app/graph/service.py:use_attribution`、`python/app/providers/usage.py:attribution_from_scope` | `go/internal/auth/authority_test.go:TestTurnAndExecutionIDsAreCoveredByScopeMAC` / `python/tests/test_langchain_provider_chains.py:test_provider_graph_binds_scope_attribution_to_usage_records` | ✅ |
| scope business_date | 跨进程 | `go/internal/auth/authority.go:IssueScopeForShopWithTurn`（按 `shop.timezone` 计算并签名）、`go/internal/auth/customer.go:IssueCustomerScope` | `python/app/rpc/rag.py`（`_v2_scope` 透传）、`python/app/graph/service.py:LangGraphRagService._business_date` | `go/internal/auth/authority_test.go:TestBusinessDateUsesTheShopTimezoneAtTheUTCDayBoundary` / `python/tests/test_v2_graph.py:test_scope_business_date_wins_over_process_fallback_and_invalid_is_rejected` | ✅ |
| SSE 帧格式 | 跨进程 | `go/internal/gateway/sse.go:sseStream` | `frontend/src/api.ts:158`（`Accept: text/event-stream` 的消费） | `go/internal/gateway/sse_test.go` / `frontend/src/api.test.ts` | ✅ |
| clarification 信号 | 跨进程 | `go/internal/gateway/turn_route.go:decideClarification` | `python/app/graph/workflow.py`（intent 分类） | `go/internal/gateway/turn_route_test.go` / `python/tests/test_workflow_intent_contract.py`（三类 intent + 键集合精确相等，不含终态字段） | ✅ |
| generate decision | 跨进程 | `go/internal/gateway/turn_generate.go:generateAnswer`（消费 `Decision`）/ `go/internal/gateway/main.go:runTurn`（HANDOFF→FAILED） | `python/app/rpc/rag.py:Generate`（`customer_chat` 与 `customer_reply` 发送 `reply_decision`）/ `python/app/graph/workflow.py:_generation_early_result` | `go/internal/gateway/turn_generate_test.go:TestGenerateAnswerHANDOFFWithoutEvidenceIsFailed` / `python/tests/test_v2_rpc_contract.py:test_generate_sends_reply_decision_for_customer_reply` | ✅ |
| store 行类型与可空列 | 同侧 | `go/internal/admin/store_imports.go`（`JobRow` / `JobDetailRow` / `JobItemRow`） | —（同侧，Go 内部契约） | `go/internal/admin/store_imports_integration_test.go:TestJobRowAcceptsNullDatasetHash`、`TestJobDetailAcceptsNullDatasetHash` | ✅ |
| domain 纯函数边界 | 同侧 | `go/internal/domain/`（`FilterEvidence` / `DecideClarification` / `ValidateCitation` / `RoutedOrderResource` + 助手 `IsOrderIdentifier` / `ResourceTokens`） | —（同侧） | `scripts/check/check-domain-imports.py`（禁 gateway/admin/httpapi/pgxpool/grpc 直接引用，禁导出函数接 `context.Context`）+ `go test ./internal/domain/ -cover`（覆盖率以命令输出为准） | ✅ |
| turn 决策记录 | 同侧 | `go/internal/admin/store_turn.go`（`AppendTurnDecision`，按 `tenant_id, execution_id` 建键，接调用方 tx，`ON CONFLICT DO UPDATE`） | —（同侧） | `go/internal/admin/store_turn_test.go`（nil tx / 同一 execution 覆盖为 DONE / 空数组非 NULL） | ✅ |
| 稀疏写入契约 | 跨进程 | `sql/migrations/0044_sparse_vector_wider.sql`（`sparsevec(131072)`；0037 为历史维度）与 `0038_sparse_tokenizer_id.sql`（tokenizer 标记） | `python/app/ingest/sparse.py:to_literal` / `sparse_tokenizer_id`（写入 `chunk.sparse_embedding`） | `python/tests/test_sparse_write.py`（维度、索引越界、权重、非零上限） | ✅ |
| 建议结果与引用 | 跨进程 | `go/internal/gateway/support_suggestions.go:createSuggestion` / `copySuggestion`、`result.go:executionResult`、`recovery.go:persistExecutionTerminal`（`can_copy`） | `frontend/src/customer-api.ts`、`frontend/src/SuggestionPanel.vue`（生成/引用卡片/复制禁用态） | `go/internal/gateway/turn_decision_lifecycle_test.go:TestPersistExecutionTerminalSetsCopyability`、`support_suggestions_test.go:TestCopySuggestionDeniedWhenNotCopyable`、`go/internal/admin/store_analytics_integration_test.go:TestSuggestionTurnsExcludedFromOutcomesAndGaps` / `frontend/src/api.test.ts`（suggestion panel） | ✅ |
| 稀疏查询契约 | 同侧 | `python/app/retrieval/postgres.py`（`SPARSE_RETRIEVAL` 开关、`<#>` sparsevec 分支、降级、`last_sparse_mode` 探针、tokenizer 版本过滤与 `last_sparse_skipped_tokenizer` 计数） | —（同侧，Python 内部契约） | `python/tests/test_sparse_query_mode.py`、`python/tests/test_sparse_mode_probe.py` | ✅ |

## 维护规则

- 新增跨进程字段或语义：先在本表加一行（A 侧文件、B 侧文件都要填），再写实现。
- 同侧（Go 内部）契约也进本表，B 侧填"—（同侧）"：2026-10-29 的 503 就是"store 行类型与可空列"没被钉住造成的。
- 状态从 ⚠️ 升级到 ✅ 的唯一条件是**两侧都有测试**，且在"两侧测试"列写出具体文件/用例名。
- `scripts/check/check-contracts.py` 统计 ✅ 行数并设棘轮 `CONTRACTS_COVERED`：数字只许增；
  某行从 ✅ 退回 ⚠️/❌ 时该数字必然下降，检查直接失败。
