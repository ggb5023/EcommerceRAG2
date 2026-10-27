# 0020 仅 WebUI 验收标准

- 状态：已采纳
- 日期：2026-10-27
- 目标：商家部署完成后，A 级日常功能必须能只通过 WebUI 完成；B 级部署运维允许 CLI，但须一条命令；C 级开发/评测不属商家范围。

## 分级

| 级别 | 范围 | 要求 |
| --- | --- | --- |
| A 商家日常 | 店铺、资料导入/预检/审核/发布/撤回、问答工作台、客户入口、权限与审计、模型辅助开关 | 100% UI 可完成 |
| B 部署运维 | 安装、迁移、启动、备份/恢复、密钥与 Provider 配置 | 允许 CLI，但须一条命令或明确脚本 |
| C 开发评测 | 评测、迁移开发、集成测试、预算 extend | 不要求 UI |

## A 级路由矩阵

以下逐条列出 `go/internal/admin` 的 31 条路由；状态按当前 WebUI 实际承载判定，不把“有 API”当作“有 UI”。

| # | 功能 | 后端路由 | UI 入口 | 状态 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 1 | 会话/权限摘要 | `GET /admin/v1/session` | AdminApp 全局 | ✅ | 页面加载即取 |
| 2 | 运行概览 | `GET /admin/v1/overview` | 运行状态页 | ✅ |  |
| 3 | 账号列表 | `GET /admin/v1/access/accounts` | 账号与权限页 | ✅ |  |
| 4 | 权限申请列表 | `GET /admin/v1/access/requests` | 权限申请页 | ✅ |  |
| 5 | 创建权限申请 | `POST /admin/v1/access/requests` | 权限申请页表单 | ✅ |  |
| 6 | 审批权限申请 | `POST /admin/v1/access/requests/{id}/decision` | 权限申请弹窗 | ✅ |  |
| 7 | 创建员工 | `POST /admin/v1/staff` | 账号与权限页 | ✅ | 一次性密码展示 |
| 8 | 授权审计 | `GET /admin/v1/access/audit` | 授权审计页 | ✅ |  |
| 9 | 撤销授权 | `POST /admin/v1/access/grants/{id}/revoke` | 账号与权限弹窗 | ✅ |  |
| 10 | 店铺列表 | `GET /admin/v1/merchant/shops` | 店铺范围页 | ✅ |  |
| 11 | 店铺设置 | `POST /admin/v1/merchant/shops/{id}/settings` | 店铺范围页表单 | ✅ |  |
| 12 | 资料来源列表 | `GET /admin/v1/merchant/sources` | 资料来源页 | ✅ |  |
| 13 | YAML 包导入 | `POST /admin/v1/merchant/sources/import` | 资料来源页 + KnowledgeImport | ✅ | 旧路径保留 |
| 14 | 创建导入批次 | `POST /admin/v1/merchant/imports` | 导入预检向导第 1 步 | ✅ | multipart + business_date |
| 15 | 读取模型辅助开关 | `GET /admin/v1/merchant/agent-review` | 店铺范围页开关卡片 | ✅ | 默认 false |
| 16 | 修改模型辅助开关 | `PUT /admin/v1/merchant/agent-review` | 店铺范围页开关卡片 | ✅ | append-only 写一行 |
| 17 | 导入批次预览 | `GET /admin/v1/merchant/imports/{id}` | 导入预检向导第 2 步 | ✅ | 显示片段/问题/建议 |
| 18 | 确认导入 | `POST /admin/v1/merchant/imports/{id}/confirm` | 导入预检向导第 3 步 | ✅ | 商家显式点击 |
| 19 | 撤回导入批次 | `POST /admin/v1/merchant/imports/{id}/revoke` | 无 | ❌ | 向导未提供批次撤回入口 |
| 20 | 摄取任务列表 | `GET /admin/v1/merchant/ingestion` | 摄取任务页 | ✅ |  |
| 21 | 摄取任务详情 | `GET /admin/v1/merchant/ingestion/{id}` | 摄取任务弹窗 | ✅ |  |
| 22 | 取消摄取 | `POST /admin/v1/merchant/ingestion/{id}/cancel` | 摄取任务弹窗 | ✅ |  |
| 23 | 重试摄取 | `POST /admin/v1/merchant/ingestion/{id}/retry` | 摄取任务弹窗 | ✅ |  |
| 24 | 待审列表 | `GET /admin/v1/merchant/reviews` | 版本审核页 | ✅ |  |
| 25 | 版本详情 | `GET /admin/v1/merchant/reviews/{id}` | 版本审核详情 | ✅ |  |
| 26 | 审核版本 | `POST /admin/v1/merchant/versions/{id}/review` | 版本审核弹窗 | ✅ | 商家账号 |
| 27 | 发布版本 | `POST /admin/v1/merchant/versions/{id}/publish` | 版本操作弹窗 | ✅ | 商家账号 |
| 28 | 回滚版本 | `POST /admin/v1/merchant/versions/{id}/rollback` | 版本操作弹窗 | ✅ |  |
| 29 | 撤回版本 | `POST /admin/v1/merchant/versions/{id}/revoke` | 版本操作弹窗 | ✅ |  |
| 30 | 版本历史 | `GET /admin/v1/merchant/versions` | 无 | ❌ | 审核页只列待审，不展示历史版本 |
| 31 | 命令结果 | `GET /admin/v1/merchant/commands/{id}` | 无 | ❌ | 操作响应直接回显，未提供命令查询页 |
| — | 顾客入口 | `/v1/customer/*` | `/shop/:slug` | ✅ | 顾客问答、引用与转人工入口 |

## 缺口清单（按优先级）

1. **导入批次撤回 UI（❌ #19）**：确认后未发布批次的整批撤回只能调 API。优先级高，属于 L1 收尾。
2. **版本历史 UI（❌ #30）**：已发布/已撤回版本历史没有独立页面；审核页只显示待审队列。优先级高。
3. **命令结果 UI（❌ #31）**：异步命令只有即时响应，无法在 UI 按 command id 查询最终结果。优先级中。
4. **模型供应商选择（未在 31 条内，P3/0019）**：provider/provider_model、测试连接、四槽位绑定均无 UI 与后端实现；已知属 P3。

## B 级（允许 CLI，一条命令）

- 安装/迁移/启动：`docker compose` 或 `bash scripts/dev/v2-local-services.sh --env ...`；
- 备份/恢复：`scripts/ops/oss-backup.sh` / `oss-restore.sh`；
- Provider 与预算配置：`/etc/ecommerce-rag/providers.env` 与 `provider-budget.py`。
  这些不要求 WebUI；若在 WebUI 暴露，必须走 0019 的密钥与审计规则。

## C 级（不属商家）

评测、集成测试、迁移开发、预算 extend、诊断脚本。

## 结论

当前 31 条 admin 路由中 **28 条有 UI、3 条无 UI**（#19、#30、#31）；另有未在 31 条内的 P3 供应商选择缺口。
A 级尚未 100% 达标；缺口按上表顺序补齐后再验收“仅 WebUI 可完成”。
