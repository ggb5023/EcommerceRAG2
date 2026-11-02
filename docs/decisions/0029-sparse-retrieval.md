# 0029 稀疏检索：写入契约、A/B 与上线条件

- 状态：提议
- 日期：2026-10-29
- 关联：[0004](0004-retained-invariants.md)、[0018 迁移](../../sql/migrations/0018_v2_retrieval_indexes.sql)

## 背景（现状核查结论）

| 面 | 现状 | 位置 |
| --- | --- | --- |
| 写入 | **没有任何地方写 `chunk.sparse_embedding`**，该列恒为 NULL；provider 能解析 `SparseWeight` 但产物无人落库 | `sql/migrations/0001_init.sql:139`、`python/app/providers/aliyun_bailian.py:722-760`、`python/app/ingest/database.py`（0 命中） |
| 查询 | 所谓"稀疏路径"其实是文本启发式：`GREATEST(similarity(embed_text, q), 查询词命中率)`，与 `sparse_embedding` 无关；无开关，`sparse_candidates` 是函数签名里的硬编码默认值 | `python/app/retrieval/postgres.py:271-278`、`:113` |
| 融合 | `0.7 / (60 + sparse_rank)` 与 `1.0 / (60 + dense_rank)` 均为常量，无依据记录 | `python/app/retrieval/postgres.py:29`（`SPARSE_RRF_WEIGHT`，本次仅命名） |
| 索引 | `sparse_embedding` 上无任何索引；现有索引是 `chunk(embed_text gin_trgm_ops)` 与 `chunk(meta_json)` | `0018_v2_retrieval_indexes.sql:11`、`0001_init.sql:311` |

代价：稀疏分支对**授权范围内每个 chunk** 做 `similarity()` + 逐词 `strpos`，`similarity()` 不走 GIN 加速，随 chunk 数线性增长。

> 实测（2026-10-30）：mock 替身已具备 dense&sparse 契约（每条 4 个非零项，token_id 落在 0..127），
> 因此离线环境可覆盖「写入 → 探针翻牌」整条链路，无需真实模型调用。

> 流程教训：已执行的迁移文件不可修改——AI 服务启动时比对 `schema_migration` 的 sha256，
> 任何字节变动都会导致服务拒绝启动（0037/0038 曾因此把 dev 栈打挂一次）。补 advisory lock 这类改动必须新写迁移。

## 决定（提议）

引入真正的稀疏检索，但**分期上线**：先建契约与基线，再实现，且必须能 A/B。

### 四条设计约束

**a) 兼容与对照**：新路径由 `SPARSE_RETRIEVAL=legacy|sparsevec` 切换（默认 `legacy`），
旧的文本启发式**至少保留一个里程碑**用于对照；两条路径的返回结构必须一致，便于同一评测脚本消化。

**b) 基线**：切换前在冻结评测集上记录 legacy 的 `recall@5` / `MRR` / `nDCG@5`（当前离线基线 recall@5=0.981、mrr=0.981），
切换后同集对比并如实报告。**不允许以"不低于 0.981"作为收工条件**——若真实稀疏更差，结论就是更差，需要解释原因
（分词、词表、权重、数据集特性），而不是把数字调回去。

**c) 写入契约**（Go/Python 两侧契约测试钉住，纳入 S1 契约表）：

| 字段 | 约定 |
| --- | --- |
| tokenizer 版本 | 写入时记录，查询侧按版本比对；跨版本不混用 |
| 维度/词表 | 固定为 `sparsevec(131072)`（迁移 0044，`2^17`）；token_id ∈ `[0,131072)` |
| 稀疏度上限 | 每 chunk 的 `nnz` 上限（超限的截断策略） |
| 空稀疏 | **写空数组还是 NULL 必须定死**；本提议建议：无法产出稀疏时写 NULL，并让查询侧显式跳过（与"空数组"区分开） |

**2026-11-02 维度修订（WP13）**：原 `sparsevec(65536)` 由已执行的 0037 固定，
不改历史文件；0044 追加为 `sparsevec(131072)`。修订依据是 Bailian
`qwen3.7-text-embedding` 实测 `n=203`、`min_token_id=141`、`max_token_id=113496`，
65536 无法容纳，真实写入会全部退化为 NULL。`131072 = 2^17` 对当前最大值提供约
15% 余量。若真实 provider 再出现 `token_id ≥ 131072`，按同样流程追加扩维迁移
（只追加、不改已执行文件）；`sparsevec` 存储与 nnz 成正比，扩维成本可忽略。

**d) 索引与代价**：默认无索引时，仅在 **≤ 50k chunk/租户** 的规模上线；超过必须带索引
（`sparsevec` 的 HNSW/IVFFlat 需 pgvector ≥ 0.7，或倒排 GIN 方案），索引选型与迁移在实现批次内给出并实测。

