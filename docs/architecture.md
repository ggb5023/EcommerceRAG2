# 稳定架构摘要

本文件公开说明 V2 的稳定职责与约束。必须遵守的工程规则见 [AGENTS.md](../AGENTS.md)，已确认的稳定事实见 [FACTS.md](../FACTS.md)，启动、迁移和验证入口见 [README.md](../README.md)。实现字段以 Proto、迁移和源码为准；本文不记录窗口进度、环境地址、预算账本或验收报告。

## 服务与入口

| 层 | 职责与边界 |
|---|---|
| 客服 `web/`、管理 `admin-web/` | Vue/TypeScript 前端通过 Go HTTP/SSE 提交会话、资料和控制操作；不直接访问 Python 或数据库 |
| Go 控制面 | 唯一外部业务入口，负责身份、授权、租户/店铺范围、事实 API、审批、命令对账、业务执行终态、SSE 和审计 |
| Python AI 面 | LangChain/LangGraph 执行理解、检索、Embedding、Rerank、上下文门控、生成、解析和资料摄取；只消费 Go 签发的 scope |
| FastAPI | Python 内部健康、就绪和受保护开发接口，不承载浏览器业务流量或业务执行终态 |
| Proto/gRPC | Go 与 Python 的业务 RPC 契约和调用边界；字段源为 `proto/rag/v1/rag.proto` |
| PostgreSQL/pgvector、pg_trgm | 持久化业务状态、版本、chunk、向量、事件和 checkpoint；受限服务角色落实表级写入所有权 |
| Redis | 提供缓存与协调能力；缓存或锁不能替代数据库中的授权、租约、fencing 和持久化终态 |

活动服务入口是 `go/cmd/gateway`、`app.rpc.server`、`app.rpc.ingest_server` 和 `app.ingest.worker_main`。资料摄取 RPC 与 worker 独立于 AI RPC；Go 通过命令提交和对账驱动摄取，不运行资料 writer。开发工作树、旧工程参考与数据库运维职责见 FACTS；具体运行参数由受限配置提供。

## 身份与适配边界

- **身份**：Go 的 `IdentityAdapter` 将可信身份映射为 tenant、user、角色、店铺范围、权限版本和撤权状态。固定开发身份或本地 OIDC mock 与正式 OIDC 使用同一职责边界；客户端参数和资料 manifest 不能赋予权限。
- **授权**：Go 签发绑定请求及执行身份的 scope，并校验撤权、权限版本、有效期和资源归属。Python 必须回验，在检索、相邻 chunk、重排、生成上下文和引用前按该范围过滤，不得扩大权限。
- **事实 API**：商品、库存、订单和物流等事实由 Go 的 `facts.Provider` 和平台适配器调用只读服务，保留版本、观察时间、mock 标记及租户/店铺/订单归属校验。事实不可用时失败关闭，不以资料快照替代当前事实。
- **资料摄取**：受控源文件经过提交、解析、切片、Embedding、质量门控、审核、发布和查询。源 hash、处理版本、document/version/chunk 绑定及披露资格用于追溯；撤销与回退经控制命令执行。
- **模型 Provider**：Python 通过显式 profile 适配 control、document/query Embedding、Rerank、generation。模型、维度、参数、错误、usage、deadline 和取消语义由 Provider 契约及配置确定；不混用不兼容向量，也不自动切换未经评测的模型。真实模型调用使用已授权环境和共享预算账本；离线 Provider 用于默认回归与故障注入。

开发身份、商家 fake 和受控资料不构成真实身份或真实商家接入；真实模型调用也不能证明生产业务验收。证据分层与限制见 FACTS。

## 数据来源与服务目标

`eval/public-data-sources.manifest.json` 管理公开数据集、开源服务目标和未解决的来源缺口，并记录类别、语言/司法辖区、许可与隐私状态、用途范围和获取状态。候选来源只有在原始条款、许可证范围、revision、字段/隐私审查和仓外存储均落实后才能准入；未满足时保持未下载。原始公开数据不进入 Git.

当前实现仍使用固定开发身份及 Go 商家 facts fake。Keycloak OIDC 是未来本地集成目标，使用本地创建的合成测试账号；所选发行版许可仍需核实，当前没有 Keycloak 服务或真实 OIDC 集成。Medusa 2.21.2 是未来本地 REST 集成目标；记录的 MIT 许可仅覆盖非 Enterprise 内容，Enterprise 内容排除。Medusa 本地服务或测试账号都不代表真实商家 API。

Open Food Facts 是候选食品商品来源；官方条款区分 ODbL 数据库、DbCL 单项内容和 CC BY-SA 商品图片，首批候选排除图片并待字段审查。UCI Online Retail II 页面标注 CC BY 4.0、API 许可字段为空，解决差异并完成隐私审查前不下载或准入。中文官方 FAQ/政策和中文社区问答尚有来源缺口；英文社区问答单独评测并保留语言与许可归属，不能作为商家政策或客服答案真值。公开数据派生的 Medusa 库存/订单是本地测试记录，删除客户标识并绑定来源版本、输入 hash 和转换版本。

