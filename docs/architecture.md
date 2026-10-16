# 架构

[项目入口](../README.md) · [产品定位](product.md) · [路线图](../ROADMAP.md) · [开发指南](development.md)

本文描述**目标架构**和必须保留的设计约束，并列出当前实现与目标的差距。字段和表结构以 `proto/`、`sql/migrations/` 和源码为准，本文不重复。

## 目标运行时

```text
浏览器  /shop（顾客）  /support（客服）  /admin（商家）
   │ HTTPS：HTTP + SSE
┌──▼─────────────────────────────┐
│ Go gateway（唯一对外入口）       │
│  认证与会话 · 授权与店铺范围     │
│  顾客会话 · 人工队列 · SSE       │
│  管理 API · 审计 · 静态前端      │
└──┬─────────────────────────────┘
   │ gRPC（proto/rag/v1/rag.proto）
┌──▼─────────────────────────────┐
│ Python ai（单进程）              │
│  RAG 工作流（LangGraph）         │
│  检索 · 重排 · 生成 · 引用校验    │
│  摄取 worker 循环：解析/切片/向量 │
└──┬─────────────────────────────┘
┌──▼──────────────────────────────────────┐
│ PostgreSQL 16 + pgvector + pg_trgm       │
│ 业务表 · 向量 · 摄取任务 · SSE 事件 · checkpoint │
└─────────────────────────────────────────┘
可选：Keycloak（OIDC）· Redis（缓存）    外部：百炼模型 API 或离线 mock
```

默认部署是 3 个服务：`gateway`、`ai`、`postgres`。Keycloak 和 Redis 只在显式启用时运行。

## 组件职责

| 组件 | 负责 | 不负责 |
| --- | --- | --- |
| `frontend/`（单一 Vue 工程） | 顾客页、客服工作台、商家管理台，按路由分区 | 直接访问 Python 或数据库 |
| Go gateway | 身份（内置本地账号，OIDC 可选）、授权、店铺范围、顾客与员工会话、人工队列与领取、消息发布、SSE、管理 API、审计 | 检索、生成、资料解析 |
| Python ai | 问题理解、检索、重排、上下文门控、生成、引用校验；资料解析、切片、Embedding、发布执行 | 签发权限、决定最终发布、对外 HTTP |
| PostgreSQL | 全部持久状态，包括摄取任务队列（`SKIP LOCKED`）和租约 | — |
| 模型 Provider | 百炼（生成、Embedding、Rerank）和离线 mock，共用一个接口 | 自动切换未评测的模型 |

## 核心流程

**资料**：商家上传 → Go 记录提交意图 → Python worker 解析、切片、Embedding → 商家预览并审核 → 发布 → 可撤回、回滚。只有已发布、在有效期内、允许对外的版本才能参与回答。

**问答**：顾客或客服提问 → Go 校验身份并签发 scope → Python 在 scope 内检索并生成 ANSWER、CLARIFY 或 HANDOFF 建议 → Go 重新校验权限、版本和引用后发布 → SSE 推送。最多追问一次，之后仍不足就转人工。

**人工**：会话状态是 `AI → WAITING_HUMAN → HUMAN → AI / CLOSED`。客服原子领取，租约 120 秒，每 30 秒续期；控制权变化时 epoch 递增，迟到的 AI 结果和过期客服的回复都会被拒绝。

## 契约

- 浏览器到 Go：HTTP/SSE。顾客 `/v1/customer/`，客服 `/v1/support/`，管理 `/admin/v1/`。写请求校验 Origin、CSRF 和 Idempotency-Key。P1 补 OpenAPI 描述。
- Go 到 Python：只用 `proto/rag/v1/rag.proto`。改动顺序是先改 proto，再运行 `bash scripts/dev/generate-proto.sh`，最后用 `buf breaking` 检查兼容。不新增平行的 HTTP/JSON 业务接口。容器之间走 mTLS。
- 数据库：只通过 `sql/migrations/` 的 checksum 迁移演进。

## 必须保留的设计约束

这些约束来自现有实现，已被测试和验收证明有价值，重构时不能丢。保留理由见 [决策 0004](decisions/0004-retained-invariants.md)。

1. **Go 是唯一对外入口**。浏览器流量只到 Go；Python 的 FastAPI 只提供内部健康检查。
2. **权限只由 Go 决定**。Python 只使用 Go 签发的 scope，并在检索、上下文和引用时再过滤一遍，不能扩权。
3. **一张表只有一个写入方**，由受限数据库角色落实。Go 写会话、消息、执行、审计和控制命令；Python 写摄取的 job、version、chunk、发布状态以及 AI trace。双方不直接写对方的表。
4. **迁移不可修改**。结构变化只能新增 checksum 迁移；应用进程不执行 DDL；在非超级用户、无 `BYPASSRLS` 的库上验证。
5. **幂等**：同一幂等键绑定同一请求摘要，输入不同就拒绝。
6. **租约和 fencing**：摄取任务带 owner、租约和递增 epoch，被接管后旧 worker 无法写入。
7. **引用可追溯**：每条引用绑定 document/version/chunk；撤回或回滚后，旧答案和 SSE 回放都要重新核验。
8. **AI 只给建议**：最终消息由 Go 发布，不直接推送模型的原始输出。
9. **失败关闭**：证据不足、资料冲突或服务失败时转人工，不让模型硬答。
10. **预算账本**：所有付费模型调用共用一个 SQLite 账本，调用前先预留费用；可重试错误最多重试一次，共用原 deadline。
11. **冻结评测集**：60 条案例、37 份合成文档和答案点不为适配新功能而修改。

## 当前实现与目标的差距

| 方面 | 当前 | 目标 | 阶段 |
| --- | --- | --- | --- |
| Python 进程 | 3 个：`app.rpc.server`、`app.rpc.ingest_server`、`app.ingest.worker_main` | 1 个 `ai` 进程 | P1 |
| 前端 | `web/` 和 `admin-web/` 两个工程 | 单一 `frontend/` | P1 |
| 身份 | Keycloak 必选 | Go 内置本地账号，OIDC 可选 | P1 |
| 部署 | Compose 有 9 个服务（含 bootstrap、edge、redis） | 3 个默认服务 | P1 |
| 资料 | DOCX、Markdown、CSV、FAQ；无商品表结构，无时效 | 商品表结构化、生效和失效时间 | P2 |
| 工作台 | 人工队列加员工 AI 辅助 | 建议回复、一键复制、知识缺口 | P2 |

## 技术栈

| 范围 | 技术 | 版本来源 |
| --- | --- | --- |
| Go | HTTP/SSE、gRPC、pgx、go-oidc | `go/go.mod` |
| Python | LangChain、LangGraph、FastAPI（内部）、gRPC、psycopg | `python/pyproject.toml`、`python/uv.lock` |
| 前端 | Vue 3、TypeScript、Vite、vue-router | `package.json` 和锁文件 |
| 契约 | Protobuf、buf | `buf.yaml`、`buf.gen.yaml` |
| 存储 | PostgreSQL 16、pgvector、pg_trgm；Redis 可选 | `docker/db/` |
| 模型 | 阿里云百炼（qwen 生成、1024 维 Embedding、Rerank） | `python/app/providers/` |
| 工具链 | Go、Node、uv | `toolchains.lock.json` |
