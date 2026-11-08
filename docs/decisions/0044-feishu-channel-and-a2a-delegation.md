# 0044 飞书渠道与 A2A 委托边界

- 状态：提议（方向已由用户确认；采纳与实现需负责人批准并另开工作包）
- 日期：2026-11-08
- 工作包：待建
- 性质：决策输入，不是工作包；不授权实现。
- 关联：[0004](0004-retained-invariants.md)、[0013](0013-merchant-delegated-agent-review.md)、[0021](0021-usage-log-instead-of-budget.md)、[0025](0025-code-level-rules.md)、[0026](0026-proxy-fronted-deployment.md)、[0028](0028-orchestration-boundary.md)、[0032](0032-intelligence-domain-and-crawling.md)、[0041](0041-customer-service-lanes-and-tickets.md)、[0042](0042-account-disable-ingest-serialization.md)、[0045](0045-llm-assisted-crawling.md)

## 1. 问题

仓库当前没有任何 A2A、OpenClaw 或飞书实现；轮次引擎只有浏览器入口与内部 HTTP/SSE。
商家希望在自己的飞书里使用本工程，外部 agent 也希望用标准协议委托任务。需要先把两件事分开：

- **渠道协议**：飞书事件、验签、媒体、消息发送；
- **委托协议**：外部 agent 把目标交给本工程，由 Go 控制面决定授权、状态与落库。

两者若混在本仓，会新增一套外部入口、第二套状态机和一整套飞书凭据，直接冲击 0004 的保留约束。

## 2. 决定

### 2.1 渠道归属：本仓不接飞书

飞书渠道完全由**仓外 OpenClaw** 与官方 larksuite/openclaw-lark 插件承担。本工程：

- 不接收飞书回调，不实现飞书事件订阅、验签/解密、媒体下载、消息发送；
- 不持有飞书 `app_secret`、verification token、encrypt key；
- 不 fork、不 vendor OpenClaw；OpenClaw 是可选外部组件，不进入默认三服务 Compose；
- 唯一集成契约是 A2A：OpenClaw 作为 A2A client，本工程作为 A2A server。

这样 Feishu 的 Node/插件依赖、应用审核与消息格式都留在本仓之外。

### 2.2 对外 agent 面

v1 **只暴露 `data-acquisition`**：先只读 `ListObservations`，再开放长任务委托。
`customer-service` 放到 P4，另开工作包（需要 turn 引擎改动），不进入 v1。

内部子智能体、LangGraph 节点、`intel` worker 一律不作为 agent/skill 暴露。
本决策不做通用 Agent 平台、agent builder、动态注册或多 agent 编排。

### 2.3 传输、路由与 Agent Card

v1 只做 **JSON-RPC 2.0 over HTTPS**：

```text
POST /v1/a2a/{agent}
GET  /v1/a2a/agents/{agent}/agent-card.json
```

方法集：message/send、message/stream、tasks/get、tasks/cancel、tasks/resubscribe。

v1 不支持 push notifications、gRPC transport、标准 `/.well-known/agent-card.json`。
若 OpenClaw 强制要求 well-known，同一工作包内改 `go/internal/edge/edge.go:67-75`、
`go/internal/gateway/routes_middleware.go:58-78` 与 `scripts/check/check-openapi.py:12`，
并补 `docs/openapi.yaml`；不另起协议。

A2A 认证必须独立于浏览器 OIDC/CSRF：挂在 `/v1/customer/` 分支旁、早于
browser 的 localAuth.ValidateWrite，不要挂 `businessRoutes`；不接受浏览器 cookie/Origin，
A2A 凭据与浏览器 token audience 分离。

Agent Card 只声明实际启用的接口与安全方案，不列内部工具名、不列未启用的 scope。

### 2.4 任务单位与状态映射

- v1 `data-acquisition`：1 个 A2A task = 1 个 `intel_job`。
- P4 `customer-service`：1 个 task = 1 个 turn = N 个 execution；续接走
  `resumeTurnExecution`（`go/internal/gateway/turn_resume.go:106-178`），终态 task 不可续。
