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
  - `@typescript-eslint/no-unused-vars`（warn，仅 `.ts`；plugin-vue 10 已移除
    script-setup-uses-vars，`.vue` 的模板变量交给 vue/no-unused-vars）
  - `no-undef`（warn，仅 `.vue`；`.ts` 的类型名由 TypeScript 解析，JS 规则只会误报）
  - `@typescript-eslint/no-explicit-any`（warn，用于生成 any 清单）
  - `no-dupe-keys` / `no-unreachable` / `no-debugger`（error；既有代码无违规）
  - vue/no-unused-vars / vue/no-unused-components（warn）
- `npm run lint` 用 `eslint-baseline.mjs` 对比指纹基线：旧 warning 放行，
  新增 warning 组或同组数量增长退出 1；基线清单保存到 `frontend/eslint-baseline.txt`。
- 依赖版本：`eslint@^10.14.0`、`typescript-eslint@^8.72.1`、
  `eslint-plugin-vue@^10.12.0`、`vue-eslint-parser@^10.4.1`、`globals@^17.14.0`。
- 在 `scripts/check.sh` 增加独立 fast 项 `frontend-lint`（fast 42 → 43），同步
  `docs/agent-entry.md`、`docs/environment-facts.md` 和 `check-doc-numbers`。
- 生成并记录 `src` 中 `any` 的出现清单（本包不清零，后续逐步消除）。

## 验收命令

from-zero（含 0052）与三库迁移的权威命令见
[agent-entry §4](../agent-entry.md)；本包只记录已执行的结果，不复制命令。

```bash
# 表存在性
psql "$V2_GATEWAY_DSN" -X -At -c "SELECT to_regclass('public.ingest_stage'), ..."

# 前端
cd frontend && npm run lint && npm test && npm run build

# 门禁
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 验证结果（2026-11-05）

- from-zero：52 个迁移全部 PASS（含 0052），checksum 全对，关键表与 gateway
  INSERT 授权检查通过。
- 三库：`ecr_v2_dev_local`、`ecr_v2_test_m4`、`ecr_v2_accept_20261026` 均为
  `schema_migration=52`；0052 checksum 与文件 sha256
  `b2a9b816814551fce5874ea3f2787ad4a17e8d29847c9ebbff8783571f778c70` 一致。
- 三库断言：五张 A 表 `to_regclass` 均为 NULL；`provider` / `provider_model` 均存在。
- `migration-grants`：PASS，69 张建表记录仍满足授权检查。
- dev 服务已重启（启动时间 2026-11-05 20:23:57/58），`/healthz` 返回 ok。
- frontend：`npm run lint` → 27 warnings / 0 errors / exit 0；
  临时新增一个 `any` 后 → 新 warning 组 / exit 1，基线生效；`npm test` 57 passed；
  `npm run build` 通过。
- `bash scripts/check.sh` → `PASSED 43 / UNCONFIGURED 0 / FAILED 0`。
- `bash scripts/check.sh --smoke` → `PASSED 46 / UNCONFIGURED 0 / FAILED 0`，
  五条 diff 与 `.local/w2/` 基线逐字节一致。
- `doc-numbers`：`migrations=52 fast=43`；文档守卫全过。

## 不做

- 不删 `provider` / `provider_model`；不清 30 处原始 `any`（27 条 lint warning
  加 3 条模板/注释） ；不改其它棘轮/基线。
- 不重采 smoke；不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- 远端 GitHub Actions 未验证；smoke / e2e 仍为本地合并前必跑项。
- ESLint 旧 warning 基线只覆盖 `frontend/src`；`tests/`、`vite.config.ts` 不在本轮范围。
- `any` 只生成清单，不清零：`eslint-any-baseline.txt` 记录 30 处原始出现，其中
  27 处是 `@typescript-eslint/no-explicit-any` warning，3 处是模板内联表达式或注释，
  规则不会访问；后续清理时同步下调基线与清单。
- 五张表已 DROP；`provider` / `provider_model` 保留。若后续 P3 启用，需另行迁移。
- 真实模型、OSS、Keycloak 不在本轮范围。
