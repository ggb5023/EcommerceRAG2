# 14 稀疏收尾：权重敏感性与 tokenizer 版本比对

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：`docs/decisions/0029-sparse-retrieval.md` 第四步、WP13

## 目标

WP13 的真实 A/B 在 `SPARSE_RRF_WEIGHT=0.7` 下得到 sparsevec 略差的结果，并留下
两个未闭合项：权重敏感性和查询侧 tokenizer 版本比对。本包只做这两项，不改变
产品默认开关或默认权重。

## 口径

### 权重敏感性

- 固定真实语料与真实查询嵌入，跑
  `SPARSE_RRF_WEIGHT ∈ {0.3, 0.5, 0.7, 0.9}`；每个权重同时跑 legacy 与
  sparsevec，指标为 aligned-53 的 recall@5、MRR、nDCG@5。
- 只通过 WP12 harness 的显式参数传权重；**产品默认仍为 0.7**，不写入配置、
  不切换默认开关。
- 若某权重下 sparsevec ≥ legacy，如实写出；若始终更差，结论为“默认保持 legacy”。
  不为让数字变好而改评测集、候选数或指标公式。

### tokenizer 版本比对

- 查询 embedding 链输出 `sparse_tokenizer_id`，命名规则与写入侧 0038 相同：
  `<provider>:<model>:sparse`；写入侧规则不变。
- `PostgresRetriever` 在 sparsevec 分支只接受
  `chunk.sparse_tokenizer_id = 查询版本` 的 chunk；不一致（含 NULL）的 chunk
  跳过，`last_sparse_skipped_tokenizer` 计数并暴露到检索 trace，不报错、
  不失败关闭。
- 查询版本缺失时保持旧调用兼容（不按版本过滤），但产品链路
  `workflow.py` 必须传版本；单测覆盖一致→参与、不一致→跳过并计数。
- 不做写入侧迁移；不新增 tokenizer 表；不改 0038。

## 改动范围

- `scripts/dev/retrieval-ab.py`：`--sparse-rrf-weight`（仅 harness）与
  `query_sparse_tokenizer` 传递；summary 增加 `skipped_tokenizer_mismatch`。
- `python/app/chains/provider.py`：embedding 输出增加 `sparse_tokenizer_id`。
- `python/app/graph/workflow.py`：校验该字段并传给 `asearch`；trace 暴露跳过数。
- `python/app/retrieval/postgres.py`：sparsevec 版本过滤、跳过计数与自检属性。
- 对应单测；`docs/decisions/0029` 第五步、wp13 指针、wp14 实际交付。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
# 真实模型：每次 4 个权重各一次，--max-requests 300 --max-requests-per-minute 30
# 运行 WP12 harness 的 run，权重 0.3 / 0.5 / 0.7 / 0.9
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不建 sparse 索引（仍 ≤50k）；不改写入侧；不调 `SPARSE_RRF_WEIGHT` 默认值；
  不改评测集；不重采 smoke 基线；不调棘轮。
- 不做 WP15；不 push、不 merge、不删分支。

## 未覆盖路径

- 权重实验只覆盖 aligned-53 与 ab-test 的 74 个 chunk，不代表全量语料。
- tokenizer 比对只覆盖同一 provider/model 的版本串；跨 provider 或换 tokenizer
  的迁移流程仍留后续。
- 无索引的查询计划仍为 Seq Scan；超过 50k chunk 需另开索引决策。
- 真实模型调用受 300 请求 / 30 RPM 上限约束；失败不重试超出额度。
