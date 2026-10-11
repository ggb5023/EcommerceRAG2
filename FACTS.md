# 长期事实

## 架构结论

- 系统采用 Go 控制面和基于 LangChain/LangGraph 的 Python AI 面。
- Go 是对外业务入口，负责身份、租户/店铺授权、控制操作和审批。
- Python 负责 AI 工作流和 v2 摄取 job、item、version、chunk 的写入。
- Go 与 Python 通过 gRPC/Protobuf 通信。FastAPI 仅提供内部健康、就绪和开发接口；浏览器不直接访问 FastAPI。
- PostgreSQL/pgvector 与 Redis 提供共享持久化和协调能力。当前开发目标不部署本地模型。

## 工作区职责

- `/opt/ecommerce-rag-v2` 是活动 Dev 工作树，分支为 `refactor/v2-rebuild`。
- `/opt/ecommerce-rag-ops` 是 DB 运维工作区，负责 PostgreSQL/pgvector、Redis、迁移、备份和隔离数据库测试。
- `/opt/ecommerce-rag` 作为只读旧工程参考和回滚来源保留。

## 证据边界

- 实现行为以源码、Proto、迁移和测试为准；运行报告只证明报告明确记录的范围。
- fixture、静态检查、隔离 synthetic、真实 Provider 和生产结果属于不同证据层级。
- 低层检查通过不代表真实身份、真实电商数据、生产就绪或更高里程碑通过。
- V2 开发使用固定身份或本地 OIDC mock、版本化商家 API fake、现有业务文档及受控 Markdown/CSV/DOCX fixture；模型集成与受控全链路使用真实百炼，默认回归保持离线。真实 OIDC/API 缺失不阻塞重构；在线调用显式选择 profile 并遵守授权预算，替代输入保留授权和故障边界，各类证据分别记录。

## 文档职责

- `AGENTS.md` 保存总规则；`HANDOFF.md` 用于窗口交接；本文件保存稳定结论。
- 详细工程资料位于 `.local/dev-docs/docs/`。验收当前门槛在 `验证与验收.md`，历史全文位于 `.local/dev-docs/archive/acceptance-history/`。
- 来源、合成资料和评测适配边界统一维护在 `数据来源与评测输入.md`；不再创建独立的原型来源边界副本。
- 前端总设计、客服交互、管理员控制台和 HITL 保持独立专题，因为它们分别维护应用边界、页面流程、管理权限和人工介入规则。
- 对话和过程记录位于 `.local/dev-docs/archive/`，日常工作不读取；历史快照保持不变。
