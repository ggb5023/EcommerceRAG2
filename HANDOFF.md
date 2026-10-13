# 交接简报

## 当前进度

- 用户已确认仓外 53 案例人工复核包通过人工审核。见证文件与绑定 sidecar 已生成；最新质量门禁报告仍为 `NOT_PASSED`，唯一缺口是 Go HTTP/SSE E2E 证据。
- 新提交 `f235801` 增加人工复核见证 sidecar 工具和测试。工作树随后增加 37 文档生命周期 runner、E2E 严格校验和生命周期测试改动，尚未提交。

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`；本轮未推送或合并。质量闭环新增提交 `0a7ab64`（受限证据上下文、答案 capture、定向案例）和 `2e468bd`（分层质量门禁）。
- 固定 revision-1 输入、37 文档、批准 evidence sidecar 和 53/7 安全分区未修改。百炼固定 control、Embedding、Rerank、generation 槽位；用户已授权本阶段不限额度，但所有调用仍保留共享 SQLite 预算、限流和失败预留。
- 新 generation 契约已提交为 `115eba3`：有相关证据时不得返回空答案点，拒答/澄清/来源限制/冲突/时效规则必须显式表达，并保持日期、版本、数字、单位和限定词。定向 9 案例复测 41 次请求通过；完整新回归 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-full-generation-gate-20261013.json` 完成 53/53、236 次请求，使用独立质量窗口账本，`real_service_acceptance=false`。
- 新回归答案 capture 位于仓外 `/var/lib/ecommerce-rag/quality-review/full-generation-gate-20261013/`，新人工复核包位于 `/var/lib/ecommerce-rag/quality-review/human-review-full-generation-20261013/`；用户已确认包内文件人工审核通过，sidecar 已绑定基线、案例和 capture hash。
- 完整 context-neighbors 百炼报告 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-full-context-neighbors-20261012.json`：53/53、236 requests、PASS transport；同版本 source-neighbor 扩展后批准证据完整交付 53/53；Rerank Recall@5=1.0、MRR=0.981132、nDCG@5=0.986073。
- 答案原文只保存在仓外 `/var/lib/ecommerce-rag/quality-review/full-context-neighbors` 的 0600 文件；工程诊断 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-engineering-review-v2-20261012.json` 明确 `reviewer_kind=engineering`、`human_review_completed=false`。模型拒答/澄清/时效/限定词表现未达门槛，质量门禁 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-gate-v2-20261012.json` 为 `NOT_PASSED`（高风险失败 42、普通通过率 0）。
- 七条越权、撤权或过期案例安全报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-safety-20261012.json` PASS，Provider attempt delta=0。不得把安全前置报告升级为业务质量通过。
- 独立数据库真实 Go→gRPC→Python→百炼 三格式生命周期报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-live-lifecycle-20261012-final4.json` PASS，覆盖 3 个受控 fixture 的提交、解析、审核、发布、检索、重排、生成、引用、终态持久化。它不覆盖 37 文档/53 案例，不代表 M2 或生产就绪。
- `eval/run_v2_quality_e2e.py` 已要求真实生命周期、独立安全报告、53 eligible 案例、SSE 单调序列和终态持久化，并拒绝只有 3 个 fixture 的生命周期报告。`eval/run_v2_live_lifecycle.py` 已扩展为提交、审核和发布仓外批准的 37 文档 corpus，保留原始 hash、披露分类和动态 Embedding 预算。
- 质量评测代码验证：`python/.venv/bin/python -m pytest -q eval` 176 passed；changed eval scoped Ruff、privacy guard、git diff check 通过。随后本窗口的文档检查器调整后又运行了 `scripts/check.sh`，结果通过。
- 本窗口完成受 Git 管理的工程文档边界调整：完善 AGENTS/FACTS/README，新增 `docs/architecture.md`，公开检查只要求根入口文档，`.local/` 专题仍为私有。隐私扫描仅增加架构摘要的精确路径例外，内容扫描照常；`bash scripts/check.sh` 在文档和检查器更新后通过。该文档任务独立提交，不推送。
- 本窗口新增开源来源准入规则与 v3 manifest/validator：登记 Keycloak 本地 OIDC 目标、Medusa 2.21.2 REST 目标、Open Food Facts、UCI Online Retail II、Amazon ESCI/Reviews、英文 Stack Exchange 问答候选和中文 FAQ/社区问答缺口。UCI 页面与 API 许可元数据冲突；Keycloak 选定发行版许可待核验；待许可/隐私审核的数据均未下载。Medusa MIT 范围排除 Enterprise 内容。公开数据、开源服务本地集成和派生本地测试记录分别标记，`real_service_acceptance=false`。
- 来源变更同步到 `AGENTS.md`、`FACTS.md`、`README.md`、`docs/architecture.md`、`eval/README.md` 和私有专题 `.local/dev-docs/docs/数据来源与评测输入.md`；新增 `eval/test_validate_public_sources.py`。冻结的 60 条案例、37 份合成文档、批准答案点和 evidence sidecar 未改。
- 来源校验器通过，`python/.venv/bin/pytest -q eval` 为 185 passed，相关 Ruff 通过；`python3 scripts/check-doc-baseline.py`、`python3 scripts/check-docs.py`、`python3 scripts/privacy_guard.py --identity`、`git diff --check` 和 `bash scripts/check.sh` 通过。最新完整检查中 Python 为 292 passed、27 skipped；双前端测试/构建及 Go 检查通过。没有下载公开数据、部署服务或发起新的模型请求。最新门禁复核报告为 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-gate-human-review-20261013-rerun.json`。

