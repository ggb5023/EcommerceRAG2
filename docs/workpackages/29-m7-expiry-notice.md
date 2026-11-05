# 29 M7 阶段 2（三）：过期与即将过期提示

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0033](../decisions/0033-document-validity.md)、
> [data-ownership](../data-ownership.md)、[contracts](../contracts.md)

## 目标

在管理端只读版本列表与版本详情上显示 `effective_from` / `effective_to` 和时效状态，
使用与 AI scope 相同的业务日期来源；不改检索、不自动下架、不新增写路径。

## 口径

- 状态三态：`active` / `expiring_in_30` / `expired`；阈值 30 天，由
  `validityNoticeDays` 单点定义。
- 业务日期：优先 `SYNTHETIC_BUSINESS_DATE`（合法日期），否则
  `shop.timezone + now`；与签发 scope 共用 `auth.ShopBusinessDate`。
- `effective_to` 是失效日（不含当天）：`effective_to <= business_date` 即
  `expired`；`effective_to = business_date + 30 天` 仍是 `expiring_in_30`，
  `+31 天` 为 `active`；`NULL` 为 `active`。
- API 只读返回：`effective_from`、`effective_to`（`YYYY-MM-DD` 或空串）、
  `validity_status`、`days_until_expiry`（`NULL` 为 0）。
- 列表/详情共用同一领域函数，SQL 只在 `store_*.go`；前端只渲染，不重算日期。
- 只提示，不自动下架；0016 检查页若存在则复用同一状态口径，不新增写操作。

## 改动范围

- Go：`validityStatus` 领域函数与边界测试；`auth.ShopBusinessDate` 导出；
  `ReviewQueue` / `Versions` 增加有效期列；admin 列表与详情装配状态字段。
- 前端：`version-history.ts` 增加标签/色板/日期范围；`ReviewsPage.vue` 在历史版本、
  版本行和详情显示日期与徽标（过期红、即将过期橙、正常不显示徽标）；
  新增 `validity-dates` / `validity-badge` 测试选择器。
- 文档：本工作包、INDEX 与 CURRENT。

## 验收命令

```bash
cd go && go test ./...
cd frontend && npm test && npm run build
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 二进制重建 + `strings` 验证新符号 + 重启；接口返回真实
`expiring_in_30` / `expired`；`.local/redesign/` 两张截图（即将过期、已过期）；
确认五条 smoke 基线逐字节一致且未重采。

## 不做

- 不改检索 SQL；不自动下架；不加 cron；不新增迁移。
- 不改现有 API 字段语义、权限、幂等或终态判定。
- 不调棘轮/预算/基线；不重采 smoke；不引依赖；不改 本机未跟踪目录。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- Go 领域函数：`validityStatus` 按业务日期计算三态与剩余天数；`go test ./...`
  全绿，`TestValidityStatusBoundaries` 覆盖 `NULL`、失效当天、过去、第 30 天、
  第 31 天（`expired` 0 天、`expired` -1 天、`expiring_in_30` 30 天、`active` 31 天）。
- `auth.ShopBusinessDate`：读取 `shop.timezone` 后走既有
  `businessDateForLocation`，保留 `SYNTHETIC_BUSINESS_DATE` 优先级；
  admin 列表与详情逐店缓存业务日期。
- admin API：`GET /admin/v1/merchant/reviews`、`GET /admin/v1/merchant/reviews/{id}`、
  `GET /admin/v1/merchant/versions` 返回有效期与状态；详情复用 `ReviewDetail`
  日期，不新增端点。dev 环境 `SYNTHETIC_BUSINESS_DATE=2026-09-29` 下，
  版本 38 设 `effective_to=2026-10-20` 返回 `expiring_in_30` / 21 天，
  设 `effective_to=2026-09-29` 返回 `expired` / 0 天。
- 前端：历史版本、版本列表行、审核详情显示有效期与状态徽标；正常状态不显示
  徽标；无货币金额。`npm test` 50 passed，`npm run build` 成功。
- 截图：`.local/redesign/validity-expiring.png`（橙色 `30 天内失效`）与
  `.local/redesign/validity-expired.png`（红色 `已过期`），均 1440×900。
- 门禁：`check.sh` **PASSED 40 / UNCONFIGURED 0 / FAILED 0**；
  `--smoke` **PASSED 45 / UNCONFIGURED 0 / FAILED 0**，五条 `diff` 无输出。
- Go 二进制重建后 `strings .local/v2-gateway | grep -c 'expiring_in_'` 为 **1**；
  最终重启后的进程启动时间见交付汇报。

## 未覆盖路径

- 只读展示，不验证跨日自动刷新；页面重新加载后状态才更新。
- 未在 active / superseded / revoked 全状态上逐一验证；只验证一个 ready 版本。
- 0016 只有设计草案、没有已实现页面，因此没有可复用的检查页；未自动下架。
- 未做浏览器自动化回归；截图来自本地 dev 管理端。
- 未自动下架；未来日期不阻断审核或发布。
