# 95 控制提示词修复 + 真实模型 12×1 重跑

- 状态：见 INDEX.md
- 依据：WP92 真实模型 9/12；用户 2026-11-08 要求修 3 例并重跑
- 前置：WP92 已集成（`969d8f9`，状态 implemented）

## 目标

修控制提示词对写请求与配送争议的路由，使 after-104/105 走 `unsupported_request`、
after-015 同时请求状态与物流；重跑 12 个事实型 case ×1，gate 目标 12/12。不改冻结契约/断言。

## 范围

1. 只改 `CONTROL_AFTER_SALES_SUFFIX`：
   - 执行写操作（取消/退款/退货换货/改址/上门取件）→ `intent=factual`、
     `information_source=realtime_business`、`required_fact_tools=[]`，不得归为知识、不得声称已执行；
   - 政策/材料/资格问题仍归知识且空工具；
   - 配送争议（已签收但没收到/丢失/破损）同时请求 `get_order_status` 与 `get_logistics`；
     普通“到哪了”仍只请求所需工具。
2. `control` prompt 版本 v3→v4；prompt matrix 锁 v4 并断言两条规则存在。
3. eval `provider_identity` 增加 prompt variant/asset SHA-256 与控制提示词文件哈希。
4. 先定向跑 3 个变化 case，再全量 12×1；仅 after-015/104/105 应变化，其余 9 例保持一致；
   新 capture/review 绑定新 SHA-256；gate 目标 PASS 12/12。
5. 不改 Go reason 映射；不新增 repeat=2/知识 case。

## 验收

- `pytest tests/test_prompt_matrix.py`、`bash eval/run.sh offline`。
- 专用 `ecr_v2_test_wp95`/26181 隔离栈 `--profile v2_langchain_bailian`、无已发布知识，12×1。
- `after_sales_service_quality.py` PASS 12/12；报告 `real_service_acceptance=false`、无货币。
- 恢复 mock、停栈；dev 栈不受影响。

## 不要做

- 不修改 `eval/after-sales-service-contract.v1.json` 或 gate 断言；
- 不改 Go 全局 reason 映射；
- 不把非目标 case 的变化当作通过；任何回退先停下核查；
- 不打印密钥/DSN/cookie；
- 不提交本地未跟踪的审查目录，不用 `git add .`。

## 实施记录（worktree `wp95`，2026-11-08）

- `CONTROL_AFTER_SALES_SUFFIX`：写执行类（取消/退款/退货换货/改址/上门取件）显式
  `intent=factual`、`information_source=realtime_business`、`required_fact_tools=[]`，
  不得归为 knowledge、不得声称已执行；同类政策/材料/资格问题仍为 knowledge 且空工具；
  配送争议（已签收但未收到/丢失/破损）同时请求 `get_order_status` 与 `get_logistics`，
  普通“到哪了”只请求所需工具；不同订单实体指令原样保留。`CONTROL_SYSTEM` 未改。
- `control` 版本 v3→v4；`python/tests/test_prompt_matrix.py` 锁 v4，并断言上述
  写路由、政策保持 knowledge、配送争议双工具与不同订单指令均存在。
- `eval/run_after_sales_service.py` 的 `provider_identity` 新增 `prompt_variants`、
  逐资产 `prompt_asset_sha256`、聚合 `prompt_assets_sha256`，并把
  `python/app/graph/prompts/control.py`、`registry.py` 纳入 `implementation_sha256`；
  `eval/test_after_sales_service.py` 新增对应单测。
- 离线验证：`pytest tests/test_prompt_matrix.py` 24 passed；`bash eval/run.sh offline`
  354 passed，after-sales PLAN cases=12；`ruff check app tests ../eval` 通过；
  `bash scripts/check.sh` 快速层 PASSED 44 / UNCONFIGURED 0 / FAILED 0。
- 预期 live 变化仅为 after-015（双工具，ANSWER）、after-104/105
  （`unsupported_request`，HANDOFF + OPEN 工单）；after-106 仍走
  `knowledge_missing`、after-107 仍为 `unsupported_request`，其余 8 例应逐字节不变。
  gate 目标 PASS 12/12，报告保持 `real_service_acceptance=false` 且无货币字段。
- 本 worktree 未调用模型、未起隔离栈、未建 DB/env；由主 agent 用
  `ecr_v2_test_wp95`/26181、`--profile v2_langchain_bailian`、无已发布知识执行
  12×1，按新 capture SHA-256 绑定 review 后跑 gate；完成后恢复 mock 并停栈。
