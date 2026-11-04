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

- **采用方案 2（有界超额取回）**。最小改动（恢复完整 WHERE，仅删除 `c.id`）
  在 50,000 条测试库上仍为 `Seq Scan + Sort`，因此未采用。
- dense SQL 的内层 ANN 只按 tenant/shop/embedding 排序，limit 为
  `min(dense_candidates * K, 256)`，K 由构造参数 `dense_overfetch` 配置，默认 8；
  外层保留完整 document/version/disclosure/date 授权 WHERE。若授权后条数小于
  请求 limit 且内层 limit 小于 256，提升到 256 重试一次；仍不足则按实际数量返回。
- 最大超额：每轮最多读取 256 条 ANN 候选；最坏情况下执行两次 ANN 查询（初始
  `min(dense_candidates*K,256)`，必要时重试 256）。不改变 sparse 分支、RRF 权重、
  `dense_candidates` 默认或输出 limit。
- 50,000 条 halfvec 集成测试（≥10,000）自然 `EXPLAIN (FORMAT JSON)` 命中
  `Index Scan using idx_chunk_embedding`，未使用 `enable_seqscan=off` 作为验收：
  ```text
  EXPLAIN natural: Index Scan using idx_chunk_embedding
  DENSE_P50_MS_OLD 195.784
  DENSE_P50_MS_NEW 126.477
  ```
- 正确性测试构造 32 条 ANN 更近但已 expired 的 chunk，另放 2 条更远、active 的
  chunk；`dense_candidates=4, K=2` 时第一次取 8 条全部被授权过滤，重试 256 后
  返回 2 条可见 chunk，顺序稳定；测试结束清理：
  `temp_tenants=0`、`temp_chunks=0`。
- `python/tests/test_postgres_retriever.py` 的 dense 排序单测通过；
  `python/tests/test_dense_hnsw.py` 的 3 个集成测试通过（50k plan、确定性、expired top-N
  可见性恢复）。完整 Python 测试（不设 DB env）455 passed、35 skipped。
  使用临时完整 test env 时，既有 `test_postgres_retriever` 的 sparse 探针测试因
  test DB 已有 sparse 行而失败，不能作为本改动的回归通过；未改该测试/数据。
- `bash scripts/check.sh` PASSED 41；`bash scripts/check.sh --smoke` PASSED 45，
  五条 smoke 与 `.local/w2/` 基线逐字节一致。
- 未改 sparse 分支、`SPARSE_RETRIEVAL` 的 legacy 默认、RRF 权重、迁移或索引。

## 未覆盖路径

- HNSW 计划验证使用临时 50,000 条测试数据，不是生产规模；未调 HNSW 参数
  （`m`、`ef_construction`、`ef_search`）。
- 授权过滤在 ANN top-N 之后执行；最多取 256 条，如果这 256 条仍全部被授权过滤，
  结果会少于 limit（不会无限放大）。
- 未运行 `check.sh --db`、`--e2e`、`--all`；未做浏览器验证；未调用真实模型。
- 未验证真实稀疏数据与 HNSW 同时启用；0029 的 legacy 默认和 sparse 分支保持不变。
- 真实大数据量、并发查询和索引维护成本未测量；p50 只是 20 次临时测试库测量。
- `eval/run.sh offline` 的 aligned-53 不经过 `PostgresRetriever`，不作为本次 SQL
  改动的证据。

