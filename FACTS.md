# 长期事实

## 架构结论

- 产品定位为面向商家的开源电商客服 RAG 系统。首版默认单商家、单店铺，保留租户/店铺隔离；提供商家知识管理、客户聊天和真实客服工作台。自有代码采用 MIT，第三方保留各自许可证与声明。
- 当前交付优先商家源码部署；已有容器实现保留，当前阶段不新增 Docker 开发。源码部署须独立验证，不能直接沿用容器验收结论。
- 系统采用 Go 控制面和基于 LangChain/LangGraph 的 Python AI 面。
- 浏览器和外部业务流量的入口是 Go。Go 持有身份、授权决策、租户/店铺范围、业务执行终态、SSE、审批和对外审计。
- Python 负责 LangChain/LangGraph AI 工作流，只消费 Go 签发的 scope 并执行防御性过滤，不签发权限或发布 Go 业务终态。
- Go 与 Python 的业务 RPC 以 `proto/rag/v1/rag.proto` 为契约源。自部署跨容器业务及 `AuthorizationService.ValidateScope` 回验使用 mTLS gRPC；内部 HTTP 回验仅保留显式本机开发兼容路径，不作为容器部署入口。FastAPI 仅提供内部健康、就绪和开发接口。
- V2 表级写入按服务角色划分：Go 写员工及客户会话、执行、终态结果、消息、人工控制权、SSE、控制命令和授权审计；Python AI 写 checkpoint/retrieval trace 及独立 customer_retrieval_trace；Python 摄取单写 source、job、item、version、chunk、发布状态和摄取命令回执。每张业务表只有一个写入方，Go 提交意图与 Python 摄取回执分表保存。
- 表级权限及单写者边界由 checksum 迁移和受限数据库角色落实；应用进程不执行 DDL，已应用迁移保持不变，新结构通过新增迁移引入。
- PostgreSQL/pgvector 与 Redis 提供共享持久化和协调能力。当前开发目标不部署本地模型。
- OIDC 身份按请求隔离，并绑定到既有 Go 数据库 tenant/user/shop；bearer profile 的 token 角色、范围与权限版本必须匹配，不能创建或扩权。Go 同时校验签名、issuer、audience、真实时间到期与实时 introspection。旧 token 不自动升级；Python scope 回验关联同一身份的在线活跃状态。
- 员工浏览器使用独立 `oidc_browser` profile，经 confidential 授权码和 PKCE S256 登录 Keycloak；令牌只在 Go，浏览器持有 HttpOnly 不透明 Cookie，写请求校验 Origin/CSRF。进程内会话最多 4096 个、绝对期限 30 分钟、登录事务 5 分钟；Go 重启后重新登录。自部署固定 subject 映射后以 Go 数据库提供角色、店铺、权限版本和撤权状态；刷新不能升级旧会话授权，权限变化使旧会话失效。
- Go 控制面可通过显式 `V2_FACTS_BASE_URL` 加载版本化 `facts.HTTPProvider`；未配置时事实查询 fail-closed。`go/cmd/facts-fake` 是 loopback 开发 fake，固定返回商品/库存/价格/订单/物流并带 mock、版本、观察时间和租户/店铺绑定。
- Go 显式区分 development、OIDC bearer 和浏览器身份 profile，不隐式回退。Keycloak 的固定发行版、Apache-2.0 与 notice 指纹由 `docker/identity/images.lock.json` 管理；本地合成账号集成不代表生产身份。版本化 facts fake 仅用于冻结合成回归；自部署客户首版不接入实时订单、库存、价格或配送承诺工具。Medusa、Open Food Facts 双轨、平台专用适配器、API Key 和独立检索 API 暂缓，已有来源及许可审计保留。后续平台连接器由商家或集成方实现，本系统提供标准接口；公开数据派生的本地库存/订单仍属测试记录。
- 冻结的 60 条合成案例、37 份合成文档、批准答案点和 evidence sidecar 属于独立基线；公开来源清单建立隔离数据轨，不修改或补写该基线真值。

## 商家知识客服闭环

