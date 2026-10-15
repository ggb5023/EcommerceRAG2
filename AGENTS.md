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
- 不引入新的服务、语言、框架或外部依赖，除非 ROADMAP 或决策记录要求。
- 不为"通过验收"而修改冻结评测集、真值或门槛。

## 架构红线

[架构文档](docs/architecture.md#必须保留的设计约束)中的 11 条约束不可违反。最常碰到的几条：

- 浏览器只访问 Go；权限只由 Go 决定，Python 只用 Go 签发的 scope。
- 一张表只有一个写入方；不用 SQL 直写终态绕过业务流程。
- 结构变化只能新增 checksum 迁移；应用不执行 DDL。
- Go 和 Python 的契约只在 `proto/rag/v1/rag.proto`；改完运行 `bash scripts/generate-proto.sh`，不手改生成文件。

## 检查

- 全量：`bash scripts/check.sh`。
- Go：在 `go/` 下运行 `gofmt`、`go test ./...`、`go vet ./...`。
- Python：在 `python/` 下运行 `uv sync --frozen`、`uv run pytest`、`uv run ruff check app`。
- 前端：在 `web/`、`admin-web/` 下运行 `npm test`、`npm run build`。
- 文档：`python3 scripts/check-doc-baseline.py`、`python3 scripts/check-docs.py`。

先跑与改动直接相关的检查；跨服务、数据库或用户流程的改动要扩大范围。汇报时写明哪些没有运行。离线测试通过不代表真实模型或真实商家场景通过。

## Git

- 修改前看 `git status`，保护已有的未提交工作；不用 `reset --hard`、`clean`、裸 `stash`。
- 一个功能点一次提交，只暂存具体文件，不用 `git add .` 或 `-A`。
- 提交前运行 `python3 scripts/privacy_guard.py`；不用 `--no-verify`。
- 提交身份 `EcommerceRAG Maintainers <maintainers@example.invalid>`；hooks 路径 `.githooks`，缺失时运行 `bash scripts/install-git-hooks.sh`。
- `origin` 是 Gitee 主仓，`github` 是 GitHub 镜像。push、merge、改写远端历史都需要明确授权。

## 安全

- 凭据、DSN、私钥、客户正文和完整聊天记录不进入 Git、日志或文档。秘密只从仓库外的受限配置读取。
- 付费模型调用、生产数据变更、常驻服务重启、发布推送，都必须属于当前任务并获得授权。所有付费调用共用预算账本。

## 文档维护

- 架构或约束变化：更新 `docs/architecture.md`。
- 重大取舍：新增 `docs/decisions/NNNN-*.md`，只追加；被取代时在旧记录上标注。
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