## 下一步任务

1. 由数据库运维提供带 pgvector 0.8.6 的新空 V2 数据库；当前 `ecr_v2_test_20261011` 缺少 `vector` 扩展，不能由受限迁移账号自行安装。
2. 在新空库运行 checksum 迁移、身份种子和四进程，再以 `/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/documents.jsonl` 执行 37 文档生命周期。
3. 生命周期 PASS 后运行 53 条 Go HTTP/SSE E2E，再用人工 sidecar、安全报告和 E2E 报告运行最终质量门禁。
4. 完成上述验证后运行 `bash scripts/check.sh`、文档检查、数据库跨进程和双前端回归。推送/合并仍需用户明确授权。
5. OIDC/商家 API 后续集成前，核验选定 Keycloak 发行版许可证、确定服务版本与运行范围，再设计本地合成测试账号和 Medusa 测试数据；不得把本地集成结果描述为真实生产商家验收。
6. 公开数据后续获取前先解决 UCI 许可元数据差异，完成各候选的字段/隐私、语言/司法辖区和用途审查；当前中文官方 FAQ/政策与中文社区语料缺口保持显式，不用许可未明来源填补。

## 遗留问题

- 当前质量门禁为 `NOT_PASSED`，新 generation 完整回归和人工复核已通过组件门槛，但 53 条 Go HTTP/SSE 入口证据尚未生成；不能报告 V2 质量基线通过。真实 OIDC、真实商家 API、M2/M3 或生产就绪仍未验收。
- Keycloak OIDC、Medusa REST 均未部署或集成；Keycloak 精确发行版许可待核验。UCI Online Retail II 页面标注 CC BY 4.0 而 UCI API license 元数据为空；Open Food Facts 虽记录了数据/内容许可范围，具体字段和隐私审查尚未完成；Amazon 与社区来源仍待许可/隐私核验，中文官方 FAQ/政策和中文社区问答暂无已选来源。
- `run_v2_quality_e2e.py` 的真实 53 案例运行尚未执行：新 37 文档 runner 已就绪，但独立空库缺少 pgvector 扩展，迁移在 `0001` 的 `halfvec` 类型处阻塞。
- 百炼完整批次账本和失败预留必须保留，不能清零、覆盖或改写历史报告。生产多实例恢复、容量和灾备不在本阶段证据内。
- `/opt/ecommerce-rag-ops` 是 DB 运维职责约定，本开发主机没有该目录；LangChain pending-deprecation warning 保留。
