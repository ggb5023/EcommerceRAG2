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
| 维度/词表 | 固定并在迁移中注明 |
| 稀疏度上限 | 每 chunk 的 `nnz` 上限（超限的截断策略） |
| 空稀疏 | **写空数组还是 NULL 必须定死**；本提议建议：无法产出稀疏时写 NULL，并让查询侧显式跳过（与"空数组"区分开） |

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