- A2A 状态是**只读投影**，绝不写 `turn.status`、`conversation_execution.status`、
  `intel_job.status`；`a2a_task` 不设状态列。

| A2A | v1 数据获取 | P4 客服 |
| --- | --- | --- |
| `submitted` | `pending` | — |
| `working` | `running` | `EXECUTING` / `CANCEL_REQUESTED` |
| `input-required` | `waiting_input` | `ASKING` |
| `completed` | `done` | `DONE` |
| `failed` | `failed` | `FAILED` |
| `canceled` | `cancelled` | `CANCELLED` |

`rejected` 与 `auth-required` 是入口的 HTTP/JSON-RPC 错误（401/403/404/429），
不创建持久 task。

终态硬规则：

- `terminal()`（`go/internal/gateway/main.go:568-575`）包含 `ASKING`，适配层不得用它判断
  A2A 任务结束；
- `completed` SSE 事件不等于成功（`go/internal/gateway/recovery.go:102-107`），必须读
  `payload.status`；
- SSE 流关闭不等于完成（`go/internal/gateway/turn_handlers.go:258-263`），关闭前回查
  `conversation_execution.status`；
- 终态只由 `finishTurn`（`turn_persist.go:75-112`）与 `persistExecutionTerminal`
  （`recovery.go:41-108`）写。

取消复用 `cancelTurn` 语义（`go/internal/gateway/turn_handlers.go:347-386`）：
内部已是终态（含 `ASKING`）时返回当前状态且不取消；非终态写 `CANCEL_REQUESTED`，
A2A 保持 `working`，直到 `finishTurn`/recovery 真正写 `CANCELLED`；适配层不得自建取消。

### 2.5 持久化与幂等

新增 `a2a_task` 表（迁移 `sql/migrations/0062_a2a_task.sql` 待建），只存映射，不存业务状态：

```text
tenant_id, task_id, context_id, agent_id, client_id,
creation_message_id, input_hash,
turn_id NULL, intel_job_id NULL,
created_at, updated_at
CHECK (turn_id IS NOT NULL) <> (intel_job_id IS NOT NULL)
UNIQUE (tenant_id, task_id)
UNIQUE (tenant_id, client_id, creation_message_id)
```

不存状态、不存提案、不存 artifact；`resubscribe` 以现有 `sse_event` 保留与客户端
`Last-Event-ID` 为准。SQL 只出现在 `go/internal/gateway/store_a2a_task.go`（待建），
迁移同文件 GRANT 给 `rag_v2_gateway`；写迁移前先在 `docs/data-ownership.md` 登记。

幂等：A2A `messageId` 到内部键 `a2a:<client_id>:<task_id>:<message_id>`；
`intel_job.a2a_message_id` 唯一，`CreateCrawlJob` 幂等；先建 job 再插 `a2a_task`，
重试按 messageId 收敛；同一 messageId 不同 `input_hash` 返回 409/JSON-RPC 参数错误。
不得依赖内部 turn hash（`turn_create.go:77` 只含 query+parent_turn_id）。

### 2.6 认证、租户与授权

v1 默认 **0600 静态 client registry**：`client_id`、`token_sha256`、`tenant_id`、
`user_id`、`shop_ids`、`role`、`allowed_agents`；企业自部署可选 mTLS。
OAuth2 `client_credentials` 延后，需单独决策与 IdP。

- 不复用浏览器 OIDC cookie、CSRF、localAuth 会话；
- 每次请求重查 `app_user` 禁用/撤销状态与 `permission_revision`，未绑定/已禁用/过期一律 fail closed；
- 建议一员工一个 `client_id`，保留审计与吊销粒度；若改 per-tenant 服务账号，必须限制
  `allowed_agents`/`shop_ids` 并写明员工级吊销失效；
- 授权只用 `requireWriteRole`（`authorization.go:19-25`）、`authorizeExecution`（:80-98）、
  `conversationHistoryStale`（`turn_create.go:183-189`）；scope 只经 `gateway.authority()`
  （`authorization.go:100-109`）签发，不 new `auth.Authority`、不自 mint scope；
