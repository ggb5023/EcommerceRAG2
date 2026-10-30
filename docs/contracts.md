# 跨进程契约表

[项目入口](../README.md) · [架构](architecture.md) · [0025](decisions/0025-code-level-rules.md)

一条契约一行。**两侧都要有测试**才算覆盖；缺一侧标 ⚠️，未实现标 ❌。
`scripts/check/check-contracts.py` 统计 ✅ 的行数并设棘轮，规则见文末。

| 契约 | Go 侧 | Python/前端侧 | 两侧测试 | 状态 |
| --- | --- | --- | --- | --- |
| gRPC 码 → HTTP 状态 | `go/internal/admin/queue_failure.go:queueFailureResponse` | `python/app/rpc/ingest.py`（`context.abort` 的码） | `go/internal/admin/queue_failure_test.go` / 无（Python 侧只测了提交流程，未断言码） | ⚠️ 缺 Python 侧断言 |
| ingest 提交幂等键 | `go/internal/admin/ingestion.go:queuePackage`（`"admin:"+shopID+":"+sha256(manifest)`） | `python/app/rpc/ingest.py:Submit` → `database.submit(idempotency_key=…)` | 无 / `python/tests/test_v2_ingest_owner.py`（只用到 key，不断言推导规则） | ⚠️ 两侧都缺推导规则断言 |
| scope 传递 | `go/internal/auth/authority.go:IssueScopeForShop` | `python/app/chains/provider.py`（`require_scope`） | 无（`auth` 包无对应单测）/ `python/tests/test_provider_scope.py` | ⚠️ 缺 Go 侧断言 |
| SSE 帧格式 | `go/internal/gateway/sse.go:sseStream` | `frontend/src/api.ts:158`（`Accept: text/event-stream` 的消费） | `go/internal/gateway/sse_test.go` / `frontend/src/api.test.ts` | ✅ |
| clarification 信号 | `go/internal/gateway/turn_route.go:decideClarification` | `python/app/graph/workflow.py`（intent 分类） | `go/internal/gateway/turn_route_test.go` / 无（Python 侧未断言与 Go 的字段对应） | ⚠️ 缺 Python 侧断言 |
| store 行类型与可空列 | `go/internal/admin/store_imports.go`（`JobDetailRow` / `JobItemRow`） | —（同侧，Go 内部契约） | `go/internal/admin/store_imports_integration_test.go:TestJobDetailAcceptsNullDatasetHash` | ✅ |
| 稀疏向量写入 | （待 [0029](decisions/0029-sparse-retrieval.md)，尚未实现） | `python/app/providers/aliyun_bailian.py:722`（只解析，不落库） | 无 / 无 | ❌ 未实现 |

## 维护规则

- 新增跨进程字段或语义：先在本表加一行（A 侧文件、B 侧文件都要填），再写实现。
- 同侧（Go 内部）契约也进本表，B 侧填"—（同侧）"：2026-10-29 的 503 就是"store 行类型与可空列"没被钉住造成的。
- 状态从 ⚠️ 升级到 ✅ 的唯一条件是**两侧都有测试**，且在"两侧测试"列写出具体文件/用例名。
- `scripts/check/check-contracts.py` 统计 ✅ 行数并设棘轮 `CONTRACTS_COVERED`：数字只许增；
  某行从 ✅ 退回 ⚠️/❌ 时该数字必然下降，检查直接失败。
