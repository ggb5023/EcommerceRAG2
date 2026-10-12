# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`；本轮未推送或合并。质量闭环新增提交 `0a7ab64`（受限证据上下文、答案 capture、定向案例）和 `2e468bd`（分层质量门禁）。
- 固定 revision-1 输入、37 文档、批准 evidence sidecar 和 53/7 安全分区未修改。百炼固定 control、Embedding、Rerank、generation 槽位；用户已授权本阶段不限额度，但所有调用仍保留共享 SQLite 预算、限流和失败预留。
- 新 generation 契约已提交为 `115eba3`：有相关证据时不得返回空答案点，拒答/澄清/来源限制/冲突/时效规则必须显式表达，并保持日期、版本、数字、单位和限定词。定向 9 案例复测 41 次请求通过；完整新回归 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-full-generation-gate-20261013.json` 完成 53/53、236 次请求，使用独立质量窗口账本，`real_service_acceptance=false`。
- 新回归答案 capture 位于仓外 `/var/lib/ecommerce-rag/quality-review/full-generation-gate-20261013/`，新人工复核包位于 `/var/lib/ecommerce-rag/quality-review/human-review-full-generation-20261013/`；包已生成但需绑定实际逐案审核字段后才能生成 `reviewer_kind=human` sidecar。
- 完整 context-neighbors 百炼报告 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-full-context-neighbors-20261012.json`：53/53、236 requests、PASS transport；同版本 source-neighbor 扩展后批准证据完整交付 53/53；Rerank Recall@5=1.0、MRR=0.981132、nDCG@5=0.986073。
- 答案原文只保存在仓外 `/var/lib/ecommerce-rag/quality-review/full-context-neighbors` 的 0600 文件；工程诊断 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-engineering-review-v2-20261012.json` 明确 `reviewer_kind=engineering`、`human_review_completed=false`。模型拒答/澄清/时效/限定词表现未达门槛，质量门禁 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-gate-v2-20261012.json` 为 `NOT_PASSED`（高风险失败 42、普通通过率 0）。
- 七条越权、撤权或过期案例安全报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-safety-20261012.json` PASS，Provider attempt delta=0。不得把安全前置报告升级为业务质量通过。
- 独立数据库真实 Go→gRPC→Python→百炼 三格式生命周期报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-live-lifecycle-20261012-final4.json` PASS，覆盖 3 个受控 fixture 的提交、解析、审核、发布、检索、重排、生成、引用、终态持久化。它不覆盖 37 文档/53 案例，不代表 M2 或生产就绪。
- `eval/run_v2_quality_e2e.py` 已要求真实生命周期、独立安全报告、53 eligible 案例、SSE 单调序列和终态持久化；由于现有生命周期只有 3 个 fixture，本轮拒绝伪造 53 案例质量 E2E。
- 质量评测代码验证：`python/.venv/bin/python -m pytest -q eval` 176 passed；changed eval scoped Ruff、privacy guard、git diff check 通过。随后本窗口的文档检查器调整后又运行了 `scripts/check.sh`，结果通过。
- 本窗口完成受 Git 管理的工程文档边界调整：完善 AGENTS/FACTS/README，新增 `docs/architecture.md`，公开检查只要求根入口文档，`.local/` 专题仍为私有。隐私扫描仅增加架构摘要的精确路径例外，内容扫描照常；`bash scripts/check.sh` 在文档和检查器更新后通过。该文档任务独立提交，不推送。
- 本窗口新增开源来源准入规则与 v3 manifest/validator：登记 Keycloak 本地 OIDC 目标、Medusa 2.21.2 REST 目标、Open Food Facts、UCI Online Retail II、Amazon ESCI/Reviews、英文 Stack Exchange 问答候选和中文 FAQ/社区问答缺口。UCI 页面与 API 许可元数据冲突；Keycloak 选定发行版许可待核验；待许可/隐私审核的数据均未下载。Medusa MIT 范围排除 Enterprise 内容。公开数据、开源服务本地集成和派生本地测试记录分别标记，`real_service_acceptance=false`。
- 来源变更同步到 `AGENTS.md`、`FACTS.md`、`README.md`、`docs/architecture.md`、`eval/README.md` 和私有专题 `.local/dev-docs/docs/数据来源与评测输入.md`；新增 `eval/test_validate_public_sources.py`。冻结的 60 条案例、37 份合成文档、批准答案点和 evidence sidecar 未改。
- 来源校验器通过，`python/.venv/bin/pytest -q eval` 为 185 passed，相关 Ruff 通过；`python3 scripts/check-doc-baseline.py`、`python3 scripts/check-docs.py`、`python3 scripts/privacy_guard.py --identity`、`git diff --check` 和 `bash scripts/check.sh` 通过。完整检查中 Python 为 291 passed、27 skipped；双前端测试/构建及 Go 检查通过。没有下载公开数据、部署服务或发起模型/爬虫请求。

## 下一步任务

1. 针对新 generation 回归输出完成 53 条实际人工语义复核，生成绑定新报告和 capture 的 `reviewer_kind=human` sidecar；不得用工程 token overlap 诊断替代人工结果。
2. 使用人工 sidecar、七条安全报告和完整生命周期证据运行最终质量门禁；若门禁仍因 Go HTTP/SSE 证据缺失保持 `NOT_PASSED`，按真实范围记录，不升级为通过。
3. 若要运行 53 条 Go HTTP/SSE 入口评测，先在新的独立数据库按 37 文档语料完成真实生命周期，不能复用当前三 fixture 生命周期报告；随后提供安全报告并运行 E2E 驱动。
4. 完成上述修复后再运行 `bash scripts/check.sh`、文档检查、数据库跨进程和双前端回归。推送/合并仍需用户明确授权。
5. OIDC/商家 API 后续集成前，核验选定 Keycloak 发行版许可证、确定服务版本与运行范围，再设计本地合成测试账号和 Medusa 测试数据；不得把本地集成结果描述为真实生产商家验收。
6. 公开数据后续获取前先解决 UCI 许可元数据差异，完成各候选的字段/隐私、语言/司法辖区和用途审查；当前中文官方 FAQ/政策与中文社区语料缺口保持显式，不用许可未明来源填补。

## 遗留问题

- 当前质量门禁为 `NOT_PASSED`，新 generation 完整回归已通过传输和结构化契约，但人工 sidecar 与 53 条 Go HTTP/SSE 入口证据尚未绑定；不能报告 V2 质量基线通过。真实 OIDC、真实商家 API、M2/M3 或生产就绪仍未验收。
- Keycloak OIDC、Medusa REST 均未部署或集成；Keycloak 精确发行版许可待核验。UCI Online Retail II 页面标注 CC BY 4.0 而 UCI API license 元数据为空；Open Food Facts 虽记录了数据/内容许可范围，具体字段和隐私审查尚未完成；Amazon 与社区来源仍待许可/隐私核验，中文官方 FAQ/政策和中文社区问答暂无已选来源。
- `run_v2_quality_e2e.py` 的真实 53 案例运行尚未执行：当前独立生命周期仅三种 fixture，驱动会拒绝 corpus 不完整或缺安全证据的运行。
- 百炼完整批次账本和失败预留必须保留，不能清零、覆盖或改写历史报告。生产多实例恢复、容量和灾备不在本阶段证据内。
- `/opt/ecommerce-rag-ops` 是 DB 运维职责约定，本开发主机没有该目录；LangChain pending-deprecation warning 保留。
