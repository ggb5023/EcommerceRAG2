# 07 测试分层剩余项（批次 2 / 批次 3 尾项）

> 状态：见 INDEX.md　适用提交：b7ef570　验收：见 §验收命令
> 详细规格：`docs/archive/testing-refactor-plan.md` §4（覆盖率）、§5.1（迁移从零建库）、§5.2（固定等待禁令）、§5.4（覆盖率实装）、§5.5（db-env-health）

## 目标

补齐 `--cov`、迁移从零建库、固定等待禁令、db-env-health 四项，使测试层能自证环境健康。

## 改动范围

- `.github/workflows/check.yml`：push main / PR 跑 fast 层；不跑 `--db` / `--e2e` / `--smoke`。
- `scripts/check.sh`：`--cov` 实装（Go `-coverprofile` + 每包覆盖率、Python `coverage --source=app`）；
  `--db` 接入 db-env-health 与 migrations-from-zero。
- `scripts/check/check-db-env-health.py`：schema_migration 计数 + `document>0 && document_version=0` 探针。
- `scripts/ops/verify-migrations-from-zero.sh`：scratch 库从零迁移、checksum、关键表、gateway INSERT 授权。
- 固定等待替换为轮询（§5.2 清单：`waitForTimeout` 10 → 0）。

## 不碰

- 不改任何产品代码。
- 不提高既有覆盖率门槛来"凑绿"。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
bash scripts/check.sh --cov
bash scripts/check.sh --db
python3 scripts/check/check-db-env-health.py --env /etc/ecommerce-rag/dev.env
ECR_SCRATCH_DSN=... ECR_SCRATCH_GATEWAY_DSN=... bash scripts/ops/verify-migrations-from-zero.sh
```

## 行为变更

零（产品代码未改；新增 CI、采集脚本与测试等待方式）。

## 未覆盖路径

- CI workflow 已提交，但 GitHub Actions 是否在远端启用无法从仓库内验证。
- `--cov` 只采集 Go 与 Python；前端 `vitest --coverage` 未接入，避免新增前端覆盖率依赖。
- `migrations-from-zero` 在 `--db` 层需要 `ECR_SCRATCH_DSN`，否则 UNCONFIGURED；scratch 库的
  `vector`/`pg_trgm` 需数据库管理员预建（本次手动用 postgres 容器创建后已删除）。
- 浏览器测试的等待改动未在隔离栈/Keycloak 环境实跑；`--e2e` 仍为本地可选。
- `--db` 仍有既有的 ingest/V2_INGEST_DSN UNCONFIGURED 项，与本包无关。

## 实际交付 vs 计划

- **CI**：新增 `.github/workflows/check.yml`，ubuntu-24.04、`sudo bash scripts/dev/bootstrap-dev.sh`、
  uv/npm 缓存 key 含 `toolchains.lock.json` / `python/uv.lock` / `frontend/package-lock.json` 哈希；
  只跑 fast 层，smoke 仍为本地合并前必跑项。
- **--cov**：新增 `coverage` dev 依赖；Go 总覆盖率 18.4%（逐包表在 `.local/coverage/go-packages.txt`），
  Python `app` 总覆盖率 72%；产物在 `.local/coverage/`；不设门槛。
- **db-env-health**：dev 43/43、test 43/43；负向验证见交付报告。
- **migrations-from-zero**：scratch 库实跑 43 个迁移、checksum 全对、关键表存在、gateway INSERT turn 回滚成功。
- **固定等待**：`waitForTimeout` 10 → 0；import-wizard 用 `expect.poll`，review-suggestion 用 API/DOM 轮询
  与 `networkidle`，keycloak 的轮询循环改用 `pollDelay`；130 秒的 token TTL 等待保留为显式延时（env-gated）。
- 07 保持 `active`；是否转 `done` 等主 agent 复核。
