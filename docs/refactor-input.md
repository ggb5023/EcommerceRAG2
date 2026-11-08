# 重构输入：已知问题与边界
> 本台账只记技术债务；**决策文档的缺失另外核对**：每次合并前跑 `ls docs/decisions/ | grep -c '^00'` 并与 0003 索引的行数比对。


本文只记录事实与影响，不写实现方案。所有结论均为 2026-10-27 在本机实测。

## WP68 已完成（2026-11-06）

- #15 幂等键：改为 `admin:<shop>:<batch>:<manifest_sha256>`，旧键只在未完成时复用；
  详见 [0036](decisions/0036-batch-scoped-ingest-idempotency.md)。
- #9 错误映射与撤回重导入：403/409/500/503 + cause 已落地并登记 openapi；
  撤回后同 source 同名重导入创建新版本，换 source 仍 409；详见
  [0037](decisions/0037-reimport-and-import-error-mapping.md)。
- #10 证据去重：Go 消费处按 chunk id 保留首次、稳定重排 rank；详见
  [0038](decisions/0038-evidence-dedupe-by-chunk-id.md)。

## WP67 已完成（2026-11-06）

- #3 预算命名残留：仅剩的 0021 旧账本注释已改名；其余 `budget` 为 live request/call
  budget 与门禁棘轮。
- #5/#6 usage 口径：重试按行计入 `usage_log` 的口径已写入 `docs/metrics.md`；
  `tenant_id IS NULL` 在 Go/Python 聚合中单独归入 `unattributed`。
- #7 `run_case` 的旧 `ledger` 注入参数与对应测试已删除。
- #11 `resolveFactIntent` 返回克隆值，不再修改入参。
- #12 `turnGuard` 终结路径评估后保持现状：它复用同一个 `finish` 闭包，没有第二条
  `finishTurn` 路径；改签名风险大于收益。
- #16 `setTrace` 已提前到检索返回后调用，ASKING 与生成前 FAILED 也会落 rejected。

## 待决问题

> 本清单是历史累积：条目标注“已证伪”“已由 WPxx 完成”“已取消”或“保留为历史”的属于记录，
> 不是待办；判断当前状态时以对应工作包、代码与 `docs/workpackages/INDEX.md` 为准。

1. ~~遗留表 `window` / `attempt` 仍在库中~~ —— **此条已证伪**：dev 库（全 schema）无 `window` / `attempt`，
   35 条迁移里也从未创建过；代码同样无引用。属于历史命名残留，**无需迁移决策**，只是文档里别再提。
2. mock provider profile 与真实供应商路径并存；不统一，测试与生产分支会继续分叉。
3. `provider-budget.py` 已删除并由 `scripts/dev/provider-usage.py` 取代（0021），
   但目录、文档与历史命名中仍有 budget 字样；需要统一命名
   （文档侧残留清点见总纲 R0，以 `check-docs-drift` 实测为准）。
4. Go 负责 admin/merchant 路由与授权，Python 负责 ingest 与模型调用；当前 `usage_log`
   由 Python 以 AI / ingest 角色写入、Go 只读。这条边界要在重构中固定下来，并有测试守护。
5. `python/app/providers/aliyun_bailian.py` 的 `_call` 保留一次瞬时重试，重试同样计费，
   会让一次业务调用产生两行 usage；按次记还是按行记，需要在重构中定口径。
6. `usage_log.tenant_id` 允许 NULL（启动自检 / 健康检查用）；请求链路已强制非空，
   但聚合接口尚未把 NULL 单独归入 unattributed 桶。
7. `eval/run_v2_quality_safety_http.py` 的 `run_case` 仍保留可选 `ledger` 参数供旧测试注入；
   仅是测试注入点，不影响生产路径，但属于历史命名残留（0021 之后不再有账本），建议重构时清掉。
8. `docs/ui-only-acceptance.md` 的矩阵是 UI 覆盖入口；后端路由数不在此硬编码，
   以 `go/internal/admin/handler.go` 和守卫输出为准。已有 UI 缺口见该文。
9. `adminConfirmImport` / `persistParsed` 把 `IngestAuthorizationError` 与迁移不完整这两类不可重试错误统一映射成
   503 `ingestion_queue_failed`，与"临时排队失败"混淆；另：某文档的 active 版本被 revoke 后，同名再导入在 confirm
   阶段失败（换新 document id 则正常，根因待查）。
   修法方向：按错误类型映射（403 / 500 / 409），并在响应里带 cause code；撤回后重导入的语义需单独定
   （允许新版本？要求换 id？）。
10. 上游 `Evidence` 可能返回重复 id；当前不去重，会重复落库与重复引用。去重是有价值的行为改进，但需独立决策：
    确认上游是否会重复、去重后引用计数如何算、是否需要按 (id, version) 而非仅 id 去重。不要在重构提交里顺便加。
