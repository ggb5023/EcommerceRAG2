# 仅 WebUI 验收标准

阶段 2 的 UI 覆盖以本矩阵为入口；后端总路由数不在此硬编码，以
`go/internal/admin/handler.go` 和 `admin-boundaries` 检查输出为准。`usage`、
`handoff`、`validity` 的 UI 已由 WP42 实现；不在本文重复路由计数。

- 日期：2026-10-27
- 目标：商家部署完成后，A 级日常功能必须能只通过 WebUI 完成；B 级部署运维允许 CLI，但须一条命令；C 级开发/评测不属商家范围。

## 分级

| 级别 | 范围 | 要求 |
| --- | --- | --- |
| A 商家日常 | 店铺、资料导入/预检/审核/发布/撤回、问答工作台、客户入口、权限与审计、模型辅助开关 | 100% UI 可完成 |
| B 部署运维 | 安装、迁移、启动、备份/恢复、密钥与 Provider 配置 | 允许 CLI，但须一条命令或明确脚本 |
| C 开发评测 | 评测、迁移开发、集成测试、真实模型调用授权 | 不要求 UI |

## A 级路由矩阵

以下列出已核对的管理入口；状态按当前 WebUI 实际承载判定，不把“有 API”当作“有 UI”。后端新增路由应同步登记，路由总数以 `handler.go` 为准。

