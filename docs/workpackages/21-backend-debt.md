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
  因此不追加 0046，不修改历史迁移；在 `store_suggestion.go` 内按依赖顺序显式删除：
  `turn_decision` → `citation` → `evidence_snapshot` → `customer_reply` →
  `sse_event` → `retrieval_trace` / `routing_trace` / `tool_call_trace` →
  `feedback` → `message` → `turn_idempotency` → `conversation_execution` → `turn`。
- 所有删除都带 `tenant_id`；只写 `turn*` / `conversation_execution` / `message` /
  `sse_event` 等 gateway 表，走 ownership 语义。
- 保留策略删除旧 `turn_idempotency` 行，因此被清理的旧幂等键不再能重放；新请求不受影响。
- 测试：同一 suggestion conversation 连续插入 52 个 turn，断言只保留最新 50 个，
  最旧 turn 及其 evidence/citation/message/sse_event/turn_decision 均无残留。

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

（完成后填写）

## 未覆盖路径

（完成后填写）