- 沿用 answer/intelligence 两个域，不新增第三域。

### 2.7 限流、审计、日志与出域

- 按 `client_id` 与 `tenant_id` 设 RPM、并发 task 数、请求体/part 大小、最长流连接；
  超限 429 + `Retry-After`；拒绝 JSON-RPC batch、file/base64 part、未知方法。
- 审计复用 `admin.AppendAudit` 或新增 gateway 审计端口；不得新增 gateway→admin import
  文件，不得提高 `GATEWAY_ADMIN_BASELINE`（`scripts/check/check-structure.py:36-39`）。
- 日志禁列见 `docs/logging.md:6-28`；只记 token 与次数，不出现金额（0021）。
- A2A 输入中的 URL/域名/配方一律不可信；适配层与 gateway 不得直接发起任意出站请求；
  v1 `pushNotifications=false`；抓取只能经 intel worker 并受 0045 约束；出口代理默认关。
  安全段只写地址类别（私网/回环/链路本地/CGNAT/云元数据/保留/组播/文档网段），
  不写具体 IPv4 字面量。
- A2A artifact 白名单只含 answer、citations、clarification、can_copy、blocked_reason、
  `external_allowed`/`customerEligible` 的 evidence；不返回 facts_json 原始、trace、
  prompt、内部错误 message、其他租户数据。审计只存 client/tenant/task/action/result/reason
  与哈希。

### 2.8 部署与开关

A2A 属于现有 gateway 进程，落 D1/D2/D3（`docs/decisions/0026-proxy-fronted-deployment.md:29-31`），
不改 `WEB_ROOT`/`HTTP_ADDR` 语义，不新增公共端口、第二 gateway/edge、独立写库进程。

`V2_A2A_ENABLED=0` 默认；键与默认值只在 `docs/install.md` 单点定义，同步 `.env.example`
与 `scripts/dev/v2_services.py` 的 `SERVICE_KEYS`。关闭时 `/v1/a2a/` 不启用、不提供 Card；
不改 `validateDeploymentForm`。

## 3. 边界与不变量

- 终态仍只由 Go 写；A2A 状态是投影，不形成第二状态机；
- 单写者、幂等、事件顺序、角色隔离、无货币均未放宽（0004、`docs/architecture.md:65-75`）；
- 内部子智能体、LangGraph 节点、intel worker 不作为 agent/skill 暴露；
- 0028 当前是提议，只作支持依据；0004 与 architecture 是已采纳依据。

## 4. 阶段

| 阶段 | 内容 | 退出条件 |
| --- | --- | --- |
| P0 | 本决策与 0045 文档、INDEX/constraints 同步 | 文档门禁全绿，不改代码 |
| P1 | Go A2A 骨架：Card、JSON-RPC、认证、`a2a_task`、只读 `ListObservations` | 单测 + 租户隔离 + Card 可读 |
| P2 | intel 长任务委托 + 0045 规划/执行 | 一个 goal 走完输入确认到完成/取消 |
| P3 | OpenClaw + 官方飞书插件试点 | 飞书端到端一条消息可完成委托 |
| P4 | `customer-service` 暴露 | 另开工作包；turn 引擎改动单独评审 |
| P5 | 原生飞书适配器 / OAuth2 / SDK / well-known 决策 | 按试点数据另写决策 |

## 5. 验收

P0（本轮）：`bash scripts/check.sh` 与 `check-decisions-index.py`、`check-doc-paths.py`、
`check-doc-numbers.py`、`check-docs-drift.py` 全绿；不改代码、不改迁移、不跑 smoke。

实现阶段：`go build ./... && go vet ./...`；`go/internal/agentgateway`（待建）单测；
`bash scripts/check.sh --smoke` 逐字节一致；`bash scripts/check.sh --db`；OpenClaw 手工清单
（Card 发现、message/send、tasks/get、tasks/cancel、input-required、重连 resubscribe）。
缺前置时 `UNCONFIGURED` 不算通过，必须在汇报中点名。

## 6. 风险

