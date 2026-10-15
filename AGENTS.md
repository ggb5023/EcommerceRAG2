# 工程总规则

## 文档职责

- 本文件是适用于整个仓库、受 Git 管理的工程规则入口，规定必须遵守的边界和检查入口；稳定事实见 `FACTS.md`，公开架构摘要见 `docs/architecture.md`。
- 每个新窗口先读 `HANDOFF.md` 了解当前进度；需要稳定背景时再读 `FACTS.md`。日常开发不读 archive。
- 每个新窗口在 `.local/dev-docs/archive/` 新建一份简短对话归档，记录决定、重要发现、操作和验证，不复制完整聊天。
- 归档文件命名为 `YYYYMMDD-主题.md`；窗口结束时只更新一次 `HANDOFF.md` 与确有变化的 `FACTS.md`，过程细节留在该归档。
- 窗口交接时集中核对并更新 `HANDOFF.md` 与 `FACTS.md`。HANDOFF 只写当前进度、下一步任务、遗留问题；FACTS 只保留稳定结论，旧结论直接覆盖，不堆叠历史。
- 根目录 `AGENTS.md`、`FACTS.md`、`HANDOFF.md`、`README.md` 和 `docs/architecture.md` 可提交，内容必须脱敏。HANDOFF 可记录当前进度，但不保存敏感正文。
- 私有专题和运行证据保留在 `.local/dev-docs/`，过程历史放在 `.local/dev-docs/archive/`；整个 `.local/` 不得进入 Git。不得为了公开文档检查上传私有专题。
- 归档是私有资料，仅在回溯历史问题时读取。既有快照保留为历史记录。

## 关键文件索引

- `README.md`：仓库用途和启动入口。
- `HANDOFF.md`：当前进度、下一步任务和遗留问题。
- `FACTS.md`：稳定架构结论和工程边界。
- `docs/architecture.md`：公开、稳定的架构摘要，不包含运行历史或环境秘密。
- `.local/dev-docs/docs/README.md`：私有详细参考文档索引。
- `.local/dev-docs/docs/项目概览与架构.md`：架构职责和设计依据。
- `.local/dev-docs/docs/重构工程实施方案.md`：重构阶段和验收边界。
- `.local/dev-docs/docs/验证与验收.md`：当前验收门槛和历史归档入口；不替代当前代码和运行核对。
- `proto/rag/v1/rag.proto`：Go/Python RPC 契约。
- `sql/migrations/`：数据库结构变更和迁移校验。
- `go/`、`python/`、`web/`、`admin-web/`：服务和前端代码。
- `scripts/check.sh`：工程检查入口；检查结果只覆盖脚本实际运行的项目。

## 文档维护

- 活动文档只保留设计、契约、运行边界和当前验收门槛；窗口状态只在 `HANDOFF.md`，长期结论只在 `FACTS.md`。
- 架构职责变化同步公开架构摘要及相关私有设计；接口、数据、Provider、页面和运维变化同步所属专题。每个任务明确列出文档更新，或说明无影响及理由。
- 不新增重复的活动状态矩阵，不改写历史归档和 snapshots。公开文档不复制私有历史、预算账本、客户内容或运行报告。

## 稳定架构不变量

- 浏览器和外部业务流量只能访问 Go；浏览器认证允许跳转到配置白名单中的 Keycloak 登录页，业务 API/SSE/资料操作仍只经过 Go。FastAPI 仅提供 Python 内部健康、就绪和受保护的开发接口。
- Go 独占身份、授权决策、租户/店铺范围、业务执行终态、对外 SSE 和审计职责；Python 摄取生命周期事件与 Go 业务审计分开保存。
- Python 只能使用 Go 签发的 scope，必须回验并在检索、上下文、生成和引用处过滤；不得扩大权限、自行签发 scope 或发布 Go 业务终态。
- Python 单写 V2 摄取 `job/item/version/chunk` 及其发布状态；Go 负责提交、控制命令、审批和审计，不能直接更新摄取表。
- 一张业务表只有一个写入方；表级所有权由迁移和受限服务角色落实。禁止通过 SQL 直写最终结果绕过真实业务流程。
- Proto/gRPC 是 Go 与 Python 的唯一跨语言契约。字段、状态、错误、取消或 deadline 变化先修改 `proto/rag/v1/rag.proto`，再用 `bash scripts/generate-proto.sh` 同步生成代码；不新增平行的业务 HTTP/JSON 契约。自部署授权回验使用 mTLS Proto/gRPC；开发 loopback HTTP 兼容边界见 `docs/architecture.md`。
- 已应用迁移不可修改；数据库结构变化必须新增 checksum 迁移，并在独立库、非超级用户且无 `BYPASSRLS` 条件下验证。应用进程不执行 DDL。

## 数据来源与准入

