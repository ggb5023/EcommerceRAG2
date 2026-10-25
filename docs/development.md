# 开发指南

[项目入口](../README.md) · [架构](architecture.md) · [路线图](../ROADMAP.md) · [工程规则](../AGENTS.md)

## 仓库结构

| 目录 | 内容 |
| --- | --- |
| `go/` | Go gateway（`cmd/gateway`）、Web 静态服务（`cmd/web`）、开发用 facts fake |
| `python/` | AI 与摄取（`app/graph`、`app/retrieval`、`app/ingest`、`app/providers`、`app/rpc`） |
| `frontend/` | 顾客页、客服工作台、商家管理台（单一 Vue 工程，按路由分区） |
| `proto/` | Go 与 Python 的 gRPC 契约 |
| `sql/migrations/` | checksum 迁移 |
| `eval/` | 评测集与评测工具 |
| `data/synthetic/` | 合成店铺资料（冻结评测输入） |
| `docker/` | 数据库、身份、自部署 Compose |
| `scripts/` | 检查、开发、部署、DB 服务器脚本，分目录说明见 [脚本索引](../scripts/README.md) |

## 环境

工具链版本见 `toolchains.lock.json`：Go、Node、uv（Python 3.12）。数据库需要 PostgreSQL 16，带 `vector` 和 `pg_trgm` 扩展。

```bash
bash scripts/dev/install-git-hooks.sh
(cd python && uv sync --frozen)
(cd frontend && npm ci --include=dev)
```

`scripts/dev/bootstrap-dev.sh` 可在任意 Ubuntu 24.04 机器上执行：版本取自 `toolchains.lock.json`，不依赖固定仓库路径或固定内网地址；下载优先用官方校验和验证，GitHub 不可达时 `buf`、`grpcurl` 退回 `go install` 构建，`uv` 退回官方安装脚本。`codex` 只在存在时记录版本，不作为前置依赖。装完先跑 `bash scripts/check.sh` 确认工具链完整（缺 `shellcheck`、`protoc` 会直接失败）。

## 运行配置

配置拆成两个文件，职责不同，不要互相复制。

**运行时配置**（仓库外，权限 `0600`）：键名必须落在 `scripts/dev/v2_services.py` 的 `CONFIG_KEYS` 白名单内，否则进程启动即失败（`runtime config contains an invalid or duplicate key`）。模板见 [`.env.example`](../.env.example)。必填项：

- 四个 DSN：`V2_GATEWAY_DSN`、`V2_RETRIEVER_DSN`、`V2_CHECKPOINT_DSN`、`V2_INGEST_DSN`；`ai` 是单进程，同时持有后三个并各开一个角色绑定连接池，`gateway` 仍然拿不到任何 Python 角色
- 开发身份：`V2_IDENTITY_TENANT`、`V2_IDENTITY_USER`（填 `external_id`，不是自增主键）
- `SCOPE_SIGNING_KEY_FILE`：Go 签发 scope 用的密钥文件，`0600`、内容 ≥ 32 字节
- 在线档另需 `V2_PROVIDER_CONFIG` 与预算三项 `V2_ONLINE_BUDGET_LEDGER` / `V2_ONLINE_BUDGET_ID` / `V2_ONLINE_BUDGET_CNY`

`V2_RPC_TLS` 留空表示进程间明文；只有显式写了取值才会走 mTLS，并需要同时给出 CA、证书和密钥三个路径。

**Provider 配置**（`/etc/ecommerce-rag/providers.env`，权限 `0600`，属主是运行用户）：百炼、MinerU、Tavily 的凭据都在这一个文件里，模板见 [`providers.env.example`](../providers.env.example)。`python/app/providers/config.py` 明确不合并环境变量，写进运行时配置或 `.env` 不会生效。

两个文件都不进 Git；凭据、DSN、密钥只在仓库外出现。

## 本地运行

1. 由数据库管理员预先创建独立数据库、服务角色和扩展；应用不执行 DDL。
2. 迁移：`python/.venv/bin/python scripts/ops/v2-migrate.py --env /path/to/migration.env`。迁移用户必须是非超级用户、无 `BYPASSRLS`。
3. 启动后端：`bash scripts/dev/v2-local-services.sh --env /path/to/runtime.env`。配置文件权限为 `0600`，放在仓库外。默认模型 profile 是离线的 `v2_langchain_mock`，不产生费用。
4. 启动前端：在 `frontend/` 运行 `npm run dev`，用 `VITE_API_PROXY_TARGET` 指向 gateway 的 HTTP 端口；`/v1` 与 `/admin/v1` 走同一个代理目标。

`frontend/` 是唯一入口，`/chat`、`/support`、`/shop/:slug`、`/admin/*` 都在同一个 origin 下；浏览器仍然只访问 Go，前端不做授权判断。开发机常驻启动用一条命令：