- `ASKING` 是内部终态，若误当 A2A `input-required` 会提前结束任务；
- 长任务跨重启、`a2a_task` 保留期与 resubscribe 回放；
- OpenClaw 的 A2A 插件是否在你使用的版本可用；不可用则先用 MCP/HTTP 过渡；
- 静态 client registry 的 token 轮换与吊销；OAuth2 延后的合规缺口；
- SSRF、数据出域、单实例限流在多副本下失效；
- 门禁缺口：新包 `go/internal/agentgateway`（待建）当前不被 `check-stage-boundaries.py` 覆盖；
- 与 product.md “不做 Agent 平台”的定位冲突，需负责人批准窄口径例外。

## 7. 备选方案与被否决

| 备选 | 否决理由 |
| --- | --- |
| fork/vendor OpenClaw 或其飞书插件 | 继承 Node/TS 栈与发布节奏；本工程核心是 Go/Python |
| 先做原生 Go 飞书适配器 | v1 工作量最大；先把 A2A 委托跑通，P5 再按试点决定 |
| MCP-only | MCP 是工具协议，承载不了长任务、`input-required` 与目标委托 |
| 独立 A2A 微服务 | 形成第二控制面/第二状态机；放现有 gateway 最稳 |
| Python 侧实现 A2A | 绕过 Go 的授权、幂等与终态 |
| 暴露内部子智能体 | 绕过主 agent 的规划、预算与审计 |
| SSE 直通给外部 | 泄露内部事件与字段；必须做 artifact 白名单 |
| OAuth2-first | 需要 IdP/JWKS/scope 映射；v1 先用静态 registry |
| per-tenant 共享 client | 失去员工级审计与吊销粒度 |

## 8. 被修订条款与文档同步

本决策是**提议**，P0 只记录拟改文本，不直接改产品文档：

- `docs/product.md:59` “首版不做通用工作流编排、Agent 平台”——拟加窄口径例外：
  “除 0044 定义的两个固定 agent 外部委托入口外”；须负责人批准，不能据此直接改文。
- `docs/product.md:22`、`:38`、`:62`：区分“电商平台开放接口”与“飞书办公消息渠道”，
  说明本决策不是被禁止的电商平台深度对接。
- `docs/product.md:38`、`docs/decisions/0001-product-positioning.md:13`、`ROADMAP.md:55-58`：
  P4 开放 API 改为“设计先行，实施时再更新 ROADMAP”；P0 不改 ROADMAP 当前阶段。
- `docs/product.md:52`、`:73` 的“模型费用/单次成本”按 [0021](0021-usage-log-instead-of-budget.md)
  第 37 行的口径改为 token 与次数。
- [0032](0032-intelligence-domain-and-crawling.md) 第 38 行的 P4 顺序加注“被 0044 讨论”。
- 实现阶段同步 `docs/install.md`、`docs/data-ownership.md`、`docs/contracts.md`、
  `docs/error-taxonomy.md`、`docs/openapi.yaml`、`.env.example`、`scripts/dev/v2_services.py`。

## 9. 待决问题

1. 是否批准 0044 以“两个固定 agent 的窄口径外部委托”例外进入，而 P0 只记录未来 ROADMAP 修改？
2. v1 是否接受 0600 静态 client registry + 可选 mTLS，把 OAuth2 延后？
3. v1 是否接受 `data-acquisition` 先行、`customer-service` 放 P4 并另开工作包？
4. OpenClaw 的 A2A 插件在你的目标版本是否可用？不可用时是否接受先用 MCP/HTTP 过渡？
5. `a2a_task` 的保留期、审计留存与多副本限流策略由谁定？

## 10. 不要做

- 不 fork/vendor OpenClaw；不做原生 Go 飞书适配器（P5 再议）；
- 不暴露内部子智能体、LangGraph 节点、intel worker；
- 不做通用 Agent 平台/agent builder/动态注册/多 agent 编排；
- 不做 MCP server/tools（另开决策）；
- 不新增公共端口、第二 gateway、独立写库进程；不改终态归属/幂等/事件顺序/落库；
- 不引入货币/成本/预算；不改已执行迁移；不把飞书 app secret 放进本仓。
