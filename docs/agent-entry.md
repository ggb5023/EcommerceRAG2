# Agent 入口（唯一必读）

> 权威等级：context　适用提交：4a625b1　最后核对：2026-10-30
> 读完本文件应能回答四件事：改动该放哪、什么会被门禁拦、怎么验证、怎么汇报。
> 预算 ≤200 行。超了就把新内容拆到 `docs/constraints.md`，不要撑大本文件。

## 1. 仓库地图

| 路径 | 放什么 | 不该出现 |
| --- | --- | --- |
| `go/internal/gateway` | 编排 + HTTP 适配（`turn_*.go` / `routes_*` / `sse` / `edge`） | SQL、业务分支堆叠 |
| `go/internal/domain` | 纯函数判定（证据过滤、澄清、引用校验、资源定位） | I/O、依赖 gateway/admin |
| `go/internal/admin` | 管理域 handler + `store_*.go`（81 条 SQL） | handler 里的 SQL |
| `go/internal/auth` `localauth` | 授权签名与 scope 校验 / 账号会话 | — |
| `go/internal/customer` | 顾客侧服务 | — |
| `go/internal/ports` | `IngestClient` / `FactClient` / `Authority` 接口 | 实现 |
| `python/app/graph` | LangGraph 编排（intent 分类、检索多跳） | 终态决定、发 SSE、写库 |
| `python/app/ingest` | 解析→切块→嵌入→发布（**唯一写入方**） | 权限判定 |
| `python/app/retrieval` | 稠密 + 稀疏检索、RRF 融合 | — |
| `python/app/providers` | 模型适配（Bailian / MinerU / Tavily） | — |
| `frontend/src` | 管理端 + 顾客端 SPA | — |
| `eval/` | 冻结评测（53 例对齐集） | 产品代码 |
| `sql/migrations/` | 37 条迁移，**只追加** | 修改历史文件 |
| `scripts/check/` | 守卫 | — |

## 2. 分层与纪律

Go 五层：**适配**（`turn_handlers` / `routes_*` / `sse`）→ **用例**（`turn_*.go`，`runTurn` 117 行）→ **领域**（`domain/`，覆盖率 93.5%）→ **持久**（`admin/store_*.go`）→ **出入口**（`ports/`）。
Python 三层：**编排**（`graph/`）→ **能力**（`chains/` `providers/`）→ **摄取**（`ingest/`）。
两者之间是 gRPC 进程边界，字段契约由 `docs/contracts.md` 钉住。

三条硬规则（决策 0025）：
1. **每条规则只有一个出处**（同一事实不得在两处陈述）；
2. **每个函数只在一个抽象层工作**（阶段函数 ≤80 行，不接 `*http.Request`）；
3. **阶段之间只通过显式数据通信**（不靠共享状态传值）。

边界（决策 0028）：**Go 拥有**授权、幂等、状态机、事件顺序、落库、失败分类、终态（ASKING/DONE/FAILED）；**Python 拥有** intent 分类、clarification 文案候选、confidence、检索多跳、提示组装。判据：**能不能看/能不能发 → Go；还要不要再检索/再问一句 → Python**。

## 3. 门禁（`scripts/check.sh`，35 项）

| 组 | 项 | 拦什么 |
| --- | --- | --- |
| 边界 | `stage-boundaries` `admin-boundaries` `domain-imports` `ownership` | SQL 回流 handler、阶段函数接 request、domain 依赖基础设施、越权写表 |
| 契约 | `contracts` | 跨进程契约缺一侧测试（棘轮只许增：cross 5/6、same 2/2） |
| 文档 | `docs-links` `docs-drift` `doc-baseline` `openapi` | 断链、同一事实多处陈述、基线被改 |
| 隐私 | `privacy-guard` `privacy-tests` `publication-tests` | 密钥、非公开文档入库 |
| 可观测 | `service-logs` | 服务起了但完全静默 |
| 正确性 | `go-test` `python-tests` `frontend-tests` `eval-offline` `facts-fake-matrix` `provider-smoke` `artifact-*-smoke` | 行为回归 |
| 协议 | `buf-lint` `buf-build` `buf-breaking` | proto 破坏性变更 |
| 构建 | `shellcheck` `shell-syntax` `go-vet` `python-lint` `python-imports` `npm-ci` `frontend-build` | 编译与静态检查 |

