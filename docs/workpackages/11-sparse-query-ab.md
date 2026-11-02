# 11 稀疏查询 A/B（0029 第三步）

> 状态：见 INDEX.md　适用提交：139b8d3　验收：见 §验收命令
> 依据：`docs/decisions/0029-sparse-retrieval.md`（第三步/查询/融合/开关/评测）

## 目标

把 `python/app/retrieval/postgres.py` 的稀疏分支从文本启发式换成真正的
`sparsevec` 相似度查询，用 `SPARSE_RETRIEVAL=legacy|sparsevec` 切换并如实 A/B。

## 口径（0029 抄录，不新增）

- 开关 `SPARSE_RETRIEVAL=legacy|sparsevec`（默认 `legacy`）；`SPARSE_RRF_WEIGHT` 接配置（默认现值 0.7）。
- legacy 分支（文本启发式）判定逻辑一字不改，保留一个里程碑用于对照；两条路径返回结构一致。
- sparsevec 分支：`chunk.sparse_embedding <#>` 查询 sparsevec，替代 `similarity()+strpos`；
  保留授权、时间窗、披露过滤与稠密路径一致；稠密+稀疏经 RRF 融合。
- 降级：sparsevec 但数据不可用（查询无稀疏 / 列全 NULL）→ 退回纯稠密；
  `last_sparse_mode` 报告 `sparsevec|dense_only|text_heuristic` 实际模式。
- 不建新索引、不加迁移（当前 <50k chunk；EXPLAIN 记录进本文件，超 0029 d 规模线再评估）。

## 改动范围

- `python/app/retrieval/postgres.py`：开关、sparsevec 查询分支、`query_sparse` 入参、
  可配置 RRF 权重、降级与探针。
- `python/app/graph/workflow.py`：把查询的 `dense&sparse` 结果里的 `sparse` 传给 `asearch`。
- `scripts/dev/v2_services.py`：`CONFIG_KEYS` 白名单加 `SPARSE_RETRIEVAL`、`SPARSE_RRF_WEIGHT`。
- `docs/development.md`：运行配置一节补这两个键（单一出处）。
- `docs/contracts.md`：稀疏查询契约 ❌ → ✅（同侧，检索实现全在 Python）。
- `docs/decisions/0029-sparse-retrieval.md`：文末追加「第三步结果」。

## 不做

- 不改 0029 写入契约与 0037/0038 迁移；不新增迁移/索引；不碰 ingest 写入侧。
- 不改 legacy 判定逻辑；不改评测集、不重采 smoke 基线、不调任何棘轮
  （除 contracts 的 CONTRACTS_SAME 覆盖度上升，单独提交）。
- 不做 WP12。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
bash eval/run.sh offline          # legacy 与 sparsevec 各一次
python3 scripts/check/check-contracts.py
```

## 未覆盖路径

- **真实模型未跑**：本机是 mock 离线档，`run.sh offline` 的 aligned-53 是
  `deterministic-keyword-v1` 纯分词命中，不经过 `PostgresRetriever`，切换
  `SPARSE_RETRIEVAL` 不影响该数字；sparsevec 分支只做单测与 SQL 构建验证。
- dev 库 70 个 chunk 中只有 1 个 `sparse_embedding` 非空（mock tokenizer），
  没有真实 sparsevec 语料可做端到端召回对比。
- 查询侧 tokenizer 版本比对未实现（查询链不返回 tokenizer_id），写入侧的
  `sparse_tokenizer_id` 已记录，跨版本不混用的完整闭环留给后续。
- `EXPLAIN` 计划需真实 sparsevec 数据；当前空列只能证明无索引时走 seq scan。
