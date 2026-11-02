# 21 后端债务收尾：保留策略 + SSE 修正 + 剩余长函数

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[05-python-long-functions](05-python-long-functions.md)、
> [environment-facts](../environment-facts.md)、[0005](../decisions/0005-copyable-reply-policy.md)

## 目标

1. 每个员工的隐藏 suggestion conversation 只保留最新 **N=50** 个建议 turn；
2. `completed` SSE 帧的 `can_copy` 与 `customer_reply` 终态一致；
3. 拆分 WP05 未覆盖的剩余 >80 行 Python 函数，行为零变更。

## 保留策略

- 插入新建议 turn 的同一事务内，删除该 conversation 超出 50 的最旧 suggestion turn。
- 已核对 FK：`turn` / `conversation_execution` 的依赖表均**没有 ON DELETE CASCADE**，
  因此不改 FK，也不修改历史迁移；在 `store_suggestion.go` 内按依赖顺序显式删除：
  `turn_decision` → `citation` → `evidence_snapshot` → `customer_reply` →
  `sse_event` → `retrieval_trace` / `routing_trace` / `tool_call_trace` →
  `feedback` → `message` → `turn_idempotency` → `conversation_execution` → `turn`。
- 所有删除都带 `tenant_id`；只写 `turn*` / `conversation_execution` / `message` /
  `sse_event` 等 gateway 表，走 ownership 语义。
- 保留策略删除旧 `turn_idempotency` 行，因此被清理的旧幂等键不再能重放；新请求不受影响。
- 显式删除需要 `turn_decision` 与 `retrieval_trace` 的 DELETE 权限；追加 **0046** 只做
  两条 GRANT（不改 schema / FK），已应用 dev、test-integration、eval 三库并从零验证。
- 测试：同一 suggestion conversation 连续插入 52 个 turn，断言只保留最新 50 个，
  最旧 turn 及其 evidence/citation/message/sse_event/turn_decision/trace/feedback 均无残留。

## SSE 完成帧

- `persistExecutionTerminal` 计算出的 `canCopy` 同时写入 `customer_reply.can_copy`
  与 `completed` 事件的 `can_copy`；补测试断言两者一致，覆盖 DONE 有/无引用和 mock。

## 长函数（开工前 AST 实测）

| 函数 | 开工前 | 位置 |
| --- | --- | --- |
| `validate_manifest` | 138 | `python/app/ingest/cli.py` |
| `embed` | 131 | `python/app/providers/aliyun_bailian.py` |
| `_fetch_once` | 130 | `python/app/web/crawler.py` |
| `search` | 121 | `python/app/retrieval/postgres.py` |
| `build_provider_chains` | 117 | `python/app/chains/provider.py` |
| `bundle_dataset` | 111 | `python/app/ingest/bundles.py` |
| `store_artifact_bundle` | 101 | `python/app/ingest/artifacts.py` |
| `Submit` | 89 | `python/app/rpc/ingest.py` |
| `rerank` | 88 | `python/app/providers/aliyun_bailian.py` |
| `_version` | 88 | `python/app/ingest/commands.py` |
| `safe_extract_zip` | 87 | `python/app/providers/mineru.py` |
| `parse_persisted_package` | 87 | `python/app/ingest/worker.py` |
| `_claim` | 85 | `python/app/ingest/worker.py` |
| `_call_once` | 84 | `python/app/providers/aliyun_bailian.py` |

`provider_understand_node` 在 WP15 提示词资产化后实测 52 行，已满足 ≤80，不再改动。
清单外（≤80 行）函数不拆。每个文件一个提交；每拆完一个文件跑 pytest。

## 验收命令

```bash
cd go && go test ./internal/...
cd python && uv run --frozen pytest -q
cd python && uv run --frozen ruff check app
bash scripts/check.sh
bash scripts/check.sh --smoke
cd frontend && npm test && npm run build
```

Go 代码变更后：`go build -o .local/v2-gateway ./cmd/gateway`、`strings` 核对新符号、
`systemctl restart ecommerce-rag-dev`、核对进程启动时间。
Python 变更后：重启 ai，核对进程启动时间晚于最后提交。

## 不做

- 不做顾客端 `can_copy` 统一（需单独设计），只记入 `docs/refactor-input.md`；
- 不做真实模型验证（WP22）；不改提示词/判定；不调棘轮/基线；不重采 smoke；
- 不用 `git add .`；不提交/修改/删除 `scripts/review/`；不 push。

## 实际交付

- 保留策略：`insertSuggestionTurn` 在同一事务末尾调用 `pruneSuggestionTurns`；N=50，
  按 `turn_no` 保留最新；删除 `turn_decision` 走 `admin.Store.DeleteTurnDecisions`，
  其余依赖在 `store_suggestion.go` 内按 FK 顺序显式删除，并带 `tenant_id`。
- FK 检查：依赖表均无 `ON DELETE CASCADE`；未改 FK。新增 `0046` 只授予
  `turn_decision` / `retrieval_trace` 的 DELETE，已应用 dev、test-integration、
  eval 三库（sha256 全匹配）并通过 from-zero（46 files）。
- SSE：基线 `dbda785` 已把 `completed.can_copy` 接到 `canCopy`；本轮只补回归断言，
  确认 DONE 有/无引用、mock 与 ASKING 的 completed 帧和 `customer_reply` 一致。
- 长函数：拆完 11 个文件中的 14 个 >80 行函数（validate_manifest、embed、rerank、
  _call_once、_fetch_once、search、build_provider_chains、bundle_dataset、
  store_artifact_bundle、Submit、_version、safe_extract_zip、parse_persisted_package、
  _claim 等）；`provider_understand_node` 开工即 52 行，未改。当前 `app/` 无 >80 行函数。
- 字面量 multiset：before 4832 / after 4827；removed 仅
  `request_id`、`x-request-id`、`request-id`、`id`（均为键名，来自删除的死 helper），added 0。
- 测试：`go test ./internal/...`（带 `V2_INTEGRATION_ENV`）全绿；
  `python pytest -q` **402 passed / 30 skipped**；`ruff check app` PASS。
- 门禁：`check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**；`--smoke`
  **45 PASS**，五条 smoke 与 `.local/w2/` 逐字节一致。
- 前端未改：`npm test` **49 passed**，`npm run build` PASS，JS gzip **79.54KB**。
- 重启：Go 重建 `.local/v2-gateway`，`strings` 命中 `DELETE FROM turn_decision`；
  `ecommerce-rag-dev` 重启后 Go/Python 进程启动时间晚于最后代码提交（见汇报验证结果）。

## 未覆盖路径

- 保留策略的删除**不含 FK 变更**；用显式 DELETE + 0046 权限补齐。删除旧 turn 会
  丢掉对应 `turn_idempotency` 行，旧幂等键不能重放（新请求不受影响）。
- **真实模型未验证**（WP22）；只跑 mock/离线 pytest 与 DB 生命周期测试。
- 顾客端 `can_copy` 统一未实现，已记入 `docs/refactor-input.md` 第 17 条。
- Python 只做了 AST 行数与字符串 multiset 静态对比；没有逐函数 coverage 对比。
- 未跑 `--db` 全层（迁移已单独三库应用、sha256 与 from-zero 验证）。
- 外部提交 `81ff2e0`（0032 文档）出现在本轮 HEAD，本 agent 未修改其内容。
