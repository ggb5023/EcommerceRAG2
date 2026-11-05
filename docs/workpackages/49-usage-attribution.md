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
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 未覆盖路径

- 顾客通道的 execution/turn 因表 ID 为文本，只归因 `request_id`。
- 历史 usage_log 行保持 NULL；不提供回填脚本。
- 不做 usage 页面的请求过滤 UI。

## 实际交付

- 迁移 0050：新增 nullable `request_id` / `execution_id` / `turn_id` 和
  `idx_usage_log_request_id` 部分索引；不回填历史行，不新增 GRANT。
- from-zero：`PASS 0050`，`PASS 50 migration checksums`。dev/test-integration/eval
  三库均 `schema_migration=50`，0050 checksum 与文件
  `d408ed836d88db0871e7cfa0a9d8d10fb913354a82ef36292cb2e07f9097d616` 一致。
- Python：`UsageRecorder` 新增三列；`LangGraphRagService` 从既有 scope 绑定
  task-local 归因，provider best-effort 写入读取它；ingest/eval 不设置归因。
  `UsageRecorder.from_environment(role="ai")` 增加 `V2_RETRIEVER_DSN` 回退，
  避免 selfhost 的 AI 角色没有独立 `V2_USAGE_DSN` 时静默不记录。
- Go：未改 proto；`RequestContext` 已有 `request_id`/`turn_id`/`execution_id`，
  `IssueScopeForTurn`/`IssueCustomerScope` 已填。新增 Go 测试证明 turn/execution
  字段参与 scope MAC。
- 工作台 live turn：conversation 802 / turn 823 / execution 822 / request
  `v2-1793811756061559987`，最终 DONE。dev profile 为 mock，**MockProvider 不写
  usage_log**，所以该 HTTP turn 的 usage_log 行数为 0；这是既有 mock 行为，不是
  归因丢失。
- 用同一个真实 turn 的 scope/IDs，以可写 UsageRecorder 的图服务探针验证 SQL：
  ```text
  v2-1793811756061559987 | 822 | 823 | 2 | control | wp49-attribution-probe | 1 | 1
  ```
  ingest 既有行 `137|2|embedding|mock-embedding|||` 与 eval 探针
  `139|2|control|wp49-eval-probe|||` 均为三列 NULL。探针和 turn 测试数据已清理，
  残留计数 0。
- 测试：`python/.venv/bin/python -m pytest -q` **459 passed / 36 skipped**；
  `go test -count=1 ./...` 全绿；`go vet ./...` 通过。
- 契约/文档：`contracts.md` 增加 usage_log 归因行（两侧测试）；
  `data-ownership.md` 更新 usage_log 归因口径；0021 补归因段；agent-entry 迁移数
  更新为 50。