| # | 功能 | 后端路由 | UI 入口 | 状态 | 操作成本 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 会话/权限摘要 | `GET /admin/v1/session` | AdminApp 全局 | ✅ | 一键 | 页面加载即取 |
| 2 | 运行概览 | `GET /admin/v1/overview` | 运行状态页 | ✅ | 一键 |  |
| 3 | 账号列表 | `GET /admin/v1/access/accounts` | 账号与权限页 | ✅ | 一键 |  |
| 4 | 权限申请列表 | `GET /admin/v1/access/requests` | 权限申请页 | ✅ | 一键 |  |
| 5 | 创建权限申请 | `POST /admin/v1/access/requests` | 权限申请页表单 | ✅ | 单条 |  |
| 6 | 审批权限申请 | `POST /admin/v1/access/requests/{id}/decision` | 权限申请弹窗 | ✅ | 单条 |  |
| 7 | 创建员工 | `POST /admin/v1/staff` | 账号与权限页 | ✅ | 单条 | 一次性密码展示 |
| 8 | 授权审计 | `GET /admin/v1/access/audit` | 授权审计页 | ✅ | 一键 |  |
| 9 | 撤销授权 | `POST /admin/v1/access/grants/{id}/revoke` | 账号与权限弹窗 | ✅ | 单条 |  |
| 10 | 店铺列表 | `GET /admin/v1/merchant/shops` | 店铺范围页 | ✅ | 一键 |  |
| 11 | 店铺设置 | `POST /admin/v1/merchant/shops/{id}/settings` | 店铺范围页表单 | ✅ | 单条 |  |
| 12 | 资料来源列表 | `GET /admin/v1/merchant/sources` | 资料来源页 | ✅ | 一键 |  |
| 13 | YAML 包导入 | `POST /admin/v1/merchant/sources/import` | 资料来源页 + KnowledgeImport | ✅ | 一键 | 旧路径保留 |
| 14 | 创建导入批次 | `POST /admin/v1/merchant/imports` | 导入预检向导第 1 步 | ✅ | 一键 | multipart + business_date |
| 15 | 读取模型辅助开关 | `GET /admin/v1/merchant/agent-review` | 店铺范围页开关卡片 | ✅ | 一键 | 默认 false |
| 16 | 修改模型辅助开关 | `PUT /admin/v1/merchant/agent-review` | 店铺范围页开关卡片 | ✅ | 单条 | append-only 写一行 |
| 17 | 导入批次预览 | `GET /admin/v1/merchant/imports/{id}` | 导入预检向导第 2 步 | ✅ | 一键 | 显示片段/问题/建议 |
| 18 | 确认导入 | `POST /admin/v1/merchant/imports/{id}/confirm` | 导入预检向导第 3 步 | ✅ | 单条 | 商家显式点击 |
| 19 | 撤回导入批次 | `POST /admin/v1/merchant/imports/{id}/revoke` | 导入预检向导“撤回整包” | ✅ | 单条 | 二次确认，撤回后置灰 |
| 20 | 摄取任务列表 | `GET /admin/v1/merchant/ingestion` | 摄取任务页 | ✅ | 一键 |  |
| 21 | 摄取任务详情 | `GET /admin/v1/merchant/ingestion/{id}` | 摄取任务弹窗 | ✅ | 一键 |  |
| 22 | 取消摄取 | `POST /admin/v1/merchant/ingestion/{id}/cancel` | 摄取任务弹窗 | ✅ | 单条 |  |
| 23 | 重试摄取 | `POST /admin/v1/merchant/ingestion/{id}/retry` | 摄取任务弹窗 | ✅ | 单条 |  |
| 24 | 待审列表 | `GET /admin/v1/merchant/reviews` | 版本审核页 | ✅ | 一键 |  |
| 25 | 版本详情 | `GET /admin/v1/merchant/reviews/{id}` | 版本审核详情 | ✅ | 一键 |  |
| 26 | 审核版本 | `POST /admin/v1/merchant/versions/{id}/review` | 版本审核弹窗 + 批量确认 | ✅ | 批量 | 批量确认需商家填理由，agent 不代写 reviewed_* |
| 27 | 发布版本 | `POST /admin/v1/merchant/versions/{id}/publish` | 版本操作弹窗 | ✅ | 单条 | 发布不批量：一次一个版本，需商家在版本详情页单独点击 |
| 28 | 回滚版本 | `POST /admin/v1/merchant/versions/{id}/rollback` | 版本操作弹窗 | ✅ | 单条 |  |
| 29 | 撤回版本 | `POST /admin/v1/merchant/versions/{id}/revoke` | 版本操作弹窗 | ✅ | 单条 |  |
| 30 | 版本历史 | `GET /admin/v1/merchant/versions` | 版本审核页“历史版本” | ✅ | 一键 | 展开查看状态与发布时间 |
| 31 | 命令结果 | `GET /admin/v1/merchant/commands/{id}` | 导入向导任务状态轮询 | ✅ | 一键 | 显示处理中/完成/失败原因 |
| 32 | 批量确认 | `POST /admin/v1/merchant/versions/batch-approve` | 版本审核页「批量确认」 | ✅ | 批量 | 前端已在用（`AdminApp.vue:207`），此前漏登记 |
| 33 | 生成审核建议 | `POST /admin/v1/merchant/versions/{id}/suggestion` | 版本审核页「生成建议」 | ✅ | 单条 | 前端已在用（`AdminApp.vue:178/189`），此前漏登记 |
| 35 | customer 转人工 | `GET /admin/v1/merchant/report/handoff` | 报告页 | ✅ | 一键 | 分子/分母/rate；null 显示无数据 |
| 36 | 版本有效期 | `POST /admin/v1/merchant/versions/{id}/validity` | 版本审核详情 | ✅ | 单条 | 需理由与 fencing epoch |
| 34 | 用量查看 | `GET /admin/v1/merchant/usage` | 报告页 | ✅ | 一键 | by_slot/by_model token 与调用量 |
| — | 顾客入口 | `/v1/customer/*` | `/shop/:slug` | ✅ | 单条 | 先选售前/售后；售后必须填订单号；显示工单状态；顾客引用抽屉保留 |
| — | 回答与引用审查 | `GET /admin/v1/merchant/answers`、`GET /admin/v1/merchant/answers/{kind}/{id}` | `/admin/merchant/answers` | ✅ | 单条 | 员工轮次与顾客 AI 消息；列表截断，详情含引用有效性/事实快照/mock；打开详情写审计 |
| — | 顾客服务轨道 | `POST /v1/customer/conversations` | `/shop/:slug` | ✅ | 单条 | 先选售前/售后；售后必须填订单号并绑定会话 |
| — | 售后工单生命周期 | `POST /v1/support/conversations/{id}/{action}` | `/staff` | ✅ | 单条 | OPEN→ASSIGNED→RESOLVED/CLOSED；售前转人工不建工单 |
| — | 售后只读事实 | `GET /v1/facts/{tool}`（facts provider） | 顾客聊天 / 管理台事实快照 | ✅ dev-only | 单条 | 开发与隔离栈；selfhost 首版 fail-closed |
| — | 回答-only 前台 | `GET /v1/turns/{id}`、`GET /v1/support/...` | `/assistant`、`/staff` | ✅ | 单条 | 员工/商家只显示回答；引用与事实在管理台审查 |
| — | 数据集与模拟器 | `GET /dev/v1/*`、`GET /v1/facts/*` | `scripts/dev` | B/C | — | `commerce-lab-v1` 与 `commerce-sim`；仅 loopback |