## 六块缺口（实现清单）

1. **写入**：ingest 侧把 `SparseWeight[]` 序列化为 `sparsevec` 写进 `chunk.sparse_embedding`，按 (c) 的契约落 tokenizer 版本等元数据。
2. **查询**：新增真正的稀疏相似度分支（`<#>`/`<=>`），替代现有文本启发式；保留授权、时间窗、披露过滤。
3. **融合**：给出权重依据（离线 A/B 结果），把 `SPARSE_RRF_WEIGHT` 接到配置。
4. **索引**：选型 + 迁移 + 实测查询计划。
5. **开关与降级**：`SPARSE_RETRIEVAL`、`sparse_candidates` 接配置；稀疏不可用时退回纯稠密。
6. **评测**：按 (b) 采集基线并对齐；`aligned retrieval` 的 53 例与冻结 60 例集都要跑。

## 排期

**S1（契约表）完成后启动实现**：稀疏写入/查询正是契约表要覆盖的"跨进程字段"，先建表再改实现，两侧都有测试兜着。
**不插进 S0**：S0 的目标是让 e2e 可用，本身尚未收尾。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 维持现状（文本启发式当稀疏） | [pg_trgm](https://www.postgresql.org/docs/current/pgtrgm.html) | 名不副实：`sparse_embedding` 列空置，启发式无法表达词权重，且逐行 `similarity()` 不可索引加速 |
| 用 BM25 全文检索替代稀疏向量 | [tsvector](https://www.postgresql.org/docs/current/datatype-textsearch.html) | 中文分词与权重需要额外组件，且与 provider 已有的 `SparseWeight` 产物不衔接；可作为 (b) 的对照基线之一 |
| 直接切到 `sparsevec` 且不保留 legacy | [pgvector sparsevec](https://github.com/pgvector/pgvector#sparse-vectors) | 无法做 A/B，遇到质量回退没有回退路径；违反约束 a |

## 实施教训

a) **改代码后必须重启服务再验证**：`to_literal` 的 1-based 修复提交于 09:13，而 worker 进程启动于 09:00，
   修复后第一次导入仍报 `parse_failed`——验证的是旧进程。规则：任何 ingest/retrieval 代码改动，
   验证前先重启并确认进程启动时间晚于提交时间。
b) **验证性导入不要发布**：用 `--publish` 造验证数据会让该文档进入 active 集合，改变检索结果并污染 smoke 基线
   （本轮 answer 的 evidence 从 `db-chunk-8` 变成 `db-chunk-8 + db-chunk-69`，`answer_chars` 682 → 952）。
   规则：只为验证写入时省略 `--publish`；chunk 在 ingest 阶段就已落库，不需要发布。

实测（当时）：`max(token_id)=7`、`nnz=4`、`dims=65536`，均远低于当时的上限
（2048 / 65536）；WP13 已把维度扩到 131072，见上面的维度修订。
c) **Go 侧改动的验证前置**：先 `go build` 再重启，并用 `strings .local/v2-gateway | grep -c <新符号>` 确认二进制包含改动；
   否则 smoke 通过只证明旧二进制还能跑。同理，新建表的迁移必须同时给运行时角色授权，
   否则写入只会留下一条被 `log.Printf` 吞掉的 permission denied。

## 第三步结果（实测，2026-11-02）

**实现**：`SPARSE_RETRIEVAL=legacy|sparsevec`（默认 `legacy`）开关；`SPARSE_RRF_WEIGHT` 接配置（默认 0.7）；
`PostgresRetriever.search` 新增 `query_sparse` 入参，sparsevec 分支用 `chunk.sparse_embedding <#>`
查询 sparsevec 替代文本启发式；查询无稀疏权重或列全 NULL 时退回纯稠密；
`last_sparse_mode` 报告 `text_heuristic|dense_only|sparsevec` 实际模式。

**基线 vs sparsevec**（`bash eval/run.sh offline`，两套数字完全相同）：

| 模式 | aligned-53 recall@5 | mrr | ndcg@5 | frozen-60 |
| --- | --- | --- | --- | --- |
| legacy | 0.981 | 0.981 | 0.981 | PASS |
| sparsevec | 0.981 | 0.981 | 0.981 | PASS |

**结论**：两套数字完全一致，**不是**因为 sparsevec 与 legacy 等质，而是因为 `run_aligned_retrieval.py`
的 `model_version` 是 `deterministic-keyword-v1`——它用纯分词重叠对合成语料打分，**不经过
`PostgresRetriever`**，因此 `SPARSE_RETRIEVAL` 对该评测无任何影响。真实 sparsevec 的召回对比需要
带真实 sparsevec 语料的线上检索 harness，本机（mock 离线档）不具备，本步未产出真实 A/B 结论。

