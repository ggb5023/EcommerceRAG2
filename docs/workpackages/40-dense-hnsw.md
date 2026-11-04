# 40 P1-6 修复：稠密检索走 HNSW

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-6](../audits/2026-11-04-global-audit.md)、
> [0029](../decisions/0029-sparse-retrieval.md)

## 目标

修复 dense SQL 的排序表达式，使 PostgreSQL 能使用 `idx_chunk_embedding`
（HNSW），同时在 Python 侧恢复稳定的 `(distance, chunk_id)` 顺序。只改稠密分支；
不改稀疏分支、RRF 权重、候选数/limit、索引或迁移。

## SQL 与确定性

- `dense_sql` 的 `ORDER BY` 只保留距离：
  `ORDER BY c.embedding <=> %s::halfvec(1024) LIMIT %s`；
- SELECT 的 `distance` 列保留；
- 取回 top-N 后 Python 按 `(distance, chunk_id)` 稳定排序，再进入 RRF 融合；
  距离相同时 chunk_id 顺序与旧行为一致；
- 稀疏 SQL/RRF 权重/limit 不变。

## 索引计划集成测试

新增 `V2_INTEGRATION_ENV` 门控的 Python 测试：

- 临时 tenant/shop、source/document/version、≥10,000 条 1024 维 halfvec chunk；
- 对实际 dense SQL 执行 `EXPLAIN (FORMAT JSON)`，断言出现
  `Index Scan using idx_chunk_embedding`，不是 `Seq Scan + Sort`；
- 同一查询跑两次，断言返回 chunk_id 顺序一致；
- 清理临时行。

若规划器仍自然选择 Seq Scan，只允许测试内 `SET LOCAL enable_seqscan=off`
作诊断，并在 WP 记录“生产计划未自然选中”；诊断结果不得算验收通过。

## 回归与测量

- `bash eval/run.sh offline`：aligned-53 recall@5 / MRR / nDCG 与修复前一致；
- 在 ≥10k 测试集上各跑 ≥20 次 dense 查询，记录修复前后 p50 延迟；
- `bash scripts/check.sh`（41）＋ `bash scripts/check.sh --smoke`（45）。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
bash eval/run.sh offline
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：重启 ai 并核对启动时间晚于最后提交；EXPLAIN 输出、前后 p50、10k 行清理；
`docs/refactor-input.md` 或审计跟进标注 P1-6 dense 已修复；0029 的稀疏默认 legacy
维持不变，不删分支。

## 不做

- 不改 sparse 分支/默认开关、评测集、提示词、RRF 权重或候选数。
- 不新增迁移/索引；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

- `dense_sql` 现在把 ANN 排序放在只按 tenant/shop 过滤的子查询中，外层的
  document/version/disclosure/date 授权条件保持不变；子查询只取 `dense_candidates`
  条，不改变候选数，也不改 Python 侧 `(distance, chunk_id)` 稳定排序。
- 集成测试 `python/tests/test_dense_hnsw.py` 使用 50,000 条 1024 维 halfvec
  （≥10,000 要求），测试库临时 tenant/shop，结束清理并断言无残留：
  ```text
  EXPLAIN natural: Index Scan using idx_chunk_embedding
  DENSE_P50_MS_OLD 243.397
  DENSE_P50_MS_NEW 119.741
  ```
  EXPLAIN 为实际 dense SQL 的 `FORMAT JSON`；自然计划选中 HNSW，未使用
  `enable_seqscan=off` 作为验收。
- 同一 dense 查询跑两次，返回 `chunk_id` 顺序一致；测试结束临时 tenant/chunk
  计数为 0。
- aligned-53（`bash eval/run.sh offline`）：修复前基线 recall@5=0.981132、
  mrr=0.981132、ndcg@5=0.981132；修复后三项仍为
  recall@5=0.981132、mrr=0.981132、ndcg@5=0.981132。
- `bash scripts/check.sh` PASSED 41；`bash scripts/check.sh --smoke` PASSED 45，
  五条 smoke 与 `.local/w2/` 基线逐字节一致。
- 未改 sparse 分支、`SPARSE_RETRIEVAL` 的 legacy 默认 默认、RRF 权重、候选数、迁移或索引。

## 未覆盖路径

- HNSW 计划验证使用测试库临时 50,000 条数据，不是生产规模；未调 HNSW 参数
  （`m`、`ef_construction`、`ef_search`）。
- 外层授权在 ANN top-N 之后执行，因此若 top-N 全被授权过滤，返回数会少于 limit；
  这是本次子查询改写的已知语义边界，未做补候选或二次查询。
- 未运行 `check.sh --db`、`--e2e`、`--all`；未做浏览器验证；未调用真实模型。
- 未验证真实稀疏数据与 HNSW 同时启用；0029 的 legacy 默认和 sparse 分支保持不变。
- 真实大数据量、并发查询和索引维护成本未测量；p50 只是 20 次测试库测量。

