# 51 P2：CI 增加数据库层

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P2（2026-11-04 本机私有快照，未入库）、
> [agent-entry §4](../agent-entry.md)、[development 测试分层](../development.md)

## 目标

在 GitHub Actions 的 fast job 之外增加独立 DB job：用固定 pgvector/PostgreSQL
service container 初始化角色/库/扩展，跑迁移和 `scripts/check.sh --db`，让数据库
集成层进入 CI；smoke 仍为本地合并前必跑项，不在本阶段伪装成 CI 已覆盖。

## Workflow 设计

- `.github/workflows/check.yml` 增加 `db` job，`runs-on: ubuntu-24.04`；
  service `postgres` 使用固定 digest 的 pgvector PostgreSQL 16 镜像；
- 安装 toolchains、`postgresql-client`、Python venv；数据库密码在 runner 临时生成，
  不写入仓库；
- 用 `scripts/ops/selfhost-db-init.py` 创建角色、`ecr_v2` 和扩展；另建
  `ecr_v2_test_ci`（Go admin 要求 `ecr_v2_test_` 前缀，Python 要求含 `v2_test`）
  和空的 `ecr_v2_ci_scratch`，迁移 test 库，scratch 留给 migrations-from-zero；
- 0017 迁移只插入 fixture document、不插入 document_version；test 库迁移后给每份
  fixture document 补一条 version，否则 `db-env-health` 会把新库判成坏库；
- 生成 0600 的临时 `test.env` / `import.env`，只给 `test.env` 放 migration/gateway
  DSN；`db-python` 需要 `V2_INGEST_DSN`/`V2_AI_DSN`，当前故意不提供，按
  UNCONFIGURED 处理（原因见 §未覆盖路径）；import 测试缺 ingest endpoint 同样
  UNCONFIGURED，不伪造通过；
- 运行 `bash scripts/check.sh --db`，断言 summary `FAILED 0`；不运行 smoke。

## 本机等价验证

先在 digest 对应的 pgvector 16 容器（trust auth，`127.0.0.1:55432`）按 workflow
顺序实跑：`selfhost-db-init.py` → 建 test/scratch 库、扩展、CONNECT 授权 →
`v2-migrate.py` → 补 fixture version → `bash scripts/check.sh --db`，得到
`PASSED 46 / UNCONFIGURED 3 / FAILED 0`（`db-env-health` 51/51、documents 3、
versions 3；migrations-from-zero 51 files 通过；`db-admin-access` 冷缓存通过）。

dev docker PostgreSQL 也建了 `ecr_v2_test_wp51` / `ecr_v2_wp51_scratch` 跑同一套
库/迁移/门禁，summary 相同；dev 镜像的 pgvector 是 0.8.7，而
`selfhost-db-init.py` 要求 0.8.6，所以 dev 上只能按等价步骤建库，不能原样执行该
init 脚本（pinned digest 容器是 0.8.6，已覆盖原样执行）。验证后删除上述临时库，
未改 dev 主库数据。

## 验收命令

```bash
# YAML 解析 + 无凭据痕迹
python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/check.yml"))'
grep -Rni 'password\|dsn\|secret' .github/workflows/check.yml

# 本机等价
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：本机 `check.sh --db` 的 db-init/migrate/db 层输出；文档守卫全过。

## 不做

- 不放真实凭据、DSN、密钥或任何生产地址；不把 smoke 放进 CI 并宣称已跑。
- 不改产品代码、迁移、评测集；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 未覆盖路径

- 远端 GitHub Actions 是否启用无法在本机验证；文件提交后首跑仍需外部确认。
- smoke / e2e 仍为本地合并前必跑项，CI DB job 不启动应用栈。
- 不验证真实模型、OSS 或 Keycloak。
- **db-python 未进入 CI**。当前套件在全新库上先是 ingest 测试写入
  `sparse_embedding`，随后 `test_sparse_probe_runs_against_the_real_database`
  在 `populated=true` 时要求 `last_sparse_mode=sparsevec`，但该测试调用 `search`
  不传 `query_sparse`，实际只能得到 `dense_only`；同一文件的 retriever 用例又因
  dense overfetch 重试得到 `dense_rank=3`（断言要求 0）。这是既有产品/测试行为，
  WP51 不改产品代码、不改断言，所以 `test.env` 不提供 `V2_INGEST_DSN`/
  `V2_AI_DSN`，让 `check.sh` 明确报 UNCONFIGURED。本机单独执行证据：
  `test_v2_ingest_database.py` 28 passed；`test_postgres_retriever.py` 13 passed /
  1 failed；合并执行 legacy 40 passed / 2 failed，sparsevec 39 passed / 3 failed。
  需要独立工作包修产品与测试后再把两行 DSN 加回 workflow。
- `db-localauth` 在 `check.sh` 里没有 `-tags integration`，`store_integration_test.go`
  不会编译，当前只能算单元测试项；本轮不改门禁。
- import 测试依赖可达 ingest 和 `/etc/ecommerce-rag/eval-scope.key`，CI 保持
  UNCONFIGURED。
