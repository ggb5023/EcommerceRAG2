# 重构输入：已知问题与边界

本文只记录事实与影响，不写实现方案。所有结论均为 2026-10-27 在本机实测。

## 待决问题

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
8. `docs/ui-only-acceptance.md` 的矩阵已扩充为 34 条（31 条原有 + 2 条补登记 + 1 条 usage 标 ❌）；
   `GET /admin/v1/merchant/usage` 尚无 UI，随 0021 的用量界面实现。
9. `adminConfirmImport` / `persistParsed` 把 `IngestAuthorizationError` 与迁移不完整这两类不可重试错误统一映射成
   503 `ingestion_queue_failed`，与"临时排队失败"混淆；另：某文档的 active 版本被 revoke 后，同名再导入在 confirm
   阶段失败（换新 document id 则正常，根因待查）。
   修法方向：按错误类型映射（403 / 500 / 409），并在响应里带 cause code；撤回后重导入的语义需单独定
   （允许新版本？要求换 id？）。
10. 上游 `Evidence` 可能返回重复 id；当前不去重，会重复落库与重复引用。去重是有价值的行为改进，但需独立决策：
    确认上游是否会重复、去重后引用计数如何算、是否需要按 (id, version) 而非仅 id 去重。不要在重构提交里顺便加。
11. `resolveFactIntent` 会修改传入的 `routed`（`Intent` / `Clarification` / `RequiredFactTools`）。
    纯函数改入参是隐患：调用方难以判断哪些字段被写过。重构后再评估改为返回值；本轮保持原行为，不在此处变更语义。

## 已完成（仅供参考，无需再处理）

- `scripts/check.sh` 已移除 `provider-budget.py` 路径。**已核实**：`grep -c provider-budget scripts/check.sh` = 0；
  现役脚本为 `scripts/dev/provider-usage.py`。
- ~~`docs/development.md` 已不再描述账本~~ —— **此条已随 R0 清理失效**：`scripts/check.sh` 已移除
  `provider-budget` 路径，现役脚本为 `scripts/dev/provider-usage.py`；文档侧残留以总纲 R0 的实测输出为准。

