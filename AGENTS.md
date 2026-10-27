# 工程规则

适用于 AI 助手和所有贡献者。

## 开始前必读

1. [docs/product.md](docs/product.md)：为谁做、做什么、不做什么。
2. [ROADMAP.md](ROADMAP.md)：当前阶段和任务。
3. [docs/architecture.md](docs/architecture.md)：目标架构和必须保留的设计约束。

需要时再读 [docs/development.md](docs/development.md) 和 [docs/decisions/](docs/decisions/)。私有归档不属于日常输入。

## 防跑偏

- 只做 ROADMAP 当前阶段内的任务。超出范围的想法，先向用户提出，或在 `docs/decisions/` 写一份状态为"提议"的决策记录。
- 动手前对照 [产品定位](docs/product.md#防跑偏判据) 的三个问题。
- 新功能、依赖或集成动手前先检索成熟方案（官方文档、开源实现、同类产品），列出候选、链接与不采用理由；不凭记忆直接造。
- 新 ADR 必须含「备选方案」一节：至少 2 个被评估项、链接、以及不采用的理据；只写一个方案视为未完成。
- 不引入新的服务、语言、框架或外部依赖，除非 ROADMAP 或决策记录要求。
- 不为"通过验收"而修改冻结评测集、真值或门槛。

## 架构红线

[架构文档](docs/architecture.md#必须保留的设计约束)中的 11 条约束不可违反。最常碰到的几条：

- 浏览器只访问 Go；权限只由 Go 决定，Python 只用 Go 签发的 scope。
- 一张表只有一个写入方；不用 SQL 直写终态绕过业务流程。
- 结构变化只能新增 checksum 迁移；应用不执行 DDL。
- Go 和 Python 的契约只在 `proto/rag/v1/rag.proto`；改完运行 `bash scripts/dev/generate-proto.sh`，不手改生成文件。

## 检查

- 全量：`bash scripts/check.sh`。
- Go：在 `go/` 下运行 `gofmt`、`go test ./...`、`go vet ./...`。
- Python：在 `python/` 下运行 `uv sync --frozen`、`uv run pytest`、`uv run ruff check app`。
- 前端：在 `frontend/` 下运行 `npm test`、`npm run build`。
- 文档：`python3 scripts/check/check-doc-baseline.py`、`python3 scripts/check/check-docs.py`。
- 浏览器：`cd frontend && node tests/v2-lifecycle.browser.mjs`，前置条件（离线档、Chromium）见[开发指南](docs/development.md#浏览器层用例)。

先跑与改动直接相关的检查；跨服务、数据库或用户流程的改动要扩大范围。汇报时写明哪些没有运行。离线测试通过不代表真实模型或真实商家场景通过。

## Git

- 修改前看 `git status`，保护已有的未提交工作；不用 `reset --hard`、`clean`、裸 `stash`。
- 一个功能点一次提交，只暂存具体文件，不用 `git add .` 或 `-A`。
- 提交前运行 `python3 scripts/check/privacy_guard.py`；不用 `--no-verify`。
- 提交身份 `EcommerceRAG Maintainers <maintainers@example.invalid>`；hooks 路径 `.githooks`，缺失时运行 `bash scripts/dev/install-git-hooks.sh`。
- 唯一远端是 `origin`，即 GitHub `ggb5023/EcommerceRAG2`；主分支 `main`。push、merge、改写远端历史都需要明确授权。
- 守卫脚本拦截时只能通过新增提交修正，不能用工作区临时改动让钩子通过；需要的配置改动先 cherry-pick 到目标分支再推。

## 安全

- 凭据、DSN、私钥、客户正文和完整聊天记录不进入 Git、日志或文档。秘密只从仓库外的受限配置读取。
- 模型与第三方 token 只写在 `/etc/ecommerce-rag/providers.env`（0600）；`.env` 与运行时配置只放连接串和路径，不放凭据，两个文件的职责见[开发指南](docs/development.md#运行配置)。
- 真实模型可以按需调用，但必须走共享预算账本；额度用完就停下，向用户申请（见[决策 0006](docs/decisions/0006-provider-usage.md)）。
- 生产数据变更、常驻服务重启、发布推送，都必须属于当前任务并获得授权。

## 文档维护

- 架构或约束变化：更新 `docs/architecture.md`。
- 重大取舍：新增 `docs/decisions/NNNN-*.md`，只追加；被取代时在旧记录上标注。新建前先 `git pull --ff-only` 并确认下一个编号；同号已存在时追加补充，不另建文件。
- 阶段进展：勾选并更新 `ROADMAP.md`。
- 商家可见的行为变化：更新对应的商家手册。
- 不写过程流水、报告路径、测试数字；一条规则只写在一个地方。

## 汇报格式

```text
完成内容：
改动文件：
验证结果：
未运行项和限制：
下一步：
```
