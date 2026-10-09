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

## 文档职责

- `AGENTS.md` 保存总规则；`HANDOFF.md` 用于窗口交接；本文件保存稳定结论。
- 详细工程资料位于 `.local/dev-docs/docs/`。对话和过程记录位于 `.local/dev-docs/archive/`，日常工作不读取。