以上均是数据来源或服务集成的开发证据，不构成生产商家数据或验收；`real_service_acceptance` 保持 `false`。冻结的 60 条案例、37 份合成文档、答案点和 evidence sidecar 继续独立，不由公开数据补写或改写。

## 跨语言契约

Proto/gRPC 是唯一跨语言业务契约。字段、状态、错误、取消或 deadline 变化先更新 `proto/rag/v1/rag.proto`，再通过 `bash scripts/generate-proto.sh` 同步 Go/Python 生成代码。兼容检查使用 `scripts/check-proto.sh`；不得手改生成文件或另建平行业务 HTTP/JSON 接口。

现有实现存在一个传输差异：`python/app/authorization.py` 使用受限内部 HTTP 向 Go `/internal/authorization/validate` 发送序列化 Proto scope，仅取得授权校验结果。该回验没有独立的业务字段模型，但传输尚未统一为 gRPC；不能将“仅 gRPC 传输”描述为已全面实现，也不能据此新增业务 HTTP 契约。此摘要如实记录差异，不改变该运行接口。

## 持久化与单写者

| 写入方 | 拥有的数据 |
|---|---|
| Go Gateway | 会话、turn、execution、证据快照、引用、`customer_reply`、SSE、授权及审计、控制命令意图和对账 |
| Python AI | ASKING/LangGraph checkpoint、检索 trace |
| Python 摄取 | source、document、job/item/version/chunk、发布状态、摄取命令回执及生命周期事件 |

一张业务表只有一个写入方。Go 业务执行终态与 Python 摄取状态属于不同生命周期；Python 不写 Go 的最终答案、对外 SSE 或业务审计。Go 管理审批产生控制命令，Python 摄取执行并保存回执，Go 按回执对账，双方不直接写对方所属表。具体表和角色权限以 `sql/migrations/` 为准；禁止 SQL 直写最终结果绕过真实流程。

数据库结构变化新增 checksum 迁移，不修改已应用的历史迁移。迁移在独立库、非超级用户且无 `BYPASSRLS` 条件下验证；基础设施管理者预先创建数据库、角色和扩展，业务应用不执行 DDL。

## 执行与恢复约束

- **权限与可见性**：检索仅使用当前授权、可见且发布的版本；过期、撤销或越权证据不能进入生成或成为可复制的顾客回复依据。引用必须绑定正确的 document/version/chunk。
- **幂等与对账**：同一幂等键绑定同一请求摘要；不同输入冲突。控制命令和摄取回执持久化，未知提交通过既有命令对账，不能把超时当作未执行后盲目重做。
- **租约与 fencing**：摄取 job/item 以 owner、租约和单调 fencing epoch 限制写入；接管后旧 worker 不能落库或发布，Redis 锁不能替代数据库条件更新。
- **取消与失败关闭**：显式取消传播至 Go、gRPC、Python 和 Provider，取消后不得发布资料；deadline 和错误分类端到端保留。SSE 断线与显式取消分开处理，重连通过持久化事件回放。
- **终态与恢复**：Go 持久化业务结果及连续 SSE 序号，支持 Last-Event-ID 回放。独立开发库采用单 Gateway 锁；重启将被中断的执行收敛为失败或取消，保留已完成及 ASKING 结果，不自动重发付费模型请求。这不是生产多实例恢复方案。

## 技术栈与版本来源

| 范围 | 技术及权威配置 |
|---|---|
| Go | HTTP/SSE、gRPC、pgx；`go/go.mod`、`go/go.sum` |
| Python | LangChain/LangGraph、FastAPI、gRPC、psycopg；`python/pyproject.toml`、`python/uv.lock` |
| 前端 | Vue、TypeScript、Vite；`web/package.json`、`admin-web/package.json` 及各自 `package-lock.json` |
| Proto | `proto/rag/v1/rag.proto`、`buf.yaml`、`buf.gen.yaml`；仓库生成与兼容检查脚本 |
| 数据库及协调 | PostgreSQL、pgvector、pg_trgm、Redis；`sql/migrations/`、`docker/db/docker-compose.yml`、`docker/db/images.lock.json` |
| 工具链与运行 | `toolchains.lock.json` 及受限运行/Provider 配置；具体版本与参数以当前清单、锁文件和配置为准 |

格式、测试、静态检查与完整工程检查入口由 AGENTS 定义，启动和隔离验证方法由 README 维护。私有专题、运行证据、窗口归档与历史快照留在 `.local/`；公开文档仅提取稳定且不敏感的结论。
