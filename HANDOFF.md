# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`。小功能本地提交，不推送、不合并；V1 代码和数据库保留。用户已授权不限额度百炼并确认 generation 免费额度限制解除。代理语义复核记录 `reviewer_kind=agent`、`human_review_completed=false`，不冒充人工。
- 冻结输入保持不变：60 案例、37 文档、原答案点、alignment、context-v2 evidence sidecar、历史报告；边界契约 SHA-256 `33d3a9b2f8006a6134f4698ccd3d70a10a4c5d61119bd16b7a6ac0895376eb6e`。四模型、1024 维、control 256/generation 512 token、原共享账本和 60 RPM 不变。
- 已有会话 Proto 字段、Go 六条/8192 字节授权历史、20 个 Rerank 正文候选和最多 8 条生成上下文继续使用。本接续完善生成的条件、来源级别、适用维度、原因缺口和配送能力限制；实际生成字段须受来源与引用支持，不能从评测真值补答案。control 现只消费用户历史，区分唯一资源与缺版本/日期，防止助手来源或比较造成误澄清。HTTP/SSE、浏览器、DDL 无新增契约。
- 历史日期 2026-09-29 的最新完整 r40，在业务修复提交 `1d3d30b` 上实际执行 53/53 并逐例代理复核。独立边界门禁 PASS：高风险 42/42、普通 11/11、必需证据 49/49、适用引用 49/49，4 个无需引用的澄清单独报告。报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-gate-paid-full-r40-20261013.json`；受限复核包 `/var/lib/ecommerce-rag/quality-review/boundary-agent-paid-full-r40-20261013/`，capture 位于 `boundary-history-paid-full-r40-20261013/`。这只证明历史轨，整体阶段仍进行中。
- 原答案点门禁独立 NOT_PASSED：r40 44/53，44 支持、9 遗漏、0 矛盾、0 失真。遗漏 syn-012/013/014/016/025/026/029/031/042；不能用边界澄清或有限摘要抹去。原报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-original-gate-paid-full-r40-20261013.json`；组件批准基线及其人工 sidecar 不覆盖新入口答案。
- 与 r40 绑定的七安全案例及四项撤权/旧结果回放 PASS，Provider 增量 0：`v2-boundary-safety-paid-full-r40-20261013.json`。唯一前文 r37 定向 3/3；缺前文、多候选、错误指代及 ASKING 父轮次补 SKU/订单后新授权读取的 r38 八场景执行和语义复核 PASS；syn-004 r39 三次连续实际入口与语义 PASS，原非确定性截断根因仍 NOT_PROVEN。
- 失败历史全部保留：r22 边界 47/53；r29 因中断仅 44 主案例及前置 capture，无完整报告；r32 历史边界 53/53 但早于最终指代修复；r36 52/53，syn-031 已有唯一 USB-C 前文却误澄清，按真实失败记录后修复。不得选历史最好结果替代当前代码验收。
- 当前日期 r41 定向 12 例全部 FAILED：control `qwen3.8-flash` 返回 403 `AllocationQuota.FreeTierOnly`，账本为 `allocation_quota`；不是预算或本地限流耗尽。generation 解除不证明其他模型已解除。失败报告、capture 和逐例失败复核已保留；已通知用户关闭 control 的免费额度限制，尚未收到该模型解除确认。已停止新增模型请求，未换模型或新建账本。
- r42 对上述 12 个 FAILED 和一条实际 CANCELLED 共 13 个终态，四进程真实停启后比对 HTTP 结果与持久化 SSE hash，全部不变；序列连续、gap replay、幂等保持，取消后无答案/引用/可复制回复或晚到结果，恢复未新增 Provider 请求。只证明失败关闭/取消恢复，不能替代当前时效答案验收。汇总 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-paid-window-r43-20261013.json` 为 IN_PROGRESS。
- 已补 HTTP 批跑全局账户阻塞保护：根据每例新增原共享账本错误，遇到 allocation_quota/鉴权/预算阻塞停止后续案例，保留失败并记录计划、实际与未运行项；部分批次不能过 53 门禁，历史错误不阻断新授权批次。提交 `ba34516`，新增七项检查；本接续 `1d3d30b` 修复用户指代及两项回归测试。
- 最新基础检查 PASS：Python 393 passed/27 skipped；Go test/vet、Proto、双前端 19+5 测试和构建、facts fake 故障矩阵通过。eval 281 passed；指代定向 74 passed，ruff、文档、隐私和差异检查通过。27 个数据库跳过此前用 `ecr_v2_test_20261013_checks` 补跑 30 项、Go 授权/命令对账 PASS；非超级用户/无 BYPASSRLS、25 迁移 checksum、vector 0.8.6/pg_trgm 已验证。没有因本次仅控制指代与评测工具改动重复数据库或前端业务流程。
- 既有实际浏览器恢复 6、权限/撤权 3、双前端生命周期 13 PASS；脱敏证明 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-offline-verification-20261013.json`。该报告是早先独立验证，不能当作新增在线答案证明。
- 独立验收库 `ecr_v2_dev_20261013_temporal` 保留 37 文档真实提交/解析/切片/Embedding/审核/发布结果：36 发布、1 unclassified 不可见。生命周期报告 `v2-live-lifecycle-temporal-20261013.json`。此库已经填充，不是空库。
- 公开 eval 说明、FACTS 与私有模型/验收专题同步；本窗口记录 `.local/dev-docs/archive/20261013-付费恢复与证据边界复测.md`。资料来源/接口专题沿用本窗口此前更新，不增加状态矩阵或改历史归档。临时四进程/facts fake 已停止，其他既有服务保持。

