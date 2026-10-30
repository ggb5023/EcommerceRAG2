# 01 稀疏向量写入链路（0029 第二步）

> 状态：active　适用提交：4a625b1　验收：见 §验收命令
> 详细规格：`docs/decisions/0029-sparse-retrieval.md`（本文件只列执行摘要，不复制细节）

## 目标

让 `chunk.sparse_embedding` 真正被写入，并使检索侧报告从 `text_heuristic` 变为 `sparsevec`。

## 已完成

- 迁移 `0037_sparse_vector_dimension.sql`：`sparse_embedding` 定为 `sparsevec(65536)`（已提交，未推送）。
- 检索侧自检探针：`last_sparse_mode` + 一次性 WARN（`python/app/retrieval/postgres.py`）。

## 待做

1. `python/app/ingest/sparse.py`（待建）（新）：`SparseWeight[]` → `sparsevec` 字面量序列化。
2. `python/app/ingest/worker.py`（chunk 的实际写入方，`database.py` 不是）：写入前校验——`token_id ∈ [0,65536)`、`nnz ≤ 2048`、`weight` 有限且 > 0；空列表或 provider 未返回 → **写 NULL**，并在批次结束时记**一条聚合** WARNING（missing 分类 + 总数 + 首个原因），不要在 chunk 循环里逐条打。
3. `python/app/ingest/worker.py`：chunk 插入语句带上 `sparse_embedding` / `sparse_tokenizer_id` 两列（text 绑定后 `::sparsevec`，不拼字符串）。
4. `tokenizer_id` 记产出者身份：`<provider>:<model>:sparse`（词表随模型变，必须能从数据看出是谁产的）。

## 不碰

- 不改 `python/app/retrieval/postgres.py` 的查询分支（查询切换属第三步，需 A/B 开关与评测重跑）。
- 不改稠密向量链路。
- 不改历史迁移。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
cd python && uv run --frozen pytest -q tests/test_sparse_write.py
# 真实导入后断言：
#   SELECT count(*) FROM chunk WHERE sparse_embedding IS NOT NULL;   > 0
#   检索返回 last_sparse_mode == "sparsevec"
```


## 行为变更

写入侧新增列写入（不影响现有检索结果）；检索行为不变。

## 未覆盖路径

- 真实 provider 的 `max(token_id)` 与 `max(nnz)` 需实测记录进 0029；若 `max(token_id) ≥ 65536` 立即停止并上报（typmod 需重定）。