**模式自检（本机 dev 库）**：`legacy → text_heuristic`；`sparsevec + 查询有稀疏 + 列非空 → sparsevec`；
`sparsevec + 查询无稀疏 → dense_only`。dev 库 70 个 chunk 中仅 1 个 `sparse_embedding` 非空
（`mock:mock-embedding:sparse`）。

**查询计划**：`chunk.sparse_embedding` 无索引，sparsevec 分支对 `chunk` 做 Seq Scan
（`Filter: sparse_embedding IS NOT NULL`，rows≈68）。与 d) 的规模线一致：≤50k chunk 可接受，
超线再评估 HNSW/IVFFlat 或倒排索引迁移。

**未闭合**：查询侧 tokenizer 版本比对未实现（查询链不回传 `sparse_tokenizer_id`）；
写入侧版本已记录，跨版本不混用的完整闭环留待后续。

## WP12 真实检索 A/B（2026-11-02）

WP11 的离线数字不经过检索实现，不能证明分支执行。WP12 建了专用租户 `ab-test`
（`ab-shop`），把 37 篇对齐语料走真实 ingest 管道写入，再对 aligned-53 的同一批
查询直接调用 `PostgresRetriever.asearch`，只切换 `SPARSE_RETRIEVAL`。

**分支执行**：53/53 例 legacy 的 `last_sparse_mode=text_heuristic`，sparsevec 的
`last_sparse_mode=sparsevec`；降级计数 0。过程中发现并修复 WP11 的 sparsevec SQL
参数错位（8 个占位符、9 个参数），该缺陷会让真实 sparsevec 调用每次都抛
`ProgrammingError`，此前没有测试覆盖到。

**mock 语料 A/B**（同一 scope、同一候选数、`SPARSE_RRF_WEIGHT=0.7`）：

| 模式 | recall@5 | MRR | nDCG@5 |
| --- | --- | --- | --- |
| legacy | 0.6981 | 0.3783 | 0.4575 |
| sparsevec | 0.1132 | 0.0343 | 0.0535 |
| 差值 | -0.5849 | -0.3440 | -0.4040 |

这组数字**不是质量结论**：mock tokenizer 的稀疏权重是内容哈希字节，不携带语义。
它证明的是 SQL 分支、降级判断和 RRF 融合真的执行，且两路径返回结构一致。

**真实 provider 阻塞**：用 `providers.env` 的 Bailian `qwen3.7-text-embedding`
直接探针返回 203 个稀疏项，`min_token_id=141`、`max_token_id=113496`；
`to_literal` 对 `token_id=97124` 报 `outside [0,65536)`。`reembed` 对 74 个 chunk
的真实稀疏向量全部被拒绝（`missing_sparse=74`），按本决策的降级规则只能写 NULL。
因此 **`sparsevec(65536)` 与真实 provider 的词表不兼容，真实语义 A/B 无法进行**。
要闭合该步必须先决定：扩大 sparsevec 维度（新迁移）、换 provider/词表，或接受
真实环境 sparse 永远 NULL。该决策不属于 WP12 授权范围；WP11 不能转 `done`。

**未覆盖**：真实语义质量、查询侧 tokenizer 版本比对、稀疏索引计划。

## 第四步结果：真实语义 A/B（WP13，2026-11-02）

**维度落地**：迁移 0044 用 `USING regexp_replace(... '/65536$' -> '/131072')`
把 `chunk.sparse_embedding` 扩到 `sparsevec(131072)`；dev/test-integration/
eval-migration 三库均为 44 条迁移 / `sparsevec(131072)`，逐文件 sha256 无差异。
dev 原有 74 个非 NULL 稀疏行全部存活，值逐 token 不变；真实语料 max token_id
实测 113799，Bailian 探针 max 113496，均低于 131072。

**真实 A/B**：`ab-test` 的 74/74 chunks 用 Bailian `qwen3.7-text-embedding`
重新嵌入（dense 与 sparse 一起），legacy 与 sparsevec 使用同一批真实查询嵌入、
同一 scope、同一候选数、`SPARSE_RRF_WEIGHT=0.7`。53/53 例两分支均命中预期模式，
无降级。

| 模式 | recall@5 | MRR | nDCG@5 |
| --- | --- | --- | --- |
| legacy（真实 dense + 文本启发式稀疏） | 0.9811 | 0.9151 | 0.9319 |
| sparsevec（真实 dense + Qwen 稀疏） | 0.9623 | 0.8947 | 0.9118 |
| 差值 | -0.0189 | -0.0204 | -0.0202 |

53 例中 51 例的 top5/排名发生变化，但只有 3 例命中结果翻转：
`syn-019` legacy 未命中 → sparsevec 命中；`syn-036`、`syn-038` legacy 命中 →
sparsevec 未命中。净效果是 sparsevec 少 1 例命中、MRR 略低。

