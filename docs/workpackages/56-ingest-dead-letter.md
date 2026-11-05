# 56 P2：摄取失败不静默（死信可查 + 错误不吞）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P2（2026-11-04 本机私有快照，未入库）、
> [agent-entry §2](../agent-entry.md)、[environment-facts #1/#2](../environment-facts.md)

## 目标

让摄取失败和稀疏退化不再静默：`_settle_failure` 写库失败时保留可读、脱敏的错误；
`sparse_stats` 的退化计数进入版本质量信息并写日志；管理员能在现有失败 job 查询和
Ingestion 页面看到 `error_code + error`。不新增表，不改自动重试策略。

## 错误不吞

- `_settle_failure` 正常路径仍按原逻辑决定 `pending`/`failed` 和 retry 计数。
- 写库抛 `psycopg.Error` 时不再直接 `return`：
  - 用 `_safe_failure_detail` 截断到 200 字符并脱敏 URL / `password` / `host` /
    `user` / `dbname` 形式；
  - 记录 `ingest settlement write failed` 日志，不记录 `str(error)`（psycopg 错误可能含 DSN）；
  - 尝试一次独立 `_force_failure` 写库，把 job/item 标为 `failed`，保留原
    `error_code`，error 附 `settle write failed: <异常类名>`；
  - fallback 仍失败时只记录异常类名，不抛出，worker 保持运行。
- 测试：正常 settle 写 error_code + error；首次写库失败时 fallback 状态为
  `failed` 且错误脱敏；纯函数 `_safe_failure_detail` 覆盖截断与脱敏。

## 稀疏退化

- `_embed` 已累计 `sparse_stats`；退化时写 warning 日志（ok/no_sparse/invalid/首因），
  并把 `sparse_stats` 放进 `document_version.quality_json`，管理员审核/版本接口可见。
- 测试：假 provider 返回无 sparse 的结果，断言 `no_sparse=2`、日志出现
  `sparse degradation`，且 `_insert_version` 的 quality JSON 含同样统计。

## 失败 job 查询

- 现有列足够：`ingest_job.error_code/error`、`ingest_job_item.error_code/error`；
  **不新增迁移 0053**。
- 扩展 admin store 的 `Jobs` / `JobDetail` / `JobItems` 返回 `error_code + error`；
  API 的列表、详情、资料项都带 `error`。`safeJobError` 再截断/脱敏旧数据。
- Ingestion 页面在任务状态、详情头和资料项中展示错误代码与原因。
- integration 测试：插入含 DSN/密钥形态文本的失败 job，断言列表/详情/资料项可见、
  已截断且不含 secret/DSN。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
cd go && go test -count=1 ./...
cd go && go vet ./...
bash scripts/check.sh
bash scripts/check.sh --smoke
```

失败 job 的 integration 证据用 test-integration 环境执行一次 `-tags integration`
用例；sparse 退化用 Python 日志断言。

## 验证结果（2026-11-05）

- `python`: `454 passed, 37 skipped`；新增 4 条测试（safe detail、正常 settle、
  settle 写库失败 fallback、sparse 退化/quality JSON）。
- `go test -count=1 ./...`: 23 个 package 结果，0 FAIL；`go vet ./...` 通过。
- `go test -tags integration -run TestFailedJobExposesRedactedError`：PASS。
  失败 job 的列表、详情、资料项均返回 `error_code=parse_invalid`；含 DSN/密钥形态
  的 error 已截断为 200 字符 + `…`，不含 secret / `postgresql://` / host 值。
- sparse 假 provider 返回无 sparse 结果：`no_sparse=2`，warning 含
  `sparse degradation`，`document_version.quality_json` 含同样 `sparse_stats`。
- frontend：`npm run lint` 0 errors，`npm test` 57 passed，`npm run build` 通过。
- `bash scripts/check.sh` → `PASSED 43 / UNCONFIGURED 0 / FAILED 0`。
- 重启后 `bash scripts/check.sh --smoke` → `PASSED 46 / UNCONFIGURED 0 / FAILED 0`，
  五条 diff 逐字节一致。
- 无新迁移；`ingest_job.error_code/error` 与 `ingest_job_item.error_code/error`
  已存在，未创建 0053。
- 服务在 Go 重建、Python worker 变更后重启（进程启动时间 21:28:53–21:28:55），
  `/healthz` 正常。

## 不做

- 不加新表、不新增迁移；不改错误码/自动重试策略。
- 不改评测集、棘轮/基线；不重采 smoke；不用 `git add .`；不 push。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- `_force_failure` 也失败时只能写日志，job 仍可能停在 running，等待租约回收；
  这是数据库不可用时的边界。
- sparse 统计只进质量 JSON 和日志，不新增告警/指标页。
- 远端 Actions、e2e、真实模型/OSS/Keycloak 不在本轮范围。
