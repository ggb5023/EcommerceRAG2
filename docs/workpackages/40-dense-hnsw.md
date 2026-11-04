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

（实现与验证后填写。）

## 未覆盖路径

（实现与验证后填写。）
