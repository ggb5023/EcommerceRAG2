# 90 收尾与人工审查准备

- 状态：见 INDEX.md
- 依据：WP89 后三项只读审查与 Plan 复核；用户 2026-11-07 要求「继续修复残留问题，完成收尾，为人工审查做好准备」
- 前置：WP89 已关（`0be5ea5`）

## 目标

只做收尾：修可在本机验证的 e2e/数据/分页/校验缺陷，补齐 CI 数据库覆盖，让一键演示可走查，
并把 WP80–89 的人工审查走查写入已入库文档。不引入新产品/安全决策，不扩大功能范围。

## 1. 迁移 0060（customer_ticket NULL CHECK）

- 0058 的 lane CHECK 对 `after_sales + NULL` 得 NULL 而通过；新增 0060：
  - 预检 after_sales+NULL、after_sales+非法格式、pre_sales+非 NULL；
  - `ADD CONSTRAINT customer_ticket_lane_order_check`：after_sales 显式 `order_id IS NOT NULL`
    且格式 `^[A-Z0-9][A-Z0-9_-]{3,39}$`；pre_sales 仍要求 NULL；
  - 删除 0058 自动名 `customer_ticket_check3`；写 schema_migration；
- Go 集成测试覆盖三种非法插入与一种合法插入；先 test 后 dev 应用；from-zero 60。

## 2. CI/DB 覆盖

- `check.sh` 增加 `go_db_reason`（test_env + `V2_MIGRATION_DSN` + `V2_GATEWAY_DSN`）；
- 新增 `db-customer`、`db-ingestclient`、`db-admin-answers`、`db-gateway-lifecycle`；
- 仅 `db-import-batch` 依赖 ingest 地址；CI 注释同步。

## 3. e2e

- 修 `import-wizard`/`review-suggestion` 的非法 `expect` 导入（本地 pollCount）；
  review-suggestion 的过期文案改「查看规则建议详情」；
- `validity` 使用 `GATEWAY_URL`，显式 `ECR_REUSE_WORKSPACE=1` 复用栈 Vite；
  `knowledge-preview` 报告目录递归创建；
- `check.sh` 导出 `GATEWAY_URL`/`ECR_REUSE_WORKSPACE`/`KNOWLEDGE_BROWSER_REPORT_DIR`/
  `V2_AUTHORITY_FIXTURE_ENV`；d2 指向隔离栈；6 项受控 spec 缺环境时 UNCONFIGURED；
- d2 对 401/403 返回 UNCONFIGURED（exit 2）。

## 4. 数据集第二轮

- 售后 stock/price 问题使用该订单的 SKU 并在文本中显式出现；
- `orders.updated_at` 不早于发货/签收；
- 重新生成 17 个文件；测试断言 SKU 归属与 `updated_at` 下界。

## 5. 小型正确性

- 管理台回答列表按 `id::text` 排序，与文本游标一致；补分页回归；
- staff 引用有效性同时比较 `document.active_version_id`；
- Python customer facts 只允许四个售后工具；补 pre-sales/compare_products 测试；
- control prompt v2→v3；
- `resolve` 仅 after_sales，错误码 `resolve_requires_after_sales`。

## 6. 一键演示

- `demo-shop.sh` 自动配置 `shop-demo`：`public_slug=shop-demo`、`customer_chat_enabled=true`；
- `commerce-lab.sh` 输出顾客入口 URL；
- 验证 shop 设置、顾客 session 201 与 customer-lanes。

## 7. 文档

- `docs/ui-only-acceptance.md` 补矩阵行与「WP80–89 人工审查走查」；
- 更新 support/install/knowledge/development/agent-entry/constraints/refactor-input/
  architecture/error-taxonomy；迁移 60；
- 新增本工作包、INDEX、CURRENT。

## 验收命令

迁移应用与从零建库按 `docs/environment-facts.md` #3 及 `scripts/ops/v2-migrate.py` 的用法执行
（test-integration 先、dev 后；dev 用 `/etc/ecommerce-rag/migration.env`）。其余：

```bash
bash scripts/check.sh
bash scripts/check.sh --db
bash scripts/check.sh --smoke
```

e2e 按 `docs/development.md` 的隔离栈与 seed 步骤启动，再跑 `bash scripts/check.sh --e2e`。
一键演示与顾客 browser 用例按 `docs/development.md` 的 commerce-lab 步骤，在独立端口启动后用
`customer-lanes.browser.mjs` 指向该 Vite。

## 不要做

- 不改 0058 或任何已执行迁移；不新增 SECURITY DEFINER；
- 不修 WP80 竞态、staff behavior 持久化、fixture facts、selfhost facts、真实模型评测；
- 不改 contracts 棘轮、不重采 smoke 基线、不改断言迁就代码；
- 不修改/提交 `scripts/review/`；不用 `git add .`。