**原因分析（不改权重、不调参）**：

- 两条路径的**稠密通道完全相同**（同一真实 dense 向量），差异只在稀疏通道，
  因此这是干净的稀疏对照。
- `syn-019` 期望文档 `aligned-fact-inventory-earbuds-v1` 的 dense 排名为 2；
  legacy 的文本启发式没有给它稀疏排名，RRF 被其他有稀疏排名的文档挤出 top5；
  Qwen 稀疏把它排到第 1，sparsevec 恢复命中。
- `syn-036` 期望文档 `aligned-web-waterproof-v1` 的 dense 排名为 7；
  legacy 的文本启发式把它排到稀疏第 1，RRF 将其抬到 top3；Qwen 稀疏排第 8，
  RRF 后落到文档第 7，跌出 top5。`syn-038` 同理（dense 6、legacy 稀疏 5、
  Qwen 稀疏 7）。
- aligned-53 的查询与源文档共享大量字面词；legacy 的 `similarity + strpos`
  恰好放大了这种精确词面重合。Qwen 稀疏是语义-词法权重，反而把
  routing/policy/refusal 等共享模板词的文档排得更靠前，挤压了期望文档。
- `SPARSE_RRF_WEIGHT=0.7` 是固定值；本轮没有为了提升数字而调权重。若后续要
  改权重或换 tokenizer，必须按 (b) 另开实验并如实报告。

**结论**：真实稀疏在本数据集上**略差**（recall -1 例、MRR -0.020），不是
“不低于基线”才收工；legacy 仍是默认。结论只覆盖 aligned-53 与 ab-test 的
74 个 chunk，不代表生产全量语料。

**成本**：真实调用共 15 次（reembed 5、run 4+4、差异分析 2），输入
7601 tokens，每次均写 `usage_log`；单次最大 5 请求，低于 300 请求 / 30 RPM 上限。

**未覆盖**：查询侧 tokenizer 版本比对、稀疏索引计划、权重/其他 tokenizer 的
敏感性实验。

## 第五步收尾：权重敏感性与 tokenizer 比对（WP14，2026-11-02）

### 权重敏感性（真实模型，aligned-53）

固定真实语料与查询嵌入，仅改变 harness 的 `SPARSE_RRF_WEIGHT`；产品默认仍为
0.7，未写入配置、未切换。

| 权重 | legacy recall@5 | legacy MRR | legacy nDCG@5 | sparsevec recall@5 | sparsevec MRR | sparsevec nDCG@5 |
| --- | --- | --- | --- | --- | --- | --- |
| 0.3 | 0.9811 | 0.9220 | 0.9368 | 0.9623 | 0.9104 | 0.9237 |
| 0.5 | 0.9811 | 0.9245 | 0.9389 | 0.9623 | 0.9104 | 0.9237 |
| 0.7 | 0.9811 | 0.9151 | 0.9319 | 0.9623 | 0.8947 | 0.9118 |
| 0.9 | 0.9811 | 0.9245 | 0.9389 | 0.9623 | 0.8947 | 0.9118 |

四个权重下 sparsevec 的 recall@5 始终少 1 例（0.9623 < 0.9811），MRR/nDCG 也
始终低于同权重 legacy；**没有任何权重使 sparsevec ≥ legacy**。legacy 自身的
MRR 在 0.5/0.9 最好（0.9245），0.7 为 0.9151；本轮只测不切，默认保持 0.7。
最终开关策略：**默认 `SPARSE_RETRIEVAL=legacy`，sparsevec 保留为可切换的
对照实现**；改变默认需要新的质量证据与单独决策。

### tokenizer 版本比对

查询 embedding 链现在输出 `sparse_tokenizer_id`
（`<provider>:<model>:sparse`，与写入侧 0038 同一函数），
`PostgresRetriever` 在 sparsevec 分支用 `chunk.sparse_tokenizer_id = 查询版本`
过滤；不一致（含 NULL）的 chunk 跳过，`last_sparse_skipped_tokenizer` 计数，
并进入 `provider_trace.skipped_tokenizer_mismatch`。不报错、不失败关闭。
ab-test 的 74/74 chunks 版本为 `aliyun-bailian:qwen3.7-text-embedding:sparse`；
WP14 的真实 0.7 复跑 skipped=0，指标与 WP13 逐位一致。版本不一致路径由单测
覆盖（跳过且计数），写入侧规则未改。

**成本**：WP14 真实调用 20 次（4 权重 × 4 + 0.7 复跑 4），输入 7020 tokens，
全部写 `usage_log`；单次最大 4 请求，低于 300 请求 / 30 RPM。

**仍未覆盖**：其他 tokenizer/provider 的迁移流程、跨版本回填、稀疏索引计划。
