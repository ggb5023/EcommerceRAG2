# 重构输入：已知问题与边界

本文只记录事实与影响，不写实现方案。

1. 遗留表 `window` / `attempt` 仍在库中，只读，代码已不引用；重构时需决定迁移或删除。
2. mock provider profile 与真实供应商路径并存；重构时需统一，否则测试与生产分支继续分叉。
3. `provider-budget.py` 已删除，但目录、文档与历史命名中仍有 budget 字样；重构时统一命名。
4. Go 负责 admin/merchant 路由与授权，Python 负责 ingest/provider 执行；当前 usage_log 由 Python AI/ingest 写入、Go 只读，边界需在重构中固定。
5. `eval/run_v2_quality_safety_http.py` 的 `run_case` 仍保留可选 `ledger` 参数供旧测试注入；
   `scripts/check.sh` 已移除 `provider-budget.py` 路径；`docs/development.md` 已不再描述账本。
6. `python/app/providers/aliyun_bailian.py` 仍保留 `_call` 的一次瞬时重试，与账单无关但影响用量行数口径。
7. `usage_log` 的 `tenant_id` 允许 NULL，用于启动自检/健康检查；请求链路已强制非空，但聚合接口需要把 NULL 单独归入 unattributed。
