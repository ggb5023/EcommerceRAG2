# 开发指南

[项目入口](../README.md) · [架构](architecture.md) · [路线图](../ROADMAP.md) · [工程规则](../AGENTS.md)

## 仓库结构

| 目录 | 内容 |
| --- | --- |
| `go/` | Go gateway（`cmd/gateway`）、Web 静态服务（`cmd/web`）、开发用 facts fake |
| `python/` | AI 与摄取（`app/graph`、`app/retrieval`、`app/ingest`、`app/providers`、`app/rpc`） |
| `web/`、`admin-web/` | 顾客与客服前端、商家管理台（P1 合并为 `frontend/`） |
| `proto/` | Go 与 Python 的 gRPC 契约 |
| `sql/migrations/` | checksum 迁移 |
| `eval/` | 评测集与评测工具 |
| `data/synthetic/` | 合成店铺资料（冻结评测输入） |
| `docker/` | 数据库、身份、自部署 Compose |
| `scripts/` | 开发、检查、部署与运维脚本 |

## 环境

工具链版本见 `toolchains.lock.json`：Go、Node、uv（Python 3.12）。数据库需要 PostgreSQL 16，带 `vector` 和 `pg_trgm` 扩展。

```bash
bash scripts/install-git-hooks.sh
(cd python && uv sync --frozen)
(cd web && npm ci) && (cd admin-web && npm ci)
```

## 本地运行

1. 由数据库管理员预先创建独立数据库、服务角色和扩展；应用不执行 DDL。
2. 迁移：`python/.venv/bin/python scripts/v2-migrate.py --env /path/to/migration.env`。迁移用户必须是非超级用户、无 `BYPASSRLS`。
3. 启动后端：`bash scripts/v2-local-services.sh --env /path/to/runtime.env`。配置文件权限为 `0600`，放在仓库外。默认模型 profile 是离线的 `v2_langchain_mock`，不产生费用。
4. 启动前端：`web/` 和 `admin-web/` 分别运行 `npm run dev`，用 `VITE_API_PROXY_TARGET`、`VITE_ADMIN_API_PROXY_TARGET` 指向 gateway 的 HTTP 端口。

使用真实模型时，改用 `v2_langchain_bailian` profile，并配置共享预算账本：

```bash
python/.venv/bin/python scripts/provider-budget.py initialize \
  --ledger /path/to/private/budget.sqlite --window-id <window> --cap-cny <cap>
```

所有付费调用方（ai、worker、评测）使用同一个 `V2_ONLINE_BUDGET_LEDGER`。不要为绕过额度而新建账本。

## 测试分层

| 层 | 命令 | 说明 |
| --- | --- | --- |
| 基础 | `bash scripts/check.sh` | 单元测试、静态检查、Proto、前端构建；默认跳过需要数据库的用例 |
| 数据库集成 | `V2_INTEGRATION_ENV=... uv run pytest tests/test_v2_ingest_database.py tests/test_postgres_retriever.py` | 使用独立的 `ecr_v2_test_*` 库 |
| 跨进程 | `ECR_V2_GO_PYTHON=1 go test ./internal/gateway -run TestGoCallsDedicatedPythonV2GRPC` | Go 调用真实 Python 进程 |
| 浏览器 | `cd web && node tests/v2-lifecycle.browser.mjs` | 上传、审核、发布、问答、回滚、撤回 |
| 评测 | 见 `eval/README.md` | 离线回归免费；真实模型评测走预算账本 |

上层测试不能用下层结果代替。汇报时写明运行了哪几层。

## 改契约和数据库

- **Proto**：改 `proto/rag/v1/rag.proto` → `bash scripts/generate-proto.sh` → `bash scripts/check-proto.sh`（buf lint、build、breaking）。不手改生成文件。
- **迁移**：在 `sql/migrations/` 新增 `NNNN_描述.sql`，不修改已有迁移。用 `bash scripts/test-migrations.sh` 在独立库上验证。新表要明确写入方和角色授权。
- **HTTP API**：顾客 `/v1/customer/`、客服 `/v1/support/`、管理 `/admin/v1/`。写请求校验 Origin、CSRF 和 Idempotency-Key。

## 提交

见 [AGENTS.md](../AGENTS.md#git)。一个功能点一次提交，提交信息使用 `feat:`、`fix:`、`docs:`、`test:`、`refactor:` 前缀。
