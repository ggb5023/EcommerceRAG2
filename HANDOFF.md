# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`；本轮未推送或合并。质量闭环新增提交 `0a7ab64`（受限证据上下文、答案 capture、定向案例）和 `2e468bd`（分层质量门禁）。
- 固定 revision-1 输入、37 文档、批准 evidence sidecar 和 53/7 安全分区未修改。百炼固定 control、Embedding、Rerank、generation 槽位；用户已授权本阶段不限额度，但所有调用仍保留共享 SQLite 预算、限流和失败预留。
- 完整 context-neighbors 百炼报告 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-full-context-neighbors-20261012.json`：53/53、236 requests、PASS transport；同版本 source-neighbor 扩展后批准证据完整交付 53/53；Rerank Recall@5=1.0、MRR=0.981132、nDCG@5=0.986073。
- 答案原文只保存在仓外 `/var/lib/ecommerce-rag/quality-review/full-context-neighbors` 的 0600 文件；工程诊断 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-engineering-review-v2-20261012.json` 明确 `reviewer_kind=engineering`、`human_review_completed=false`。模型拒答/澄清/时效/限定词表现未达门槛，质量门禁 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-gate-v2-20261012.json` 为 `NOT_PASSED`（高风险失败 42、普通通过率 0）。
- 七条越权、撤权或过期案例安全报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-safety-20261012.json` PASS，Provider attempt delta=0。不得把安全前置报告升级为业务质量通过。
- 独立数据库真实 Go→gRPC→Python→百炼 三格式生命周期报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-live-lifecycle-20261012-final4.json` PASS，覆盖 3 个受控 fixture 的提交、解析、审核、发布、检索、重排、生成、引用、终态持久化。它不覆盖 37 文档/53 案例，不代表 M2 或生产就绪。
- `eval/run_v2_quality_e2e.py` 已要求真实生命周期、独立安全报告、53 eligible 案例、SSE 单调序列和终态持久化；由于现有生命周期只有 3 个 fixture，本轮拒绝伪造 53 案例质量 E2E。
- 质量评测代码验证：`python/.venv/bin/python -m pytest -q eval` 176 passed；changed eval scoped Ruff、privacy guard、git diff check 通过。随后本窗口的文档检查器调整后又运行了 `scripts/check.sh`，结果通过。
- 本窗口完成受 Git 管理的工程文档边界调整：完善 AGENTS/FACTS/README，新增 `docs/architecture.md`，公开检查只要求根入口文档，`.local/` 专题仍为私有。隐私扫描仅增加架构摘要的精确路径例外，内容扫描照常；`bash scripts/check.sh` 在文档和检查器更新后通过。该文档任务独立提交，不推送。

## 下一步任务

1. 修复 generation 的拒答/澄清结构化门控、时效和限定词保持；每项单独提交，先用定向高风险案例复测，再跑完整 53 条回归。
2. 建立真实人工复核 sidecar（不是工程 token overlap 诊断），绑定完整答案 capture、冻结案例 hash、引用有效性和复制资格；通过后才可重跑最终门禁。
3. 若要运行 53 条 Go HTTP/SSE 入口评测，先在新的独立数据库按 37 文档语料完成真实生命周期，不能复用当前三 fixture 生命周期报告；随后提供安全报告并运行 E2E 驱动。
4. 完成上述修复后再运行 `bash scripts/check.sh`、文档检查、数据库跨进程和双前端回归。推送/合并仍需用户明确授权。

## 遗留问题

- 当前质量门禁为 `NOT_PASSED`，不能报告 V2 质量基线通过；真实 OIDC、真实商家 API、M2/M3 或生产就绪仍未验收。
- `run_v2_quality_e2e.py` 的真实 53 案例运行尚未执行：当前独立生命周期仅三种 fixture，驱动会拒绝 corpus 不完整或缺安全证据的运行。
- 百炼完整批次账本和失败预留必须保留，不能清零、覆盖或改写历史报告。生产多实例恢复、容量和灾备不在本阶段证据内。
- `/opt/ecommerce-rag-ops` 是 DB 运维职责约定，本开发主机没有该目录；LangChain pending-deprecation warning 保留。
