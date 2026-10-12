# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`。最新已提交基线为 `8aa781f`；本窗口新增 Gateway 重启恢复和浏览器验收改动尚未提交。未提交改动均属于本任务：Go Gateway、结果状态、worker import 排序、客服浏览器报告，以及实际权限浏览器脚本。
- 真实百炼 V2 资料生命周期已通过：Markdown/CSV/DOCX 实际摄取、真实 document/query Embedding、Rerank、generation、审核/发布、查询引用；混合结果保持 `real_service_acceptance=false`。既有共享账本和请求指纹保留，不因预算授权变化而清零。
- 客服工作台实际浏览器 12 项 PASS；实际数据库撤权和权限版本变化浏览器 3 项 PASS。报告在 `.local/v2-workspace/browser/`、`.local/v2-authority/browser/` 唯一目录下，均为脱敏元数据报告。
- 本窗口实现 Gateway 单实例数据库所有权：独立连接持有 advisory lock `(742019,1)`，第二实例拒绝；ready 前恢复 PENDING/EXECUTING/CANCEL_REQUESTED，分别持久化为 FAILED/`gateway_restarted` 或 CANCELLED/`cancelled`，追加连续 SSE error/completed；DONE、ASKING 和 checkpoint 保留，不自动重发模型请求。SSE 普通事件改由数据库事务分配序号，避免重启/并发冲突。
- 实际隔离重启证据：暂停 AI，创建 EXECUTING 请求，冻结并杀掉当前 Gateway，重启四进程；启动日志 `Gateway recovered 1 interrupted executions`，GET 为 FAILED/`gateway_restarted`，SSE 回放 seq 1–3，随后新请求实际 DONE 且含 8 条证据；第二 Gateway 被拒绝。报告：`.local/v2-recovery/browser/recovery-run/recovery-report.json`。
- `bash scripts/check.sh` 已通过：Go 全包、Python `299 passed/25 skipped`、Proto、双前端测试与 build、facts fake 故障矩阵；`web/tests/workspace.browser.mjs` 12 项和 `web/tests/v2-authority.browser.mjs` 3 项再次通过。

## 下一步任务

1. 审阅并提交当前 Gateway 恢复、结果标记、浏览器脚本和文档变更；只按具体文件暂存，运行 hooks/privacy guard。
2. 按迁移门槛继续审计旧 Python `app.server`、`app.v2_grpc_server`、旧 `rag_service` 及 synthetic 工具；活动 V2 已固定使用 `app.rpc.server`、`app.rpc.ingest_server`、`app.ingest.worker_main`。保留真正需要的历史契约/测试，退出活动兼容入口前先完成新入口回归。
3. 收口能力迁移清单、验证与验收和专题文档状态；不改写历史 snapshots/archive。完成前继续保持整体目标未完成。

## 遗留问题

- 真实 OIDC、真实商家 API、正式 HITL、生产规模质量、灾备/容量和 M2/M3 仍是后续外部验收；当前开发身份、facts fake、受控 fixture 与真实模型组合不能宣称生产就绪。
- 旧兼容入口和历史 synthetic 工具尚未全部退出；不得用 `scripts/check.sh` 的 fixture 通过替代退出审计。
- live 数据库已有发布版本和 chunk，不能直接重跑要求空索引的 live 生命周期脚本；使用已有只读证据或新独立库复验。
