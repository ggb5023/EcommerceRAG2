# 长期事实

## 架构结论

- 系统采用 Go 控制面和基于 LangChain/LangGraph 的 Python AI 面。
- Go 是对外业务入口，负责身份、租户/店铺授权、控制操作和审批。
- Python 负责 AI 工作流和 v2 摄取 job、item、version、chunk 的写入。
- Go 与 Python 通过 gRPC/Protobuf 通信。FastAPI 仅提供内部健康、就绪和开发接口；浏览器不直接访问 FastAPI。
- PostgreSQL/pgvector 与 Redis 提供共享持久化和协调能力。当前开发目标不部署本地模型。
- Go 控制面可通过显式 `V2_FACTS_BASE_URL` 加载版本化 `facts.HTTPProvider`；未配置时事实查询 fail-closed。`go/cmd/facts-fake` 是 loopback 开发 fake，固定返回商品/库存/价格/订单/物流并带 mock、版本、观察时间和租户/店铺绑定。

## 工作区职责

- `/opt/ecommerce-rag-v2` 是活动 Dev 工作树，分支为 `refactor/v2-rebuild`。
- `/opt/ecommerce-rag-ops` 是约定的独立 DB 运维工作区，负责 PostgreSQL/pgvector、Redis、迁移、备份和隔离测试；本开发主机没有该目录，不能据此推断远端运维环境已部署。当前隔离验收使用显式配置的独立测试库。
- `/opt/ecommerce-rag` 作为只读旧工程参考和回滚来源保留。
- V2 活动入口固定为 `go/cmd/gateway`、`app.rpc.server`、`app.rpc.ingest_server` 和 `app.ingest.worker_main`。旧 Python RPC 入口和 synthetic HTTP 工具已退出；HTTP `/v1`、Proto `rag.v1`、历史迁移及数据库约束 fixture 保留。Go 不运行资料 writer。

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

## V2 Gateway 恢复边界

- 每个独立开发数据库只允许运行一个 V2 Gateway；专用连接持有 session advisory lock `(742019,1)`，第二实例拒绝启动。
- Gateway 在 ready 前恢复活动 execution：PENDING/EXECUTING 变为 FAILED/`gateway_restarted`，CANCEL_REQUESTED 变为 CANCELLED/`cancelled`，终态结果与连续 SSE 事件同事务持久化；不自动重发模型请求。DONE、ASKING 和 checkpoint 保持不变。
- 对外 SSE 序号由数据库事务分配并按 Last-Event-ID 回放。该策略已在独立开发库实际中断、重启和新请求流程验证，不代表生产高可用多实例方案。
