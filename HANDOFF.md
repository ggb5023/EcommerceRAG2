# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`。用户委托 Codex 逐例语义审核，记录 `reviewer_kind=agent`、`human_review_completed=false`；不能把请求“全部通过”当作验收结论。本轮未推送、合并或修改 V1。
- 冻结 revision-1 的 60 条案例、37 文档、alignment、答案点和 context-v2 evidence sidecar 未改。百炼四槽位、1024 维、control 256/generation 512 token 上限不变；不限额度开发授权仍使用原共享账本、限流和失败预留。
- 独立 `ecr_v2_dev_20261013_temporal` 已有 37 文档生命周期证据，保留源有效日期、披露分类及非超级用户边界；36 文档发布、1 个 unclassified 保持不可见。生命周期报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-live-lifecycle-temporal-20261013.json`。本库已经填充，不能再次称为空库。
- 最新完整入口批次 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-http-e2e-compact-five-20261013-full.json` 完成全部 53 条：50 DONE、2 ASKING、1 FAILED，207 次 Provider 请求，工程状态 `NOT_PASSED`。幂等、刷新、SSE 单调序列和回放保留逐例证据；成功终态不能替代答案质量。
- 最新代理复核修订版 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-agent-review-http-e2e-compact-five-20261013-r2.json`：37 通过、16 失败、11 个高风险失败；普通案例通过率 6/11，引用有效率 49/53。实际原文 capture 在 `/var/lib/ecommerce-rag/quality-review/http-e2e-compact-five-20261013-full/`；逐例备注和见证在 `/var/lib/ecommerce-rag/quality-review/agent-http-e2e-compact-five-20261013-r2/`。首版及所有历史报告保留，不覆盖。
- 新七案例 Go HTTP/SSE 安全报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-safety-http-compact-five-20261013-full.json` 为 PASS；全部 Provider 增量 0，4 项旧结果/回放撤权和权限版本检查 PASS。安全报告绑定本轮完整入口和生命周期；它不改变答案批次失败状态。
- 最新组合门禁 `/var/lib/ecommerce-rag/real-docs/reports/v2-quality-gate-agent-compact-five-20261013-final-r2.json` 为 `NOT_PASSED`。组件已批准基线仍 PASS；入口支持率 38/53、遗漏率 14/53、失真率 1/53、未发现矛盾或严重安全错误，整体不能放行。
- 本轮修复了目录推荐过早依赖 SKU、Fact 资源/观察时间/mock 元数据、五字段澄清计划及其固定 token 预算、失败批次审核与独立安全验证。新增 Provider 修复将不完整 finish reason/非法 JSON/schema/usage 的 HTTP 200 记为失败并保留请求指纹、有效 usage 和费用预留；新增评分按组件/HTTP 分别输出语义指标，不导出私有备注。
- `syn-004` 本轮 control 输出达 256 token 上限后 FAILED；3 次受控同问题诊断返回 stop、91–109 token，未复现原失败。诊断 `/var/lib/ecommerce-rag/real-docs/reports/v2-control-diagnostic-20261013.json` 不替代完整入口，原失败具体原因仍未证实，旧账本不改写。
- 验证：`bash scripts/check.sh` PASS（Python 341 passed/27 skipped；Go 测试/vet、Proto、双前端 19+5 测试与构建、商家 fake 故障矩阵）。最新 eval 234 passed；Provider/graph/RPC 定向 96 passed。真实浏览器 lifecycle 13 项与 workspace 12 项 PASS；报告分别在 `.local/v2-lifecycle/browser/` 与 `.local/v2-workspace/browser/`，精确运行路径见本轮私有归档。
- 本轮验收四进程和 facts fake 已停止，用户已有 Vite 进程保留。启动继续使用 `bash scripts/v2-local-services.sh --env /path/to/protected-runtime.env`，真实验收配置须匹配 Go 地址、独立库、四槽位和原账本。新 Provider 失败分类有离线测试；最新完整在线批次在该修复前运行，不能宣称已验证新代码的全部真实模型行为。
- 文档已同步 AGENTS、FACTS、README、eval README，以及私有模型/接口/验收专题；过程留在 `.local/dev-docs/archive/20261013-质量入口代理复核续作.md`。本轮功能提交包含 `c92076a`、`d573ee8`、`cd3ae92`、`6847f88`、`9a21ce7`、`24f3740`、`dea0343`、`b8d3c03`、`4e1b543`、`f9db3ba`、`80d661d`。

## 下一步任务

1. 以最新 r2 代理包逐例修复，而非反复重跑取最好答案。优先定位 `syn-004` control 非确定性输出，并修复 `syn-015` 快照版本遗漏、`syn-024` 跨资源条件套用、`syn-028/029` 无证据例子、`syn-037` 工作流步骤遗漏。
2. 为 `syn-007/031` 建立独立、版本化、hash 绑定的前置会话场景；只经 Go 实际提交，不改变冻结查询或 SQL 插入历史。`syn-013/016` 缺唯一 SKU；确认快照/实时事实路由和已有合法来源绑定，不能从商品片段猜商品 ID。
3. 对 `syn-012/017/025/026/027/028/029` 登记资料或接口能力缺口：现有冻结输入没有款式用途、当前承诺时效接口、实际今天公告、标准修订/发布日期、本月报道或成分清单。不修改基线真值、补造证据或引入未准入来源；新增来源应走独立批准数据轨及后续验收设计。
4. 每项修复先定向高风险案例，再完整 53 条、新代理复核及与该批次绑定的七安全案例，另写报告；达到门槛后才能按 Keycloak → Medusa → Open Food Facts 顺序继续集成。
5. 按影响补齐隔离数据库故障、权限浏览器和重启恢复验证。本轮默认跳过的 27 项数据库测试尚未重跑，不能用 foundation PASS 替代。保持小提交和文档同步，不推送。

## 遗留问题

- 质量门禁未通过，失败 ID：syn-004、007、012、013、014、015、016、017、024、025、026、027、028、029、031、037。拒答安全不等于冻结合成答案点完成；模型提示词不能补足不存在的源事实。
- 原始公开数据未下载；Keycloak/Medusa 尚未部署或集成。Keycloak 精确发行版许可、Open Food Facts 字段/隐私/用途审核仍需完成；UCI 页面/API 许可差异、Amazon/社区许可及中文 FAQ/政策来源缺口保留。
- V1 代码只读保留；本轮未执行 V1 备份恢复验证、只读封存或删除，不能认为七天封存期已开始。公网 DB 主机此前 SSH/5432 超时，本轮没有改动其基础设施。
- `/opt/ecommerce-rag-ops` 是职责约定，本开发主机没有该目录。保留 LangGraph pending-deprecation warning。生产多实例、容量、灾备、M2/M3 不在已验收范围；`real_service_acceptance=false`。