- `eval/public-data-sources.manifest.json` 是开源数据、开源服务目标和来源缺口的版本化登记入口。公开 URL、候选条目或“开放数据”标签本身不构成下载或业务使用授权。
- 下载/准入数据前，逐来源核对原始条款、许可证范围、revision、语言/司法辖区、字段与隐私处理、用途边界和仓外受限存储；待核验来源只能保持候选且未下载。
- 原始公开数据不得进入 Git。由公开数据生成的本地库存/订单仅是测试记录，移除客户标识，并保留来源 revision、输入 SHA-256 和转换版本。
- 公开数据仅用于清单批准的用途；社区问答不能作为商家政策或客服答案真值。Keycloak/Medusa 本地开源服务目标不等于真实身份或商家生产服务，相关结果保持 `real_service_acceptance=false`。
- 冻结的 60 条案例、37 份合成文档、答案点和 evidence sidecar 与新增公开数据轨分离；不得为适配公开来源而修改既有真值或评测输入。
- 语义复核必须绑定实际答案 capture、冻结真值和实际交付证据。用户明确委托代理复核时记录 `reviewer_kind=agent`、委托依据与逐例结论，保持 `human_review_completed=false`；不得冒充真人审核，也不得因要求全部通过而覆盖失败、补造支持或放宽门槛。组件批准不能覆盖新的 HTTP 答案。

## 技术栈与检查入口

- Go 控制面采用 HTTP/SSE、gRPC/Protobuf；Python AI 面采用 LangChain/LangGraph、内部 FastAPI；客服和管理前端采用 Vue、TypeScript、Vite；持久化采用 PostgreSQL/pgvector、pg_trgm 和 Redis。
- 具体版本以 `go/go.mod`、`go/go.sum`、`python/pyproject.toml`、`python/uv.lock`、`web/package.json`、`admin-web/package.json` 及各自锁文件为准；Proto、工具链和基础设施配置分别见 `buf.yaml`、`buf.gen.yaml`、`toolchains.lock.json`、`docker/db/`。文档不另行冻结版本。
- Go：使用 `gofmt` 格式化改动，在 `go/` 运行 `go test ./...`、`go vet ./...`。
- Python：在 `python/` 使用 `uv sync --frozen`、`uv run pytest`、`uv run ruff check app`，按任务范围补充脚本及评测检查。
- Proto：运行 `buf lint`、`buf build`、`buf breaking`；兼容基线见 `scripts/check-proto.sh`，生成入口为 `scripts/generate-proto.sh`。
- 前端：分别在 `web/`、`admin-web/` 运行 `npm test`、`npm run build`，依赖安装使用对应锁文件。
- 总入口为 `bash scripts/check.sh`；文档入口为 `python3 scripts/check-doc-baseline.py`、`python3 scripts/check-docs.py`。工程检查不替代数据库、跨进程、浏览器或真实服务验收。

## 开发注意事项

- 活动代码工作树为 `/opt/ecommerce-rag-v2`；旧 `/opt/ecommerce-rag` 只读保留；`/opt/ecommerce-rag-ops` 只承担数据库、Redis、迁移、备份和隔离测试，不在其中编译或修改业务应用。
- 修改前核对工作目录、分支、HEAD、工作树、暂存区、远端、upstream 和 worktree，并审阅 `git status --short --branch`、`git diff`、`git diff --cached`。不能只凭 HEAD 推断代码状态。
- 辨认并保护已有未提交工作；禁止 `reset --hard`、`clean`、`restore`、`stash`、覆盖他人改动或擅自删除文件。
- 先运行与改动直接相关的检查；跨服务、数据库或用户流程变更时扩大验证范围，并明确列出未运行项。
- 区分 fixture、静态检查、隔离运行、真实 Provider 和生产证据。低层通过不能证明高层流程完成。
- Git、归档、日志和报告中不得放凭据、DSN、私钥、客户正文或完整聊天记录。秘密只从受限运行环境读取。
- 生产数据变更、常驻服务重启、付费外部调用、发布、推送和公网入口切换必须明确属于当前任务并获授权。

## Git 提交规范

- 一个功能点完成且相关检查通过后，执行一次 `git commit`；提交保持小而可独立审阅。
- 当前托管约定：`origin` 为 Gitee 主仓，`github` 为 GitHub 同步镜像；任何远端操作前以 `git remote -v` 为准，不能把 ahead 数量当作本次推送范围。
- 本地使用 `EcommerceRAG Maintainers <maintainers@example.invalid>` 身份。核对 `core.hooksPath` 为 `.githooks`；缺失时执行 `bash scripts/install-git-hooks.sh`。
- 只按具体文件或 hunk 暂存，禁止 `git add .`、`git add -A`。同一文件存在他人改动时只处理自己的边界；无法安全分离则保留未提交，不替他人提交。
- 提交前执行并审阅 `git diff --cached --stat`、`git diff --cached`、`git diff --cached --check`、`python3 scripts/privacy_guard.py --identity`；不得使用 `--no-verify` 或关闭隐私检查。
- 文档结构调整单独提交。本地提交不代表推送授权；未经明确授权不得 push、merge、force push 或改写远端历史。

## 用户回复模板

```text
完成内容：
改动文件：
验证结果：
未运行项和限制：
提交记录：
下一步：
```

## 商家知识客服规则

- 产品面向商家自部署；默认单商家单店铺，保留隔离。Medusa、公开商品双轨与实时订单/库存工具不属于首版默认依赖。
- 访客身份与员工分离；客户接口只发获准的最终回答与来源片段，不发原始模型增量或内部工具内容。ANSWER、CLARIFY、HANDOFF 是 Python 建议，发布权属于 Go。
- 客户会话控制与 RAG 执行状态分开；客服领取必须原子化，AI 与人工发送都检查 state/epoch/有效领取。人工回复不自动进入知识库。来源撤回及权限收紧也过滤旧结果和 SSE 回放。
- 除公开架构外，仅放行指定安装、知识、客服、备份和开发文档；其余 docs 和整个 .local 仍私有。安装包不包含开发密钥、账号、账本、资料或输出。
