# 96 深层加固：adoption 撤销串行化 + 订单族检测

- 状态：见 INDEX.md
- 依据：WP93 遗留；用户 2026-11-08 批准 A1 与窄前缀族检测
- 前置：WP93 已关（`969d8f9`）

## 目标

1. **A1**：让 localauth 的 `DisableUser`/`EnableUser` 与 ingest `submit` 共用
   `knowledge:<tenant.id>` advisory lock，消除撤销与 adoption 提交的竞态；无迁移、无 SECURITY DEFINER。
2. **窄订单族检测**：当售后请求订单工具时，识别与绑定订单同前缀族的裸标识，避免仅依赖控制模型
   上报 entity；不做全量标识扫描。

## 范围

### A1

- `go/internal/localauth/store.go` 新增单一 helper `withKnowledgeTenantLock`：
  事务内先 `pg_advisory_xact_lock(hashtext('knowledge:'||tenant_id)::bigint)`，再取现有
  `(742019, tenant_id)`；仅 Disable/Enable 使用，其它 `withTenantLock` 不变。
- keys 使用 `tenant.id`（非 external id），与 Python `submit` 一致；锁顺序 knowledge→742019。
- Go 集成测试：旁路持 knowledge 锁时 Disable 阻塞；阻塞期间 742019 可获取；释放后成功；Enable 同理。
- Python adoption 测试：持 knowledge 锁时 submit 阻塞，释放后继续/拒绝语义。
- 决策 `0042-account-disable-ingest-serialization.md`；记录直连 SQL/OIDC/ACL 残余风险。
- `docs/contracts.md` 新增 A1 跨进程行；`CONTRACTS_CROSS` 21→22 由主 agent 单独提交。

### 窄订单族检测

- `go/internal/domain/fact_resource.go` 新增 `OrderFamilyReferences(query,bound,ignore)`：
  仅当请求 `get_order_status`/`get_logistics` 时调用；token 含字母+数字+分隔符，首段与绑定订单同族、
  段数与字符类一致；排除绑定订单与模型 entity；纯数字/无分隔返回空。
- `afterSalesFactPlan` 在显式线索/entity 检查后调用；命中 → `order_clarification`。
- 测试：after-102 形态、同族 SKU、不同前缀、纯数字、绑定订单重复、产品-only 不误报。
- 决策 `0043-narrow-order-family-detection.md`；更新 contracts facts 行与限制表。

## 验收

- `go test ./internal/domain ./internal/customer ./internal/localauth`；`go vet ./...`。
- `V2_INTEGRATION_ENV=... go test -tags integration ./internal/localauth`。
- `pytest tests/test_v2_ingest_database.py tests/test_ingest_adoption.py`。
- `check-contracts.py` NOTE 22 → 主 agent 棘轮后 PASS 22/22；`check.sh` 44/0/0。

## 不要做

- 不新增迁移或 SECURITY DEFINER；
- 不扫描所有标识、不改变 staff/suggestion 复制策略；
- 不做 suggestion 审查 kind；
- 不改已执行迁移/smoke 基线；
- 不提交本地未跟踪的审查目录，不用 `git add .`。

## 实现结果（2026-11-08，分支 `wp96`）

### A1

- `go/internal/localauth/store.go` 新增 `withKnowledgeTenantLock`：事务内先
  `pg_advisory_xact_lock(hashtext('knowledge:<tenant.id>')::bigint)`，再
  `pg_advisory_xact_lock(742019, tenant_id)`；仅 `DisableUser` / `EnableUser` 使用，
  其它 `withTenantLock` 调用点不变。
- 决策 [0042](../decisions/0042-account-disable-ingest-serialization.md)；残余风险
  （直连 SQL、IdP 撤销、DB ACL）已记录。
- Go 集成测试 `TestDisableAndEnableTakeTheKnowledgeLockBeforeTheAccountLock`：旁路连接持
  knowledge 锁，通过 `pg_locks` 轮询确认 Disable/Enable 阻塞（不靠 sleep），阻塞期间
  `pg_try_advisory_lock(742019, tenant_id)` 成功（证明先取 knowledge），释放后完成。
- Python `test_adoption_submit_is_serialized_by_the_knowledge_lock`：旁路连接持
  knowledge 锁时，`lock_timeout=500ms` 的 adoption submit 抛 `LockNotAvailable`；
  释放后同一提交为 `pending`。既有 adoption 行锁权限测试保持通过。

### 窄订单族检测

- `go/internal/domain/fact_resource.go:OrderFamilyReferences`：候选 token 含字母+数字+
  至少一个 `-`/`_`，首段与 bound 大小写不敏感相等、段数相同、逐段字符类相同；排除 bound
  与 entity 值；纯数字或无分隔 bound 返回 nil。
- `afterSalesFactPlan` 在显式线索/entity 检查之后、仅当请求订单工具时调用；命中 →
  `order_clarification`。`ExplicitOrderReferences` 未改。
- 决策 [0043](../decisions/0043-narrow-order-family-detection.md)；contracts facts 行
  已补 `OrderFamilyReferences` 与测试名，并新增 A1 跨进程行。

### 验证（真实命令）

```text
cd go && go build ./... && go vet ./...                       # exit 0
cd go && go test ./internal/domain ./internal/customer ./internal/localauth -count=1
  ok domain / ok customer / ok localauth
V2_INTEGRATION_ENV=/etc/ecommerce-rag/test-integration.env \
  go test -tags integration ./internal/localauth -count=1      # ok 0.926s
V2_INTEGRATION_ENV=... go test -tags integration ./internal/localauth \
  -count=3 -run TestDisableAndEnableTakeTheKnowledgeLockBeforeTheAccountLock  # ok
cd python && uv run --frozen pytest -q tests/test_ingest_adoption.py            # 3 passed
V2_INTEGRATION_ENV=<local env with V2_INGEST_DSN> uv run --frozen pytest -q \
  tests/test_v2_ingest_database.py -k adoption                  # 2 passed
contracts 棘轮检查                                               # NOTE 跨进程 22, PASS
docs-links / docs-drift / doc-paths / decisions-index / doc-numbers  # PASS
ruff check tests/test_v2_ingest_database.py                     # PASS
```

### 未运行项与限制

- 快速层 `bash scripts/check.sh`、`--smoke`、`--db` 未在 worktree 运行；由主 agent 在合入后串行执行。
- `CONTRACTS_CROSS` 仍为 21（本分支按约不改 `check-contracts.py`）；主 agent 单独提交棘轮 22。
- Python 集成测试使用临时 env（`V2_MIGRATION_DSN`/`V2_GATEWAY_DSN` 来自
  `test-integration.env`，`V2_INGEST_DSN` 指向同一 `ecr_v2_test_m4`），只跑了 adoption 定向用例，
  未跑 Python 全量。
- 未在 dev/隔离栈上重启服务；本工作包无 ingest/retrieval 生产代码改动，smoke 基线不应变化，
  但未由本分支复验。