- 管理台为 DOCX、Markdown、CSV 上传及 FAQ 表单生成摄取 manifest，资料实际经过解析、切片、Embedding、质量门控、审核和发布；修改 FAQ 生成新版本。撤销及回退沿用真实命令流程，不 SQL 补写终态。首版不包含 PDF、XLSX、OCR 或自动抓取。
- 客户主体独立于员工账户。随机 Cookie 的凭据摘要保存在数据库，默认闲置 24 小时、绝对 7 天，绑定 tenant/shop/visitor；Go 重启可恢复，丢失 Cookie 无法找回匿名历史。客户 scope 仅允许问答，不能获得管理或摄取权限。
- `/v1/customer/` 提供客户会话、留言、SSE、转人工和来源查看；`/v1/support/` 提供店铺队列、领取、续期、释放、人工回复、交回 AI 和关闭。客户写请求校验 Origin、CSRF、幂等键，默认每访客每分钟 5 次、每 IP 20 次、每会话一个 AI 执行。
- Python 的 `ANSWER`、`CLARIFY`、`HANDOFF` 是建议，Go 检查当前授权、审核发布版本、对外披露、引用与完整性后发布。客户只接收进度和获准的最终消息，不接收原始模型增量、内部证据或工具内容；员工 `customer_reply`/`can_copy` 资格独立保留。
- 一次必要澄清后仍不足、资料缺失/冲突、模型失败、超出知识能力或客户主动请求人工时进入队列。来源只返回获准标题、版本及支持片段，不暴露存储路径；撤销、回退和权限收紧也重新过滤历史正文、链接与 SSE 回放。
- 服务控制状态 `AI → WAITING_HUMAN → HUMAN → AI / CLOSED` 与 RAG 执行状态独立。领取由数据库原子操作保证唯一客服，默认有效 120 秒、每 30 秒续期；控制权变化增加 epoch，阻止迟到 AI、过期领取和重复发送。等待/人工期间客户可继续留言，AI 不自动回复。
- 人工回复标记人工来源，不伪造引用且不自动写知识库。释放、退出、撤权、到期或 Go 重启使领取重新排队；未完成 AI 失败转人工，已发布消息不重复发送。交回 AI 后由下一条客户消息触发执行。
- 已有单机 Compose 实现包含 Web 反向代理和静态前端、Go、三个 Python 进程、PostgreSQL/pgvector、Redis、Keycloak；仅开放 Web，内部 RPC 使用 mTLS。独立 bootstrap 执行 checksum 迁移，应用不执行 DDL；模型账户、秘密、资料卷和预算账本由安装者在仓外提供，不带开发凭据。
- 自部署备份覆盖 RAG/Keycloak 数据库、资料、身份配置、信任材料与预算；恢复到独立 project 并核对数据指纹，不能覆盖活动库或重建账本。正式 CA、公网镜像拉取、容量、多实例和生产外部服务仍须各自验证。
- Provider 对 429、5xx、连接超时最多再尝试一次，两次共用原 deadline，首次保留后续时间且每次记原账本；非法结构、截断、授权和 TLS 错误不自动重试。证据引用沿用实际 `Evidence.rank`，过滤后允许编号间隔，不按数组位置重排标签。

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
- control 指代解析只消费获准历史中的用户消息；助手提到的来源或候选不能改变用户的唯一资源选择。资源已确定而版本、发布日期缺失时，通过当前授权证据说明缺口，不把资料缺口误判为资源身份未确定。
- 公开来源准入由 `eval/public-data-sources.manifest.json` 记录分类、许可/隐私状态、语言/司法辖区和用途边界；待核验来源不得下载或标为准入。公开数据、开源服务本地集成和派生本地测试记录是不同证据类别，均不代表生产商家数据。
- 冻结合成评测沿用开发身份、版本化 facts fake、既有真值和受控资料；独立商家客户评测使用本地 Keycloak 员工、隔离访客及 DOCX/Markdown/CSV/FAQ 生命周期。两轨分别记录，模型集成与受控全链路使用真实百炼，默认回归保持离线；不证明生产 OIDC、生产商家 API 或真实商家资料质量。
- `eval/customer-service-contract.v1.json` 和 `eval/customer-service-cases.v1.json` 在生成前冻结独立客户闭环契约、查询及来源 hash。逐例代理复核绑定实际 capture 和交付引用；普通至少 90%、必需证据及适用引用至少 95%、高风险全通过、严重安全错误为零。其通过不能覆盖原答案点门禁缺口，`real_service_acceptance=false` 保持有效。

## 文档职责

- 根目录 `AGENTS.md` 保存必须遵守的规则；本文件保存已确认的稳定事实；`HANDOFF.md` 用于当前窗口交接。三者与 `README.md` 均受 Git 管理。
- `docs/architecture.md` 及指定 `docs/install.md`、`knowledge.md`、`support.md`、`backup.md`、`development.md` 是受 Git 管理的公开文档；其余 `docs/` 默认忽略。`.local/` 是私有目录，不进入 Git；公开文档检查不依赖私有专题存在。
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