## 缺口清单（按优先级）

1. **用量查看、handoff、有效期**：WP42 已在报告页与版本详情实现。
2. **模型供应商选择（P3/0019，提议）**：`provider` / `provider_model`、测试连接、四槽位绑定均无 UI 与后端实现；未在当前矩阵内。

矩阵中的 `batch-approve` 与 `suggestion` 属补登记，前端早已在用；批次撤回、版本历史、命令结果已在 L1/L2 收尾中关闭。

## B 级（允许 CLI，一条命令）

- 安装/迁移/启动：`docker compose` 或 本机启动脚本见 `docs/development.md`；
- 备份/恢复：`scripts/ops/oss-backup.sh` / `oss-restore.sh`；
- Provider 与用量配置：`/etc/ecommerce-rag/providers.env` 与 `scripts/dev/provider-usage.py`（0021 之后不再有货币账本）。
  这些不要求 WebUI；若在 WebUI 暴露，必须走 0019 的密钥与审计规则。

## C 级（不属商家）

评测、集成测试、迁移开发、真实模型调用授权、诊断脚本。

## 结论

当前矩阵内已登记 A 级入口均有 UI；P3 供应商选择仍未实现。
已登记 A 级功能可只通过 WebUI 完成；浏览器用例 `frontend/tests/import-wizard.browser.mjs` 覆盖导入向导全流程（上传、四项预检、确认、20 条待审、整包撤回）；`frontend/tests/review-suggestion.browser.mjs` 覆盖 L2 建议列、筛选、批量确认、stale 重生成与发布仍需单独点击。供应商选择按 0019 实现后再验收。

## WP80–89 人工审查走查（2026-11-07）

> 审查目标：`main` 上的 WP80–89。权威行为顺序见 `docs/agent-entry.md` §8；售后轨道与工单以
> [0041](decisions/0041-customer-service-lanes-and-tickets.md) 为准，跨进程字段以
> [contracts](contracts.md) 为准。本文只给走查入口，不重复工作包里的长命令。

### 启动与身份

- 按 [development](development.md) 的「模拟电商后端」一节的 commerce-lab 命令启动隔离栈，
  打开 `--with-vite` 与 `--commerce-sim`；预期 Vite 在 `base+3`、facts/dev REST 在 `base+4`。
- commerce-lab 会自动把 `shop-demo` 的 `public_slug` 设为 `shop-demo` 并启用客户咨询；
  未启用时先在 `/admin/merchant/shops` 手动保存。
- 隔离栈使用显式 development 身份（`dev-user`，无密码）；dev 栈当前是 `oidc_browser`，
  匿名管理请求会 401，不能作为走查入口。
