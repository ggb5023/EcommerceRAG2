# 49 P2：usage_log 归因到轮次

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P2](../audits/2026-11-04-global-audit.md)、
> [0021](../decisions/0021-usage-log-instead-of-budget.md)、
> [data-ownership](../data-ownership.md)

## 目标

给 `usage_log` 增加可空的轮次归因列，让一次工作台 turn 的模型调用能追到
`request_id` / `execution_id` / `turn_id`；历史行不回填，ingest/eval 保持
unattributed。

## 迁移 0050

```sql
ALTER TABLE usage_log ADD COLUMN request_id TEXT;
ALTER TABLE usage_log ADD COLUMN execution_id BIGINT;
ALTER TABLE usage_log ADD COLUMN turn_id BIGINT;
CREATE INDEX idx_usage_log_request_id ON usage_log(request_id) WHERE request_id IS NOT NULL;
```

三列全部可空；不新增 GRANT（表级既有权限覆盖新列）；不回填历史行。列语义：

- 工作台轮次：三列均填。`request_id` 为 Go 的 turn request id；
  `execution_id`/`turn_id` 为 Go 签名 scope 中的数字 ID。
- 顾客通道：`request_id` 填 customer execution id；`execution_id`/`turn_id`
  为 NULL（customer execution id 是文本，不是 BIGINT）。
- ingest / eval / judge / 无 scope 调用：三列 NULL。

## 写入

- Python `UsageRecorder` 增加三列；`LangGraphRagService` 从既有 `scope` 设置
  task-local 归因，provider 的 best-effort 写入读取它。
- ingest worker 直接调用 recorder 时不设置归因，保持 NULL。
- Go 的 `RequestContext` 已含 `request_id` / `turn_id` / `execution_id`，本包
  不修改 proto、不做 buf 变更；`IssueScopeForTurn` 已填三个字段。
- `GET /admin/v1/merchant/usage` 聚合口径不变，暂不增加归因过滤。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
cd go && go test ./...

bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：from-zero（含 0050）PASS；dev/test-integration/eval 三库应用后
`schema_migration` 50 行且 0050 sha256 一致；重启 ai 后进程启动时间晚于最后提交；
跑一次工作台 turn，SQL 断言三列非空；各贴一条 ingest/eval 行三列 NULL。

## 不做

- 不回填历史行；不改 usage_log 聚合口径；不新增归因以外的索引。
- 不改产品响应/状态码；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 未覆盖路径

- 顾客通道的 execution/turn 因表 ID 为文本，只归因 `request_id`。
- 历史 usage_log 行保持 NULL；不提供回填脚本。
- 不做 usage 页面的请求过滤 UI。