红了先看该行打印的 `FAIL <id>` 与前一行原因；**不要改门禁去迁就代码**，要改代码或按 T1 条款记基线 amendment。

## 4. 验证命令

```bash
bash scripts/check.sh                 # 快速层 35 项（默认）
bash scripts/check.sh --db            # 加持久化层（需集成库；无栈则 UNCONFIGURED）
bash scripts/check.sh --e2e           # 加浏览器层（需隔离栈）
bash scripts/check.sh --all           # 全层，严格模式

# 四条 smoke（改动轮次链路后必跑，与 .local/w2/ 基线逐字节比对）
bash scripts/dev/smoke-turn.sh
bash scripts/dev/smoke-answer.sh
bash scripts/dev/smoke-clarify.sh
bash scripts/dev/smoke-facts.sh

# 隔离栈（--db / --e2e 的前置）
bash scripts/dev/isolated-stack.sh --identity development --ports 26081 --with-vite
bash scripts/dev/isolated-stack.sh --identity local       --ports 26091 --with-vite
bash scripts/dev/isolated-stack.sh stop --all

# 定向
cd go && go test ./internal/domain/ -cover
cd python && uv run --frozen pytest -q
```

环境文件在 `/etc/ecommerce-rag/`：`dev.env`、`test-integration.env`、`eval-migration.env`、`eval-runtime.env`。
缺前置时门禁输出 `UNCONFIGURED`（不是 FAILED），**不要把它当通过**，要在汇报里说明。

## 5. 汇报格式（固定五节）

1. **完成内容** —— 做了什么，逐条对应工作包
2. **改动文件** —— 文件清单 + 每个文件改了什么
3. **验证结果** —— 真实命令与真实输出（含失败与 SKIP）
4. **未运行项和限制 / 未覆盖路径** —— 本轮没验证的部分，必须点名
5. **下一步** —— 下一轮做什么，等什么授权

数字必须附可复现命令；"实测"两个字不算证据。

## 6. 红线

- 不新增 `SECURITY DEFINER` 函数；
- 不修改历史迁移（`0001`–`0037`），只新增；
- SQL 只出现在 `store_*.go`；handler 里出现即视为缺陷；
- 终态只能在 Go 决定（0028）；Python 不得产出 `state`/`terminal` 字段；
- 任何界面与统计**不得出现货币金额**（0021，只统计 token 与次数）；
- 未经明确授权不 `push`、不 `merge`、不删分支；
- 不用 `git add .`；提交身份 `EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 7. 索引：我要改 X → 读哪份

| 我要改 | 先读 | 相关守卫 |
| --- | --- | --- |
| 轮次编排 / 阶段拆分 | `docs/decisions/0028-orchestration-boundary.md` | `stage-boundaries` |
| 持久化 / 加 SQL | `docs/data-ownership.md` | `admin-boundaries` `ownership` |
| 跨进程字段（gRPC/SSE/错误码） | `docs/contracts.md` `docs/error-taxonomy.md` | `contracts` |
| 检索 / 稀疏 / 嵌入 | `docs/decisions/0029-sparse-retrieval.md` | `eval-offline` |
| 代码分层与三条规则 | `docs/decisions/0025-code-level-rules.md` | 全部边界类 |
| 部署形态 / 前置代理 | `docs/decisions/0026-proxy-fronted-deployment.md` | `--e2e` |
| 导入与版本归属 | `docs/decisions/0027-document-source-ownership.md` | `contracts` |
| 本地起栈 / 端口 / 环境文件 | `docs/development.md` | `service-logs` |
| 安装与商家操作 | `docs/install.md` `docs/knowledge.md` | — |
| 已知债务 | `docs/refactor-input.md` | — |
| 外部资料 / 参考实现对照 | `docs/research/` | — |