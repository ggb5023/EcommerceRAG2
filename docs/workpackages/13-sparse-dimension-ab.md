# 13 稀疏维度扩大与真实语义 A/B（0029 c 修订）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：`docs/decisions/0029-sparse-retrieval.md` c、WP12 的真实 provider 探针

## 目标

WP12 已证明 sparsevec 查询分支会执行，但真实 provider 的 sparse token_id 最大
113496，超出 `sparsevec(65536)`，74/74 个真实稀疏向量只能写 NULL。本包把维度
契约扩到 `sparsevec(131072)`，再用真实 provider 重跑 aligned-53，产出**真实语义
A/B**；更差就如实写更差，不把数字调回去。

## 口径

- 维度：`131072 = 2^17`。实测 Bailian `qwen3.7-text-embedding`：`n=203`、
  `min_token_id=141`、`max_token_id=113496`；131072 给出约 15% 余量。
- 触发条件：真实 provider 再出现 `token_id ≥ 131072` 时，按同样流程追加扩维迁移；
  迁移只追加，不改任何已执行文件。`sparsevec` 存储与 nnz 成正比，扩维成本可忽略。
- `nnz ≤ 2048`、空稀疏写 NULL、`sparse_tokenizer_id` 记录方式均不变。
- 不建 sparse 索引；仍受 0029 d 的 ≤50k chunk 规模线约束。
- 真实模型调用上限：`--max-requests 300`、`--max-requests-per-minute 30`，
  每次调用写 `usage_log`（0021）；超限即停，不绕过。
- 评测仍用 aligned-53 的 policy ELIGIBLE 集合；期望文档与指标公式与 WP12 一致。

## 改动范围

1. `docs/decisions/0029-sparse-retrieval.md`：c 的维度契约改为 131072，写明修订
   原因与再次超限的触发条件。
2. `docs/contracts.md`：稀疏写入契约的迁移引用从 0037 改为 0044。
3. `sql/migrations/0044_sparse_vector_wider.sql`：追加迁移，只做 typmod 变更。
4. `python/app/ingest/sparse.py`：`SPARSE_DIMENSION` 改为唯一代码出处 131072；
   worker 不新增第二份数字。
5. `python/tests/test_sparse_write.py`：上界=131072、越界拒绝、往返维度。
6. WP12 的检索 A/B harness：真实 provider 的请求数/速率上限；reembed 后
   断言 74 个 sparse 非空；run 输出真实 A/B。
7. 0029「第四步结果」、wp11/wp12 真实证据、本文件「实际交付」、INDEX/CURRENT。

## 执行顺序

```text
1. 文档先行（0029 c + contracts + 本文件 + INDEX/CURRENT），单独提交
2. 0044 + scratch 从零验证 PASS + dev/test-integration/eval-migration 三库应用与逐文件 sha256
3. sparse.py 上界 + 单测
4. 真实 reembed（74/74）+ run（53 例）+ usage_log 核对
5. 文档收口；WP11/WP12 的 done 判定留给主 agent
```

## 验收命令

```bash
# from-zero 验证（WP07 的脚本，含 0044）
# 三个已执行库：v2-migrate.py --env，再逐文件比对 sha256
cd python && uv run --frozen pytest -q
# 运行 WP12 harness 的 reembed/run，并加 --max-requests 300 --max-requests-per-minute 30
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不修改 0037/0038 或任何已执行迁移；不改 0029 的 nnz/空稀疏/NULL 规则。
- 不建 sparse 索引；不 publish 进 dev 租户；不重采 smoke 基线；不调棘轮。
- 不做 WP14；不 push、不 merge、不删分支。

## 实际交付

- 文档：0029 c 维度改为 `sparsevec(131072)`；contracts 稀疏写入行改指 0044。
- 迁移 0044：`USING regexp_replace(... '/65536$' -> '/131072')`；有数据 scratch
  探针中 3 行（含 NULL）值逐 token 不变、`format_type=sparsevec(131072)`、
  `1-(v <=> q)=1`、NULL 仍为 NULL。`<#>` 实测为 `-0.8900000453`（负内积），
  不是 1；pgvector 的 `<#>` 对未归一化向量不是余弦相似度，见汇报第 3 节。
- from-zero：44 个迁移、校验和、关键表、gateway INSERT 全 PASS。
- 三库：dev/test-integration/eval-migration 均 44/0044、`sparsevec(131072)`，
  逐文件 sha256 0 差异；dev 74 个非 NULL 行全部存活（样例
  `{1:0.317647,3:0.141176,5:0.298039,7:0.784314}/131072`）。
- 写入上界：`SPARSE_DIMENSION=131072` 单处维护；worker 显式传入同一常量；
  新增“上界=131072 / 131072 越界拒绝”测试；`pytest -q` = **390 passed / 30 skipped**。
- 真实 A/B：`ab-test` 74/74 sparse 非空；53/53 例分支模式正确；legacy
  recall@5=0.9811 / MRR 0.9151 / nDCG@5=0.9319；sparsevec
  0.9623 / 0.8947 / 0.9118。sparsevec 净少 1 例命中，逐例原因见 0029。
- 真实调用：15 次（reembed 5、run 4+4、差异分析 2），7601 input tokens；
  `usage_log` 从 42 行/4637 tokens 变为 57 行/12238 tokens，全部记录在 ab-test；
  单次最大 5 请求，低于 300 请求 / 30 RPM。
- 真实语料 max token_id=113799 < 131072。

## 未覆盖路径

- 真实 A/B 只覆盖 aligned-53 与 ab-test 的 74 个 chunk，不代表线上全量语料。
- 查询侧 tokenizer 版本比对仍未实现（WP11 遗留）。
- 无索引的查询计划仍为 Seq Scan；超过 50k chunk 需另开索引决策。
- 真实模型调用受本轮 300 请求 / 30 RPM 上限约束；若中途失败，保留 usage_log，
  不重试超出额度。
