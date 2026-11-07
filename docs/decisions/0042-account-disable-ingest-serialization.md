# 0042 账户撤销与 ingest 采纳串行化

- 状态：已采纳
- 日期：2026-11-08
- 工作包：[96](../workpackages/96-adoption-lock-order-family.md)
- 关联：[0039](0039-staff-suggestion-knowledge-draft.md)、[contracts](../contracts.md)

## 问题

员工建议采纳走 `ingest_adoption` 受众：Python `IngestDatabase.submit` 在
`knowledge:<tenant.id>` advisory 事务锁内重新校验 `app_user.revoked_at`，再写
`ingest_job`。Go 的 `DisableUser`/`EnableUser` 只取本地的
`pg_advisory_xact_lock(742019, tenant_id)`，两者不互斥：

1. submit 先读到未撤销的账号；
2. 管理员 disable 在另一事务提交（`revoked_at` 置位、会话全撤）；
3. submit 继续提交，撤销后的账号仍能完成一次采纳导入。

反过来先 disable 再 submit 时 submit 会正确拒绝，所以这是单向竞态，不是权限放大，
但它让“撤销立即生效”的语义不再成立。

## 决定

`go/internal/localauth/store.go` 增加单一 helper
`withKnowledgeTenantLock(ctx, tenantID, fn)`，事务内按固定顺序取锁：

1. `pg_advisory_xact_lock(hashtext('knowledge:' || tenant_id)::bigint)`；
2. `pg_advisory_xact_lock(742019, tenant_id)`。

只有 `DisableUser` 与 `EnableUser` 改走该 helper；其它既有
`withTenantLock` 调用（会话、凭据、setup）不变。

- 键使用 `tenant.id`（主键整数），不是 external id；与 Python
  `IngestDatabase.submit`、worker claim、ingest 命令的 `knowledge:<tenant_db_id>`
  完全一致（同一 `hashtext` 输入文本）。
- 顺序固定为 knowledge → 账户锁。若反序，submit 持 knowledge 等账户行锁、
  disable 持账户锁等 knowledge，会互相等待。Python 侧不取 `(742019, tenant_id)`，
  所以只要 Go 单方面保持该顺序即可无环。
- 事务级锁：提交、回滚或连接断开都会释放，不需要清理逻辑；无迁移、无
  `SECURITY DEFINER`。

## 残余风险

- 直连 SQL（迁移、bootstrap、运维手工 UPDATE）不取 knowledge 锁，仍可绕过串行化；
- OIDC/上游 IdP 撤销账号不经 `localauth`，不会等待该锁；本决策只覆盖本地账号开关；
- 数据库 ACL/角色回收不参与该锁；被回收角色的连接失败由数据库报错兜底；
- 锁只协调“自愿参与”的写者，不构成跨进程事务，也不改变既有撤销语义
  （disable 仍只撤当前租户账号与会话）。

## 验证

- `go/internal/localauth/store_integration_test.go:TestDisableAndEnableTakeTheKnowledgeLockBeforeTheAccountLock`：
  旁路连接持 knowledge 锁时 Disable/Enable 阻塞；阻塞期间 `(742019, tenant_id)`
  仍可获取（证明先 knowledge）；释放后成功。等待通过 `pg_locks` 轮询确认，不靠 sleep。
- `python/tests/test_v2_ingest_database.py:test_adoption_submit_is_serialized_by_the_knowledge_lock`：
  持 knowledge 锁时带 `lock_timeout` 的 adoption submit 抛 `LockNotAvailable`；释放后
  同一提交为 `pending`。