- 顾客入口：`/shop/shop-demo`。售后测试订单可用 `ORD-2609-0005`（数据集内的发货订单）。

### 顾客售后链路（WP83/85）

1. 在 `/shop/shop-demo` 先选「售后咨询」，填写订单号；售前不填订单号。
2. 询问订单状态或物流；离线 mock 档下顾客 AI 回答会按设计转人工（mock 被拒绝），
   这是预期行为，不是故障。
3. 转人工后查看工单状态与只读提示；顾客引用抽屉只在有真实引用时出现。

### 员工工单链路（WP83/87）

1. 登录员工端 `/staff`（旧 `/support` 跳转），队列应显示服务轨道、订单号与工单状态。
2. 领取会话后工单为 ASSIGNED；点击「解决并关闭工单」，工单变 RESOLVED、会话变 CLOSED。
3. 售前会话转人工时队列不显示工单；对售前执行 resolve 返回
   `resolve_requires_after_sales`（409），会话状态不变。

### 管理台回答与引用（WP86/87）

1. 先在 `/assistant` 提一个员工问题，生成一条可审查回答。
2. 打开 `/admin/merchant/answers`，切换来源（员工轮次/顾客消息）与结果来源（真实/Mock）；
   列表截断与复合游标不应丢行。
3. 打开详情：引用有效性、事实快照的 `observed_at`、`is_mock`、mock 标记与审计写入。
   `/assistant` 与 `/staff` 只显示回答，不显示证据面板；顾客聊天保留引用抽屉。
4. 管理台价格事实展示 `amount_cents` 与 `currency` 属于商品/订单数据（0034），不是 0021
   禁止的 usage/预算货币统计。

### 数据集与事实契约

- `commerce-lab-v1` 的 17 个文件、哈希与行数由数据集测试锁定；退货 SKU、退货/工单/物流
  时间与 `updated_at` 已加断言。核验入口见 [development](development.md) 的模拟器一节。
- facts 契约的版本/租户/店铺/资源绑定必须 fail-closed；开发/隔离栈可用
  `/dev/v1/orders`、`/dev/v1/tickets` 与 `/v1/facts/{tool}` 对照。
- commerce-sim 的事实始终 `is_mock=true`，只用于开发；不能证明真实商家数据。

### 已知限制（审查时逐条接受/延后）

| 限制 | 影响 | 出处 |
| --- | --- | --- |
| selfhost facts 禁用 | selfhost 售后事实按不可用转人工 | 0041、`go/internal/gateway/wire.go` |
| 真实模型仅事实型 12 例 | 6 ANSWER / 2 CLARIFY / 4 转人工；gate 9/12，after-015 少工具、after-104/105 写请求 reason 不符；知识/引用与 repeat 未跑 | WP92 |
| 受控 e2e 仍有环境限制 | 5 spec + restore 在纯受控运行中通过；与隔离栈同时跑时 Docker 网络变更会造成 Chromium 瞬态失败；login 与 v2-restart 仍 UNCONFIGURED | WP91 |
| WP80 adoption 竞态 | 撤销与 adoption 提交之间无行锁；网关授权与审核兜底 | WP80/88 |
| 不同订单仅显式订单号线索 | 裸标识/未知写法仍依赖控制模型上报 | WP93 |
| suggestion 审查 kind | 建议轮次仍不在管理台回答审查列表 | WP93 |
| 数据集范围 | 单租户/单店铺、固定日期；tickets.csv 是模拟器数据，不是 customer_ticket | WP82/89 |
| 订单号即凭证 | 售前/售后都无购买人验证；知道订单号即可只读查询 | 0041 §2.4 |

### 审查记录

记录 HEAD、实际运行的命令与 PASS/FAIL/UNCONFIGURED 计数、点击过的路由与截图、
对以上限制的接受/延后结论；不要把 `browser.env`、DSN、setup token 或 provider 密钥
写入记录或截图。
