# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`。本轮开发重构的能力、代码、运行证据和文档已收口；退出基线以本文件所在本地提交为准，最终提交/工作树与 G1–G8 审计见 `.local/dev-docs/archive/20261012-v2-completion-audit.json`。Gateway 恢复提交 `a08f99b`，四进程浏览器恢复提交 `9102671`；未推送或合并。
- 活动入口固定为 Go `cmd/gateway` 和 Python `app.rpc.server`、`app.rpc.ingest_server`、`app.ingest.worker_main`。旧 Python RPC、synthetic HTTP 与 Go artifact-binding 工具已退出；有效权限/解析/管理用例迁到当前实现，HTTP `/v1`、`rag.v1` 和历史迁移保留。能力替代与证据统一在私有 `V1到V2能力迁移清单.md`。
- Gateway 使用独立 advisory lock 连接限制每库单实例；ready 前将中断执行持久化为 FAILED/`gateway_restarted` 或 CANCELLED，连续 SSE 同事务写入，不自动重发模型请求。实际四进程 crash/restart 浏览器 6 项通过，DONE/ASKING 保留并可恢复。
- 最终基础检查通过：Go、Proto、双前端测试/build、facts fake 矩阵；最终 Python `291 passed/27 skipped`。默认跳过的 24 个摄取与 3 个检索参数用例已在独立库实际执行通过（worker 24 项、retriever 共 5 项）；Go 管理权限/命令对账及 Go→Python 跨进程通过。新增真实 SIGKILL、自然 lease 到期、接管/fencing、单对象清理失败重试，以及管理申请/批准/撤销/不可变审计回归。
- 双前端生命周期 13 项、客服工作台 12 项、实际撤权/权限版本浏览器 3 项通过；最新生命周期报告 `.local/v2-lifecycle/browser/f19625bfb9ac/report.json`。完整脱敏报告路径及 hash 在最终审计中。
- 本轮真实百炼三格式摄取→审核发布→检索重排生成通过：3 个版本、5 个 chunks、17 次完整链路请求，四槽位 usage/请求指纹与引用版本已复核。共享账本累计 18 次请求、费用预留上界 0.36728 元（非实际账单）；本窗口无新增付费调用，账本不清零。混合输入保持 `real_service_acceptance=false`。
- README、FACTS 及受影响架构/方案/迁移/接口/数据/摄取/运维/验收专题已同步；私有资料与归档不进入 Git。窗口过程在 `.local/dev-docs/archive/20261012-v2-retirement-171848.md`。
- 质量评测阶段已建立：`eval/quality_gate.py` 分离 Recall/MRR/nDCG、批准证据交付、引用有效性和人工语义复核；`eval/run_v2_quality_e2e.py` 通过 Go HTTP/SSE 记录脱敏端到端元数据。批次身份绑定输入 hash、Git HEAD、generation 指令 hash 和脱敏 Provider 配置。
- 新百炼基线报告 `/var/lib/ecommerce-rag/real-docs/reports/bailian-quality-baseline-20261012.json` 已完成 53/53 可行案例、7 条不送模、236 次请求，`real_service_acceptance=false`。Rerank Recall@5=1.0、MRR=0.981132、nDCG@5=0.986073；完整批准支持片段仅 8/53，答案质量门禁为 `NOT_PASSED`。

## 下一步任务

1. 用候选窗口 20 的修复版本重新执行独立百炼批次，核对 53 个案例的批准支持片段交付是否达到 95%；保留当前基线报告，不覆盖历史证据。
2. 如支持交付达到门槛，建立受限 `v2-quality-review-v1` 人工复核 sidecar，覆盖 53 条案例的矛盾、引用、答案点、拒答和安全结果，再运行 `quality_gate.py`。
3. 生命周期通过后使用 `eval/run_v2_quality_e2e.py` 经 Go HTTP/SSE 验证端到端结果；再按结果决定是否修复 generation/citation gate。真实 OIDC、真实商家 API、正式 HITL 和生产容量仍不在本阶段范围。
4. 开始新任务前核对实际分支、HEAD、工作树和最终审计；不得把 ahead 数量当成推送范围。推送/合并仍需用户明确授权。

## 遗留问题

- 当前结果不能宣称真实 OIDC/商家 API、M2/M3 或生产就绪。生产多实例恢复、生产规模检索质量和灾备容量仍未验收。
- live 独立数据库已有本轮发布版本，不能直接重跑要求空索引的 live lifecycle runner；复核现有证据或使用新独立索引，并沿用原共享预算账本。
- 通用显式 PID shell worker harness 未做本轮运行演练；真实 worker 进程恢复由当前 Python/独立 PG 测试提供证据，不用静态前置检查替代运行。
- `/opt/ecommerce-rag-ops` 是 DB 运维职责约定，本开发主机没有该目录；远端布局/生产部署不能由本轮独立库测试推断。LangChain 的一条 pending-deprecation warning 保留，依赖升级另行处理。
- 候选窗口 10→20 的代码修复尚未重新进行在线验证；当前 baseline 仍来自修复前参数。人工语义复核 sidecar 尚未建立，因此不能报告质量门禁通过。