11. `resolveFactIntent` 会修改传入的 `routed`（`Intent` / `Clarification` / `RequiredFactTools`）。
    纯函数改入参是隐患：调用方难以判断哪些字段被写过。重构后再评估改为返回值；本轮保持原行为，不在此处变更语义。
12. `turnGuard` 持有 `finish` 闭包：授权失败可以直接终结轮次，而不是把错误返回给编排层。
    这是既有行为（当前保持），但它让阶段之外多出一条终结路径；重构后评估是否改为返回错误、
    由 `runTurn` 统一决定终结。
14. import 确认链的状态机设计：曾计划用 `confirm_failed` 作为"排队失败"的终态，后经核实 `Submit` 带
    `idempotency_key = "admin:<shop_id>:<manifest_sha256>"`（`admin/ingestion.go:queuePackage`），
    重复排队不会产生第二个 job，该终态被取消——排队失败一律回补 `staged` 让商家重试。
    任何新增状态值必须先回答三件事：**谁能进入、谁能离开、留在里面会怎样**；答不上第三条就不加。
15. 幂等键不含 batch id 的副作用：`"admin:<shop_id>:<manifest_sha256>"` 让"同一店铺内两个内容相同的批次"折叠成同一个
    摄取任务——第二个批次的确认会拿到第一个批次的 job id。当前看像特性（省一次解析），但批次与 job 的一一对应关系
    因此不成立；对账、撤销、审计都可能踩到。待评估是否需要把 batch id（或批次序号）并入幂等键。
16. `turn_decision.rejected` 只在生成阶段通过 `setTrace` 写入；
    ASKING（快照回退）与生成前 FAILED 的轮次，检索阶段采集到的 `rejected` 不会落库，
    落库的是空数组。影响：这三类轮次的"为什么不用某条证据"不可查。
    修法：把 `setTrace` 提前到检索阶段结束时调用一次（`rejected` 在那时已知），
    生成阶段再补 chosen。属 B6 第三步，不在本轮范围。

17. 顾客端没有与 0005 等价的 `can_copy` / `blocked_reason` 服务端字段；WP20 的前端按
    "AI 回答 + 至少一条引用"推导，0005 的 Go 判定仍只覆盖员工建议接口。统一口径需要
    单独设计（顾客回答是否允许复制、由哪一层判定、快照如何暴露），本轮不实现。
    **（已由 WP59/0041 完成：顾客 `can_copy`/`blocked_reason` 由 Go 判定，并按 0041 §2.5
    覆盖非 mock 事实；此条保留为历史。）**

18. 情报域直连网页抓取与 `V2_INTEL_PROXY_URL` 出口尚未接线（0032 原则 5）。需要
    真实商家场景、代理出口策略与封禁风险数据后再设计；当前 Tavily 单数据源可闭环，
    `market_observation` 的 HNSW 索引也留到真实行量再评估（WP69 明确不做）。

## 已完成（仅供参考，无需再处理）

- **P1-6 稠密检索 HNSW（WP40）**：采用有界超额 ANN 子查询（K=8，上限 256，
  授权不足时重试一次）；50k 测试库 EXPLAIN 自然命中 `idx_chunk_embedding`。
  Python 保持 `(distance, chunk_id)` 稳定顺序；稀疏分支与 `SPARSE_RETRIEVAL`
  的 legacy 默认未改。
- `scripts/check.sh` 已移除 `provider-budget.py` 路径。**已核实**：`grep -c provider-budget scripts/check.sh` = 0；
  现役脚本为 `scripts/dev/provider-usage.py`。
- ~~`docs/development.md` 已不再描述账本~~ —— **此条已随 R0 清理失效**：`scripts/check.sh` 已移除
  `provider-budget` 路径，现役脚本为 `scripts/dev/provider-usage.py`；文档侧残留以总纲 R0 的实测输出为准。


## 阶段收尾（2026-11-06，WP65–WP69 后）

本轮关闭：#3/#5/#6/#7/#9/#10/#11/#12/#15/#16/#17（见上方 WP67/WP68 已完成段）。
**仍开放（交用户审视）**：

- #2 mock/真实 provider 双路径并存——测试与生产分支继续分叉；
- #4 usage_log 写者边界（Python 写、Go 读）需一条测试守护；
- #8 ui-only-acceptance 矩阵里的 UI 缺口（以该文实测为准）；
- #18 情报域直连网页抓取与 `V2_INTEL_PROXY_URL` 出口未接线（0032 原则 5，
  阶段验收边界 = Tavily 单数据源，见 0032 实现状态）；
- market_observation 的 HNSW 索引留到真实行量再评估（WP69 备注）。

**外部依赖（不在仓库内解决）**：P3 真实商家试用（等用户启动）；Keycloak oidc
剖面的 compose 实跑（realm 导入要求已写入 install.md，本机已用真实 Keycloak
完成过授权码全链路验证）；OSS 真实归档演练（桶权限已授予，往返验证已通过，
备份/恢复脚本未对真实桶演练）。
