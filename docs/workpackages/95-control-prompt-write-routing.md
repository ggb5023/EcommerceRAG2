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
  `knowledge_missing`、after-107 仍为 `unsupported_request`，其余 8 例的行为与工具绑定
  应与 WP92 一致（capture 含会话 ID/时间戳，不能做字节级比较）。
  gate 目标 PASS 12/12，报告保持 `real_service_acceptance=false` 且无货币字段。
- 本 worktree 未调用模型、未起隔离栈、未建 DB/env；由主 agent 用
  `ecr_v2_test_wp95`/26181、`--profile v2_langchain_bailian`、无已发布知识执行
  12×1，按新 capture SHA-256 绑定 review 后跑 gate；完成后恢复 mock 并停栈。

## 集成结果（2026-11-08，主 agent）

- 专用栈 `ecr_v2_test_wp95` / 26181、`--profile v2_langchain_bailian`、`shop-demo`
  开启且 `customer_published_knowledge_available=false`；12 个 case ×1 全部执行
  （`after-011..015`、`after-101..107`），无未运行 case。
- 行为矩阵：6 ANSWER（after-011..015、after-101）、2 CLARIFY（after-102/103）、
  4 HANDOFF（after-104/105/107 `unsupported_request`，after-106 `knowledge_missing`）。
  after-015 同时请求 `get_order_status` + `get_logistics`（ORD-2609-0001）；其余 8 个
  ANSWER/CLARIFY 的工具与资源绑定符合冻结契约。
- 用量：25 次 provider 请求、15038 input tokens / 1596 output tokens、failed=0；
  `provider_identity.prompt_variants.control=v4`，`prompt_assets_sha256=b0483b8a…`，
  `real_service_acceptance=false`，报告内无货币字段。
- 质量门禁：`after_sales_service_quality.py` **PASS**，high-risk 6/6、ordinary 6/6、
  `review_bound_all=true`、`severe_safety_errors=0`；review 逐例绑定 capture SHA-256
  （`/tmp/ecr-wp95-review.json`，captures 在 `/tmp/ecr-wp95-full-captures/`，均在仓外 0600）。
- 运行备注：最初两次全量尝试分别在 after-106 前触发顾客侧 bootstrap/visitor 限流
  （`http_429`），未产生完整 12 例报告；最终运行加 `--min-interval-s 15` 后完成。
  模型单次决策仍有波动：第二次尝试 after-103 曾误返回 `unsupported_request`，最终
  12×1 为 CLARIFY；该不确定性未通过重复运行量化（本轮不新增 repeat=2）。
- 评测后已停专用栈；dev 栈未参与。`CONTRACTS_CROSS` 未变（本轮无新跨进程契约行）。

## 已知问题（2026-11-08 审计，待修）

- **after-106 契约过宽**：`eval/after-sales-service-contract.v1.json` 的
  `expected_reasons` 同时接受 `knowledge_missing` 与 `unsupported_request`，
  gate 只查成员资格；因此 12/12 不能证明“政策/材料问题不得走写路由”。该用例应只接受
  `knowledge_missing`，且 `write_request` 应为 false。契约自 WP92 冻结，WP95 无权修改；
  需下一工作包按契约修订流程处理。
- **gate 未绑定 run / prompt 身份**：capture 不含 `run_id`/prompt 身份，gate 只校验
  capture 字节、内嵌 case 与契约 SHA；runner 报告里的 `run_identity_sha256` 与
  `provider_identity` 未被消费。本次交付的 report/captures/review 自洽，但混用不同轮
  capture 可以 PASS，属潜在完整性缺口。
- **默认节奏不可复现**：契约默认 `min_interval_seconds=6.5`，runner 不处理 429/Retry-After；
  默认 12×1 会在 after-106 前被限流截断。本轮成功依赖手工 `--min-interval-s 15`。
- **after-103 仍是模型方差**：提示词对“无 SKU 的库存/价格”只说 may request tools，
  没有“必须澄清”的规则；第二次全量曾误判为 `unsupported_request`，最终运行才为 CLARIFY。
- **provenance 取自本地工作树**：runner import 并哈希本地 prompt registry，不核对服务进程
  实际加载的版本；服务未重启时报告可能声称 v4 而实际运行旧版。
- **HANDOFF 文案不扫写完成声明**：质量门禁只扫 assistant 文本，HANDOFF 用例的
  customer-visible system 文案不在 `WRITE_CLAIM_MARKERS` 检查范围内。
