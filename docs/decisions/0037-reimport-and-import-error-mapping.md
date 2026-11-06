# 0037 撤回后重导入与导入错误映射

- 状态：已采纳（2026-11-06 提议，同日采纳并实现）
- 日期：2026-11-06
- 工作包：[68](../workpackages/68-decision-batch.md)
- 关联：[0027](0027-document-source-ownership.md)、[0036](0036-batch-scoped-ingest-idempotency.md)、
  [error-taxonomy](../error-taxonomy.md)、[openapi](../openapi.yaml)

## 问题

1. 导入排队失败曾把授权错误、迁移不完整与临时故障都报成 503，调用方无法区分
   该换权限、该找管理员还是该重试；响应也没有稳定的 cause。
2. active 版本被 revoke 后，同名再导入在 confirm 阶段失败。根因是旧幂等键命中了
   已完成的 job，不会创建新版本（0036），不是 document 行不可复用。

## 决定一：按 gRPC 码映射，不读 message

| gRPC 码 | HTTP | code | cause | 可重试 |
| --- | --- | --- | --- | --- |
| `PERMISSION_DENIED` | 403 | `ingest_not_authorized` | `permission_denied` | 否 |
| `ALREADY_EXISTS` / `adminConflictError` | 409 | `version_conflict` | `conflict` | 否 |
| `FAILED_PRECONDITION` | 500 | `ingest_schema_incomplete` | `migration_incomplete` | 否 |
| `UNAVAILABLE` | 503 | `ingest_queue_failed` | `Unavailable` | 是 |
| 其他 transporter 错误 | 500 | `ingest_queue_failed` | Go 类型名 | 视情况 |

confirm 路径把 `ingest_queue_failed` 改名为 `confirm_queue_failed`，但 HTTP 状态与
cause 沿用同一映射；失败后批次回补 `staged` 并写审计。message 可能含 SQL、DSN 或
路径，绝不进入响应体，也不参与分类。

## 决定二：撤回后同名重导入允许

- `version.revoke` 只撤版本：version → `revoked`、`fencing_epoch+1`；document 保留，
  `active_version_id=NULL`、`status='pending'`。不删除 document。
- 同一 source 再导入同一 `logical_key`：
  - document 存在 → 复用 document 行，创建新 `document_version`（`ready`）；
  - document 已删除 → 按原规则新建 document；
  - 新版本仍走 review → publish，`review_status='approved'` 且 hash 匹配才可激活。
- 不同 source 复用同一 `logical_key` 仍是 409 `version_conflict`（0027 的来源归属
  不变），不能借撤销绕过。

## 状态机与 fencing

`import_batch`：`staged → confirming → confirmed`；`confirmed → revoked`；
`confirming` 超时回补 `staged`。新批次是独立行，不复用旧 batch id。

版本：`ready → active`（publish，要求 approved/hash/quality/fencing 校验）或
`ready → revoked`（revoke）；`active → superseded`（新版本发布）或
`active → revoked`（撤销）。旧 revoked 版本保留历史；新 job 的 fencing 从新 job
claim 开始，不继承被撤销版本的 epoch。

不新增状态值，因此不需要新迁移；0057 未使用。

## 验证

- `go/internal/admin/queue_failure_test.go`：403/409/500/503、cause、message 不参与分类、
  响应不泄漏 DSN/SQL。
- `python/tests/test_v2_ingest_database.py:test_reimport_after_revoke_creates_a_new_version`：
  revoke → 新 job → 新 version → publish 后 document 指回新版本。
- `docs/openapi.yaml` 的导入端点登记 403/409/500/503 与统一错误信封。
