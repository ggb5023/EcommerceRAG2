# 仅 WebUI 验收标准

阶段 2 验收完成：单元/集成/浏览器三层证据齐备，**A 级 33/34**。
`GET /admin/v1/merchant/usage` 尚无 UI，随 0021 的 token 用量界面一并实现。矩阵登记 31 条，另有 `batch-approve`、`suggestion` 两条前端已在用、本次补登记。

- 日期：2026-10-27
- 目标：商家部署完成后，A 级日常功能必须能只通过 WebUI 完成；B 级部署运维允许 CLI，但须一条命令；C 级开发/评测不属商家范围。

## 分级

| 级别 | 范围 | 要求 |
| --- | --- | --- |
| A 商家日常 | 店铺、资料导入/预检/审核/发布/撤回、问答工作台、客户入口、权限与审计、模型辅助开关 | 100% UI 可完成 |
| B 部署运维 | 安装、迁移、启动、备份/恢复、密钥与 Provider 配置 | 允许 CLI，但须一条命令或明确脚本 |
| C 开发评测 | 评测、迁移开发、集成测试、真实模型调用授权 | 不要求 UI |

## A 级路由矩阵

以下逐条列出 `go/internal/admin` 的 31 条路由；状态按当前 WebUI 实际承载判定，不把“有 API”当作“有 UI”。

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
| 34 | 用量查看 | `GET /admin/v1/merchant/usage` | 无 | ❌ | — | 待实现；随 0021 的 token 用量界面一并落地 |
| — | 顾客入口 | `/v1/customer/*` | `/shop/:slug` | ✅ | 单条 | 顾客问答、引用与转人工入口 |

## 缺口清单（按优先级）

1. **用量查看（`GET /admin/v1/merchant/usage`）**：无 UI，随 0021 的 token 用量界面实现；这是当前唯一 A 级未实现项。
2. **模型供应商选择（P3/0019，提议）**：`provider` / `provider_model`、测试连接、四槽位绑定均无 UI 与后端实现；未在 34 条 admin 路由内。

除上述两项外，**34 条 admin 路由中 33 条已有 UI 承载**（`batch-approve` 与 `suggestion` 属矩阵补登记，前端早已在用）；
批次撤回、版本历史、命令结果已在 L1/L2 收尾中关闭。

## B 级（允许 CLI，一条命令）

- 安装/迁移/启动：`docker compose` 或 `bash scripts/dev/v2-local-services.sh --env ...`；
- 备份/恢复：`scripts/ops/oss-backup.sh` / `oss-restore.sh`；
- Provider 与用量配置：`/etc/ecommerce-rag/providers.env` 与 `scripts/dev/provider-usage.py`（0021 之后不再有货币账本）。
  这些不要求 WebUI；若在 WebUI 暴露，必须走 0019 的密钥与审计规则。

## C 级（不属商家）

评测、集成测试、迁移开发、真实模型调用授权、诊断脚本。

## 结论

当前 31 条 admin 路由 **全部有 UI**；A 级剩余缺口只有未在 31 条内的 P3 供应商选择。
A 级除 P3 供应商选择外已 100% 可只通过 WebUI 完成；浏览器用例 `frontend/tests/import-wizard.browser.mjs` 覆盖导入向导全流程（上传、四项预检、确认、20 条待审、整包撤回）；`frontend/tests/review-suggestion.browser.mjs` 覆盖 L2 建议列、筛选、批量确认、stale 重生成与发布仍需单独点击。供应商选择按 0019 实现后再验收。
