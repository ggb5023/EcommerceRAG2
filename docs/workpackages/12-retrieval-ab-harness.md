# 12 检索 A/B harness（补齐 0029 第三步证据）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：`docs/decisions/0029-sparse-retrieval.md`、WP11（`11-sparse-query-ab.md`）

## 目标

WP11 的 aligned-53 评测是纯关键词重排，不经过 `PostgresRetriever`，切换
`SPARSE_RETRIEVAL` 数字不变，不能证明 sparsevec 分支真的执行。本包建一条
**经过真实检索实现**的 A/B 通道：

1. 在 dev 库建专用租户（`ab-test` / `ab-shop`），只在该租户导入 37 篇对齐语料并发布；
2. 用 `PostgresRetriever.asearch` 对 aligned-53 的同一批查询分别跑
   `legacy` 与 `sparsevec`，同一 scope、同一 `SPARSE_RRF_WEIGHT`、同一候选数；
3. 输出按文档的 recall@5 / MRR / nDCG@5、逐查询差异与 summary，写进 0029。

## 口径

- 只跑对齐集中 **policy ELIGIBLE 的 53 例**；7 例拒绝/越权例不进入指标，
  与 `run_aligned_retrieval.py` 的 measured 集合一致。
- 期望文档取 approved alignment 的 `case_to_source_documents`；检索结果按
  `document_id`（`document.logical_key`）去重后取前 5 个文档。
- `recall@5` = 前 5 中命中任一期望文档的查询比例；`MRR` = 首个命中排名倒数；
  `nDCG@5` = 二元相关、理想前 5 全相关。公式与离线评测一致。
- 两次运行使用同一查询嵌入（同一 provider、同一文本），唯一变量是
  `SPARSE_RETRIEVAL`。`last_sparse_mode` 必须是 `text_heuristic` / `sparsevec`，
  不是降级态，否则该轮报 `NOT_RUN`。
- 语料只写 `ab-test`，不 publish 到 dev 租户；不新增迁移或索引。

## 不新增的输入

- 不改 `eval/` 冻结用例、答案点、alignment、语料；
- 不重采 smoke 基线、不调棘轮/预算；除修复 sparsevec SQL 参数缺陷外不改产品检索语义；
- 不做 WP13；不 push。

## 改动范围

- `scripts/dev/retrieval-ab.py`：seed + reembed + run 三个子命令；直连 dev retrieval DSN。
- `python/app/retrieval/postgres.py`：删除 sparsevec SQL 多传的查询字面量；
  `python/tests/test_sparse_query_mode.py`：占位符/参数数量回归测试。
- `docs/workpackages/12-retrieval-ab-harness.md`：本文件。
- `docs/workpackages/INDEX.md`、`docs/workpackages/CURRENT`：状态指针。
- `docs/decisions/0029-sparse-retrieval.md`、`docs/workpackages/11-sparse-query-ab.md`：
  追加真实 A/B 数字与结论（结果提交）。
- `scripts/check/check-docs-drift.py`：路径段边界修复（单独提交）。

## 语料导入路径

`retrieval-ab.py seed`：

1. 以 `V2_MIGRATION_DSN`（仅本地 dev）幂等创建 `tenant(external_id=ab-test)`、
   `shop(ab-shop)`、`app_user(ab-user)` 与 `merchant_admin`/`knowledge_reviewer` grant；
2. 从 `/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/documents.jsonl`
   重建 37 篇 Markdown，用同一签名 key 生成 `ingest` audience scope，直接调用
   Python `IngestService.Submit` 走真实 ingest 管道；
3. 等 `ingest_job` 到 `awaiting_review`，按冻结 disclosure 逐篇写入 review/publish
   状态；`unclassified` 那篇保持 rejected（与生命周期验收一致）；
4. 断言 `chunk.sparse_embedding IS NOT NULL` 的 chunk 数 > 0，打印计数。

seed 只写 `ab-test`，不触碰 8080 dev 栈的租户或 smoke 基线。

## 验收命令

```bash
python3 scripts/dev/retrieval-ab.py seed
python3 scripts/dev/retrieval-ab.py run
python3 scripts/dev/retrieval-ab.py run --limit 5      # 可重复、确定性
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
python3 scripts/check/check-docs-drift.py
```

## 实际交付

- `scripts/dev/retrieval-ab.py`：`seed` / `reembed` / `run` 三个子命令。
  `seed` 用 migration 角色幂等建 `ab-test`/`ab-shop`/`ab-user`，以 gRPC
  `Submit` 走真实 ingest 管道，再按冻结 disclosure 在租户内审批/发布；
  36 篇 active，`unclassified` 1 篇 withheld；74 chunks、73 个 sparse 非空。
- `run`：53 例全部 measured，legacy 与 sparsevec 的 `last_sparse_mode`
  分别为 `text_heuristic`/`sparsevec`，wrong_mode 均为 0。
- **发现并修复一个 WP11 缺陷**：sparsevec SQL 只有 8 个占位符却传了 9 个参数
  （查询字面量多传一次），真实调用必抛 `ProgrammingError`。已删多余参数并加
  `test_sparsevec_sql_placeholder_count_matches_parameters` 回归测试。
- mock 语料 A/B（同一 scope、同一 `SPARSE_RRF_WEIGHT=0.7`）：

  | 指标 | legacy | sparsevec | 差值 |
  | --- | --- | --- | --- |
  | recall@5 | 0.6981 | 0.1132 | -0.5849 |
  | MRR | 0.3783 | 0.0343 | -0.3440 |
  | nDCG@5 | 0.4575 | 0.0535 | -0.4040 |

  53/53 例结果均发生变化。mock tokenizer 的稀疏权重是哈希字节，不是语义，
  因此这组数字只证明分支执行，不能当作生产质量结论。

## 真实 provider 探针（阻塞项）

用 `providers.env` 的 Bailian `qwen3.7-text-embedding` 直接探针：

```text
n=203 min_token_id=141 max_token_id=113496
rejected: token_id 97124 is outside [0,65536)
```

`reembed` 对 74 个 chunk 的真实稀疏向量全部被 `to_literal` 拒绝（`missing_sparse=74`），
按 0029 的降级规则只能写 NULL。也就是说当前 `sparsevec(65536)` 与真实 provider 的
稀疏词表不兼容，**真实语义 A/B 无法在本包内完成**；需要新迁移改维度或换 provider，
属于 WP13 的决策，不在 WP12 授权内。真实探针的用量已写入 `usage_log`。

## 未覆盖路径

- 真实语义 A/B：被上述维度阻塞；mock A/B 只证机制，不证质量。
- 语料发布因同一数据库只允许一个 gateway 持有 advisory lock（742019,1），
  seed 用 SQL 写入与产品发布相同的 review 字段（reviewed_hash/reviewed_by/
  fencing_epoch/active_version_id），未走 Go admin 发布命令；只作用于 ab-test。
- 不覆盖查询侧 tokenizer 版本比对（WP11 遗留）。
- 不覆盖稀疏索引计划；当前规模仍走 seq scan（0029 d 的 ≤50k 线）。
