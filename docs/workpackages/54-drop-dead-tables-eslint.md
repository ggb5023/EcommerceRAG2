# 54 P2：DROP 死表 + ESLint 接入

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[WP53 盘点](53-dead-code.md)、[全局审查 P2](../audits/2026-11-04-global-audit.md)、
> [environment-facts #3/#4](../environment-facts.md)

## 目标

1. 按 WP53 的 A 类清单新增迁移 `0052_drop_dead_tables.sql`，DROP 五张 0 行 0 引用的
   死表；保留 `provider` / `provider_model`。
2. 给 frontend 接入 ESLint（TypeScript + Vue），初始规则从宽；旧代码违规以
   warning + 基线计数保留，新代码新增 warning 让 lint 失败；不清零既有 `any`。

## DROP 迁移

- 新迁移 `0052_drop_dead_tables.sql`，只追加，不改已执行迁移。
- 顺序：`ingest_package_file` → `ingest_package_upload` → `eval_run` →
  `eval_case` → `ingest_stage`；每条 `DROP TABLE IF EXISTS`。
- 不删 `provider` / `provider_model`；不新增对已执行迁移的修改。
- 先跑 from-zero（含 0052），再应用到 `ecr_v2_dev_local`、`ecr_v2_test_m4`、
  `ecr_v2_accept_20261026`，逐库核对 `schema_migration` 与 0052 checksum。
- 迁移只删除未被 runtime 引用的表；应用后重启 dev 服务并核对启动时间，再跑 smoke。

## ESLint

- `frontend/package.json` 增加 ESLint、`typescript-eslint`、`eslint-plugin-vue`、
  `globals` 依赖并更新 lockfile；脚本 `lint`。
- 使用 flat config，只扫描 `frontend/src`；初始只开明显问题类别：
  - `@typescript-eslint/no-unused-vars`（warn）
  - `no-undef`（warn，浏览器 globals）
  - `@typescript-eslint/no-explicit-any`（warn，用于生成 any 清单）
  - `no-dupe-keys` / `no-unreachable` / `no-debugger`（error；既有代码应无违规）
  - vue/no-unused-vars / vue/no-unused-components（warn）
- `npm run lint` 使用 warning 基线（`--max-warnings <基线数>`）：旧 warning 放行，
  新增 warning 退出 1；基线清单保存到 `frontend/eslint-baseline.txt`。
- 在 `scripts/check.sh` 增加独立 fast 项 `frontend-lint`（fast 42 → 43），同步
  `docs/agent-entry.md`、`docs/environment-facts.md` 和 `check-doc-numbers`。
- 生成并记录 `src` 中 `any` 的出现清单（本包不清零，后续逐步消除）。

## 验收命令

```bash
# from-zero（含 0052）
ECR_SCRATCH_DSN=... ECR_SCRATCH_GATEWAY_DSN=... \
  bash scripts/ops/verify-migrations-from-zero.sh

# 三库：dev / test-integration / eval-migration
python/.venv/bin/python scripts/ops/v2-migrate.py --env <各自 migration env>

# 表存在性
psql "$V2_GATEWAY_DSN" -X -At -c "SELECT to_regclass('public.ingest_stage'), ..."

# 前端
cd frontend && npm run lint && npm test && npm run build

# 门禁
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不删 `provider` / `provider_model`；不清 30 处 `any`；不改其它棘轮/基线。
- 不重采 smoke；不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- 远端 GitHub Actions 未验证；smoke / e2e 仍为本地合并前必跑项。
- ESLint 旧 warning 基线只覆盖 `frontend/src`；`tests/`、`vite.config.ts` 不在本轮范围。
- `any` 只生成清单，不清零；基线数在交付时记录。
- 真实模型、OSS、Keycloak 不在本轮范围。