## 下一步任务

1. 收到 control 模型 `qwen3.8-flash` 免费额度限制解除确认后，用原配置和原账本先做一次入口验证；继续 60 RPM，不用新模型/账本绕过。新批跑有账户阻塞保护，所有新报告不可覆盖历史。
2. 重跑当前日期的旧库存、价格、促销、公告、标准及报道定向验证；明确观测时间、mock、来源版本与缺口，不把历史快照/无日期资料当当前事实。补跑 SKU/订单 ASKING 子轮次，并对新 DONE/ASKING 终态做重启结果/SSE hash 比对。FAILED/CANCELLED 恢复已验证，不能升级为答案通过。
3. 保持 r40 独立边界与原答案点评分分别报告。仅历史、当前时效、补充场景、安全、取消与恢复证据齐全后，报告“合成资料上的 V2 证据边界验收通过”；原答案点缺口和 real_service_acceptance=false 保持可见。
4. 放行后按 Keycloak → Medusa 2.21.2 → Open Food Facts 推进。固定发行版许可、字段/隐私/用途先核验；Keycloak 使用合成测试身份，经 Go 授权与撤权验证；Medusa 经 Go facts 边界；OFF 排除图片，获准同源字段分别进入商品目录与 RAG 生命周期。

## 遗留问题

- 整体阶段 IN_PROGRESS：历史轨边界 PASS，但当前时效受 control 免费额度设置阻塞，未通过语义与 DONE/ASKING 恢复验证，不进入服务集成。原答案点仍 NOT_PASSED。
- Keycloak/Medusa 尚未部署集成，公开原始数据未下载。Keycloak 26.8.1 官方源代码许可已只读核对为 Apache 2.0，未完成选定发行包核验或准入，不改成集成事实。OFF 字段/隐私/用途、UCI 许可差异、Amazon/社区与中文 FAQ/政策来源缺口保留。
- V1 备份恢复及只读封存未执行，七天保留期尚未开始；不删除 V1 代码或数据库。旧代码未提交改动不是已验证回滚基线。
- `/opt/ecommerce-rag-ops` 是职责约定，开发主机无该目录；生产多实例、容量、灾备、生产身份/商家 API、M2/M3 未验收。LangGraph pending-deprecation warning 保留。