```bash
cd frontend && VITE_API_PROXY_TARGET=http://127.0.0.1:8080 npm run dev -- --port 5173
```

systemd 单元属于机器特定状态，不进仓库（部署契约在 M5 定义）；仓库只约定启动命令与端口 5173。生产镜像由 `docker/selfhost/web.Dockerfile` 构建一套产物，Go 在 `/srv/frontend` 做单页兜底。

使用真实模型时，改用 `v2_langchain_bailian` profile，并配置共享预算账本：

```bash
python/.venv/bin/python scripts/dev/provider-budget.py initialize \
  --ledger /path/to/private/budget.sqlite --window-id <window> --cap-cny <cap>
```

所有付费调用方（ai、worker、评测）使用同一个 `V2_ONLINE_BUDGET_LEDGER`。不要为绕过额度而新建账本。

## 测试分层

| 层 | 命令 | 说明 |
| --- | --- | --- |
| 基础 | `bash scripts/check.sh` | 单元测试、静态检查、Proto、前端构建；默认跳过需要数据库的用例 |
| 数据库集成 | `V2_INTEGRATION_ENV=... uv run pytest tests/test_v2_ingest_database.py tests/test_postgres_retriever.py` | 使用独立的 `ecr_v2_test_*` 库 |
| 跨进程 | `ECR_V2_GO_PYTHON=1 go test ./internal/gateway -run TestGoCallsDedicatedPythonV2GRPC` | Go 调用真实 Python 进程 |
| 浏览器 | `cd frontend && node tests/v2-lifecycle.browser.mjs` | 上传、审核、发布、问答、回滚、撤回 |
| 评测 | `bash eval/run.sh offline`（已含在 check.sh 中）；真实模型见 `bash eval/run.sh live` | 离线回归免费；真实模型评测走预算账本，详见 `eval/README.md` |

上层测试不能用下层结果代替。汇报时写明运行了哪几层。

### 浏览器层用例

浏览器用例直接驱动真实 UI，跑之前需要满足两个前置条件，否则会以断言失败告终：

1. 后端必须是**离线档**：用例里有 `profile === 'v2_langchain_mock'` 的断言，在线档会拒绝执行浏览器写操作。
2. 本机要有 Playwright 的 Chromium：`cd frontend && npx playwright install chromium`（另需 `npx playwright install-deps chromium` 需要的系统库）。

冻结评测语料在仓外；缺失时相关用例与 `scripts/check/check-eval-corpus.py` 默认 SKIP，`ECR_REQUIRE_EVAL=1` 时按失败处理，metadata 的 sha256 与实际 cases 文件不符一律失败。语料路径可用 `ECR_EVAL_REVISION` 覆盖。

用例会向 `WORKSPACE_URL`（默认 `http://127.0.0.1:5173`，合并后是唯一入口，`/admin/*` 也在这个 origin 下）发起真实请求，只能指向 loopback。用例产生的资料版本会留在库里，不会自动回滚，测试库请单独准备。

本地账号登录用例 `frontend/tests/login.browser.mjs` 需要 `V2_IDENTITY_PROFILE=local` 的栈，并先用 CLI `reset-password` 把目标账号置为首次改密状态；通过 `LOGIN_USER`、`LOGIN_PASSWORD`、`LOGIN_NEW_PASSWORD` 传入账号与密码。

## 改契约和数据库

- **Proto**：改 `proto/rag/v1/rag.proto` → `bash scripts/dev/generate-proto.sh` → `bash scripts/check/check-proto.sh`（buf lint、build、breaking）。不手改生成文件。
- **测试数据**：0002、0017、0019、0021 中的开发账号（`dev-user`、`shop-demo` 等）只在库名为 `ecr_v2_test_*`、`ecr_v2_dev_*` 或含 `m1_test` 的库中保持可用，其他库由迁移 0029 停用。测试改坏了这些账号，用 `psql "$V2_MIGRATION_DSN" -f scripts/dev/seed-fixtures.sql` 恢复。新的测试数据不要写进迁移。
- **迁移**：在 `sql/migrations/` 新增 `NNNN_描述.sql`，不修改已有迁移。用 `bash scripts/dev/test-migrations.sh` 在独立库上验证。新表要明确写入方和角色授权。合并进 `main` 之前允许修改未合并的迁移，但必须重建跑过旧版本的测试库；合并之后只允许新增迁移。
- **HTTP API**：顾客 `/v1/customer/`、客服 `/v1/support/`、管理 `/admin/v1/`。`frontend/` 仍在用的 `/v1/conversations`、`/v1/turns` 属于旧会话接口，与本轮合并无关，退休另行安排，新代码只写 `/v1/customer/`。写请求校验 Origin、CSRF 和 Idempotency-Key。

## 提交

见 [AGENTS.md](../AGENTS.md#git)。一个功能点一次提交，提交信息使用 `feat:`、`fix:`、`docs:`、`test:`、`refactor:` 前缀。
