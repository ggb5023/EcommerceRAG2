# 长期事实

## 架构结论

- 系统采用 Go 控制面和基于 LangChain/LangGraph 的 Python AI 面。
- 浏览器和外部业务流量的入口是 Go。Go 持有身份、授权决策、租户/店铺范围、业务执行终态、SSE、审批和对外审计。
- Python 负责 LangChain/LangGraph AI 工作流，只消费 Go 签发的 scope 并执行防御性过滤，不签发权限或发布 Go 业务终态。
- Go 与 Python 的业务 RPC 以 `proto/rag/v1/rag.proto` 为契约源，通过 gRPC 通信；当前 scope 回验使用内部 HTTP 传递序列化 Proto，该传输差异在 `docs/architecture.md` 中说明。FastAPI 仅提供内部健康、就绪和开发接口。
- V2 表级写入按服务角色划分：Go 写会话、执行、终态结果、SSE、控制命令和授权审计；Python AI 写 checkpoint/retrieval trace；Python 摄取单写 source、job、item、version、chunk、发布状态和摄取命令回执。每张业务表只有一个写入方，Go 提交意图与 Python 摄取回执分表保存。
- 表级权限及单写者边界由 checksum 迁移和受限数据库角色落实；应用进程不执行 DDL，已应用迁移保持不变，新结构通过新增迁移引入。
- PostgreSQL/pgvector 与 Redis 提供共享持久化和协调能力。当前开发目标不部署本地模型。
- Go 控制面可通过显式 `V2_FACTS_BASE_URL` 加载版本化 `facts.HTTPProvider`；未配置时事实查询 fail-closed。`go/cmd/facts-fake` 是 loopback 开发 fake，固定返回商品/库存/价格/订单/物流并带 mock、版本、观察时间和租户/店铺绑定。
- 当前开发身份与商家事实仍由固定身份/本地 mock 和版本化 facts fake 提供；Keycloak OIDC 与 Medusa REST 是后续本地开源服务目标，尚未部署或集成。Medusa 本地数据和由公开数据派生的交易记录属于测试数据，不是生产商家数据。
- 冻结的 60 条合成案例、37 份合成文档、批准答案点和 evidence sidecar 属于独立基线；公开来源清单建立隔离数据轨，不修改或补写该基线真值。

## 工作区职责

- `/opt/ecommerce-rag-v2` 是 V2 活动 Dev 工作树。
- `/opt/ecommerce-rag-ops` 是约定的独立 DB 运维工作区，负责 PostgreSQL/pgvector、Redis、迁移、备份和隔离测试；该职责约定不证明实际运维环境已部署。
- `/opt/ecommerce-rag` 作为只读旧工程参考保留；未提交改动不是可验证的回滚基线，回滚须另有已验证的代码与数据库备份。
- V2 活动入口固定为 `go/cmd/gateway`、`app.rpc.server`、`app.rpc.ingest_server` 和 `app.ingest.worker_main`。旧 Python RPC 入口和 synthetic HTTP 工具已退出；HTTP `/v1`、Proto `rag.v1`、历史迁移及数据库约束 fixture 保留。Go 不运行资料 writer。

## 证据边界

- 实现行为以源码、Proto、迁移和测试为准；运行报告只证明报告明确记录的范围。
- fixture、静态检查、隔离 synthetic、真实 Provider 和生产结果属于不同证据层级。
- 低层检查通过不代表真实身份、真实电商数据、生产就绪或更高里程碑通过。
- 语义复核区分人工、受明确委托的代理及工程诊断。代理复核保留 `reviewer_kind=agent` 和委托来源，不成为 `human`；词面诊断不成为语义批准。HTTP 答案必须独立绑定其 capture、实际证据和冻结真值，质量与安全门槛不因审核者变化而降低。
- 原答案点质量评分与 `eval/evidence-boundary-contract.v1.json` 的独立证据边界评分分别保存；正确说明输入缺口不等于原答案点已支持。新契约保持冻结输入、风险和历史证据，只批准合成资料上的证据边界验收，不能批准生产外部服务。
- Generate 通过兼容 Proto 历史字段接收 Go 当前权限过滤的最多六条、8192 字节消息，仅用于指代。评测业务日期可重现资料时效；身份、权限、撤权、deadline 与 Provider 配额始终使用真实时间。
- 公开来源准入由 `eval/public-data-sources.manifest.json` 记录分类、许可/隐私状态、语言/司法辖区和用途边界；待核验来源不得下载或标为准入。公开数据、开源服务本地集成和派生本地测试记录是不同证据类别，均不代表生产商家数据。
- V2 开发当前使用固定身份、本地商家 API fake、现有业务文档及受控 Markdown/CSV/DOCX fixture；模型集成与受控全链路使用真实百炼，默认回归保持离线。真实 OIDC/API 尚未接入；在线调用显式选择 profile 并遵守预算规则，各类证据分别记录。

## 文档职责

- 根目录 `AGENTS.md` 保存必须遵守的规则；本文件保存已确认的稳定事实；`HANDOFF.md` 用于当前窗口交接。三者与 `README.md` 均受 Git 管理。
- `docs/architecture.md` 是受 Git 管理的公开稳定架构摘要；其余 `docs/` 默认忽略。`.local/` 是私有目录，不进入 Git；公开文档检查不依赖私有专题存在。
- 详细工程资料位于 `.local/dev-docs/docs/`。验收当前门槛在 `验证与验收.md`，历史全文位于 `.local/dev-docs/archive/acceptance-history/`。
- 来源、合成资料和评测适配边界统一维护在 `数据来源与评测输入.md`；不再创建独立的原型来源边界副本。
- 前端总设计、客服交互、管理员控制台和 HITL 保持独立专题，因为它们分别维护应用边界、页面流程、管理权限和人工介入规则。
- 对话和过程记录位于 `.local/dev-docs/archive/`，日常工作不读取；历史快照保持不变。

## Git 托管

- 当前约定 `origin` 是 Gitee 主仓，`github` 是 GitHub 同步镜像；配置可由 `git remote -v` 核对，远端名称不能代替实际配置。
- 仓库钩子位于 `.githooks`，维护者提交身份为 `EcommerceRAG Maintainers <maintainers@example.invalid>`。本地提交与远端发布是独立操作，远端写入采用明确授权边界。

## V2 Gateway 恢复边界

- 每个独立开发数据库只允许运行一个 V2 Gateway；专用连接持有 session advisory lock `(742019,1)`，第二实例拒绝启动。
- Gateway 在 ready 前恢复活动 execution：PENDING/EXECUTING 变为 FAILED/`gateway_restarted`，CANCEL_REQUESTED 变为 CANCELLED/`cancelled`，终态结果与连续 SSE 事件同事务持久化；不自动重发模型请求。DONE、ASKING 和 checkpoint 保持不变。
- 对外 SSE 序号由数据库事务分配并按 Last-Event-ID 回放。该策略属于独立开发数据库的单实例恢复边界，不代表生产高可用多实例方案。
