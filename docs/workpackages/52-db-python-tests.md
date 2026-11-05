# 52 P2：修复 db-python 测试冲突并接入 CI

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[WP51 §未覆盖路径](51-ci-db.md)、[0029 稀疏检索](../decisions/0029-sparse-retrieval.md)、
> [agent-entry §4](../agent-entry.md)

## 目标

修复 WP51 §4 记录的两个既有测试冲突，让 `test_v2_ingest_database.py` 与
`test_postgres_retriever.py` 在同一个 CI test 库上全绿，并把 `db-python` 接入
`.github/workflows/check.yml` 的 `db` job。不改变产品检索行为。

## 背景

WP51 的 CI `db` job 只给 `test.env` 放了 migration/gateway DSN，`db-python`
因此报 UNCONFIGURED。两个失败点：

1. `test_sparse_probe_runs_against_the_real_database` 依赖库中是否已有 sparse 行，
   且调用 `search` 时没有传 `query_sparse`/`query_sparse_tokenizer`；在
   `populated=true` 时它要求 `sparsevec`，实际只能得到 `dense_only`，默认 legacy
   时又得到 `text_heuristic`。
2. `test_postgres_retriever_reads_active_reviewed_chunks_and_filters_scope` 的
   `dense_rank == 0` 断言与当前产品语义不符：`dense_candidates=2` 会按
   `dense_overfetch=8` 取候选，并在授权结果不足 `limit` 时重试到 256，中文 chunk
   可能以 `dense_rank=3` 被召回；真正要证明的是稀疏通道把它排到第 1 并参与 RRF。

## 改动

### 测试

- 重写 sparse 探针测试：每个模式显式构造独立 `PostgresRetriever`，传入
  `query_sparse`/`query_sparse_tokenizer`，分别断言 `text_heuristic` /
  `sparsevec` / `dense_only`；不再依赖全局首次探针的隐式状态。
- 新增混合库用例：同一 document 同时有带 `sparse_embedding` 的 chunk 和纯 dense
  chunk，先写入再 probe，覆盖 WP51 的“先写 sparse 再 probe”场景。
- retriever 用例显式固定 `sparse_retrieval="legacy"` 与 `sparse_rrf_weight=0.7`；
  把 `dense_rank == 0` 改为稳定断言：命中内容、`sparse_rank == 1`、RRF 使用实际
  `dense_rank` 计算。稀疏召回可能压过 dense top-1，`raw_score` 改为 [-1, 1]
  边界断言；无稀疏召回的用例仍保留精确 `raw_score == 1`。

### CI

- `db` job 的 `test.env` 增加 `V2_INGEST_DSN` 与 `V2_AI_DSN`，指向
  `ecr_v2_test_ci`，沿用现有随机 DSN 生成方式；不新增凭据。
- 本机等价 DB 层实测 `db-python` 由 UNCONFIGURED 变为 PASS（43 passed），
  只剩 `db-import-batch` / `db-import-customer` 两项 UNCONFIGURED。

## 验证结果（2026-11-05）

pinned pgvector 16 容器（`eac621...`，pgvector 0.8.6）+ 新建
`ecr_v2_test_ci` / `ecr_v2_ci_scratch`，迁移 51 条后：

```text
# pytest tests/test_v2_ingest_database.py tests/test_postgres_retriever.py -q
43 passed, 1 warning in 9.13s

# bash scripts/check.sh --db
PASS db env health: migrations 51/51, documents 3, versions 3
PASS migrations from zero: 51 files, checksums, key tables and gateway INSERT grant
[db] db-python ... 43 passed, 1 warning
PASSED 47
UNCONFIGURED 2   # db-import-batch / db-import-customer
FAILED 0

# bash scripts/check.sh
PASSED 42 / UNCONFIGURED 0 / FAILED 0

# bash scripts/check.sh --smoke
PASSED 45 / UNCONFIGURED 0 / FAILED 0

# cd go && go test -count=1 ./...
23 package results, 0 FAIL
```

workflow YAML 解析通过（`fast` / `db`），所有 `run:` 块 `bash -n` 通过；
凭据 grep 只命中 `secrets.token_hex`、变量名和无密码的本地 DSN。

## 验收命令

```bash
# pinned 容器新库上的 Python DB 套件
cd python && V2_INTEGRATION_ENV=... uv run pytest \
  tests/test_v2_ingest_database.py tests/test_postgres_retriever.py -q

# 本机等价 DB 层（pinned pgvector 16 容器）
bash scripts/check.sh --db

# 快速层与 smoke
bash scripts/check.sh
bash scripts/check.sh --smoke

# Go 全量测试、YAML 解析、无凭据痕迹
cd go && go test ./...
python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/check.yml"))'
grep -Rni 'password\|dsn\|secret' .github/workflows/check.yml
```

## 不做

- 不改产品检索行为；若发现必须改产品才能通过，停下报告。
- 不改评测集、迁移、其它棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- 远端 GitHub Actions 是否启用、首跑是否全绿，无法在本机验证。
- smoke / e2e 仍为本地合并前必跑项，CI DB job 不启动应用栈。
- 不验证真实模型、OSS 或 Keycloak。
- `db-localauth` 仍没有 `-tags integration`，本轮不改这个门禁语义。
- dense overfetch/重试与 sparsevec 降级仍是现有产品语义；测试改为跟随它，
  没有改产品行为。CI `test.env` 不设 `SPARSE_RETRIEVAL`，retriever 用例按
  legacy 固定，三种 sparse 模式由测试显式构造。
