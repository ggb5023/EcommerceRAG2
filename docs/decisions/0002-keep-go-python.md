# 0002 保留 Go + Python，收敛进程和前端

日期：2026-10-15　状态：已采纳

## 背景

当前有 4 个后端进程（Go gateway，Python 的 AI RPC、摄取 RPC、worker）、2 个前端工程，Keycloak 必选，Compose 有 9 个服务。对单商家自部署来说太重。备选方案是合并成单一 Python 后端。

## 决定

- 保留 Go 控制面：唯一对外入口，负责身份、授权、会话、人工队列、SSE、审计。
- 保留 Python AI 面，但三个进程合成一个 `ai` 进程，摄取 worker 作为进程内循环运行。
- Go 与 Python 之间继续只用 `proto/rag/v1/rag.proto` 通信。
- `web/` 和 `admin-web/` 合并为单一 `frontend/`。
- Go 内置本地账号作为默认身份，Keycloak/OIDC 改为可选 profile。
- 默认部署收敛为 `gateway`、`ai`、`postgres` 三个服务。

## 理由

- Go 部分（授权、人工队列、SSE、审计）已有完整测试和验收，重写的风险大于收益。
- 保留 Go，后续支持单二进制分发、更多并发连接和多店铺时更容易。
- 部署复杂度的主要来源是进程数、前端数和 Keycloak，而不是语言数量。

## 后果

- Proto 生成和 `buf breaking` 检查的维护成本保留。
- P1 需要完成进程合并、前端合并和本地账号，详见 [路线图](../../ROADMAP.md)。
