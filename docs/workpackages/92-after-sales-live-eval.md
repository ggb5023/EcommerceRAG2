# 92 真实模型售后评测（12 个事实型 case ×1）

- 状态：见 INDEX.md
- 依据：WP90 后候选 2；bailian 凭据/网络已确认
- 前置：WP90 已关（`68685a2`）

## 目标

用 `v2_langchain_bailian` 跑 12 个事实型售后 case ×1，验证事实工具、订单绑定、不同订单澄清、
写操作转人工与 copy 规则。知识/引用与 repeat=2 等第一份报告出来后再决定。

## 范围

1. 新增冻结售后契约（source hashes、12 case、review 元数据）。
2. 新增 lane-aware HTTP/SSE runner（facts/usage 只读）、确定性 + review-bound gate 及 pytest。
3. 隔离栈脚本增加 `--profile`（默认 mock；bailian 时要求 `V2_PROVIDER_CONFIG`）；
   不改 identity，不改变现有 CONFIG_KEYS 过滤。
4. 事实型阶段用无已发布知识的隔离栈（仅启用 `shop-demo`），保证零证据；断言
   `is_mock=true` 事实下 `can_copy=false/blocked_reason=no_citable_evidence`、零引用、零非 mock 事实。
5. `eval/run.sh`/`eval/README.md`/`docs/metrics.md` 增补；报告写请求数/tokens/失败/模型与配置哈希/
   每 case behavior/copy/variance，`real_service_acceptance=false`，无货币金额。
6. 专用 DB `ecr_v2_wp92_eval`、`/tmp/ecr-wp92/test.env`、`ECR_ISOLATED_ROOT=/tmp/ecr-wp92/stack`，
   ports 26181；worktree agent 只跑离线测试，live 由主 agent 在 WP93 集成后跑。

## 验收

- `bash eval/run.sh offline` 与新增 pytest 通过。
- `--plan` 的 case 数/请求预算正确。
- 隔离栈 `--profile v2_langchain_bailian` 启动；12 case ×1 实跑生成报告；恢复 mock。
- provider smoke 输出 `CONFIG_PASS` / `LIVE_NOT_RUN`。

## 实施记录（worktree `wp92`）

- 契约：`eval/after-sales-service-contract.v1.json`，12 例 = 数据集 5 例
  `after-011`…`after-015` + authored 7 例 `after-101`…`after-107`。
  authored 为：隐式订单（ANSWER）、不同显式订单（CLARIFY）、缺 SKU（CLARIFY）、
  取消请求（HANDOFF）、退款请求（HANDOFF）、退款材料（HANDOFF）、改地址（HANDOFF）。
  原始任务列出 5+6=11 例，为满足"12 个事实型 case"新增 `after-107`（改地址写请求），
  这是相对任务文本的唯一组成偏差。
- 契约把 HANDOFF 的 `expected_states` 冻结为 `["DONE","FAILED"]`：Go 的
  pre-generation 拒绝路径落 `FAILED`，模型判定路径落 `DONE`，两者对顾客都是
  `WAITING_HUMAN` + `OPEN` 工单 + 无 assistant 消息。gate 在 `WAITING_HUMAN`
  时把空 behavior 视为 `HANDOFF`；provider 失败单独判负。
- runner：`eval/run_after_sales_service.py`（`--plan` / `--live` 显式，
  `--max-requests` 默认 12、硬上限 24；只读 REPEATABLE READ 快照
  `customer_execution`/`customer_message`/`customer_ticket`/`usage_log`）。
- gate：`eval/after_sales_service_quality.py`（capture sha256 绑定评审，5 个评审字段，
  `reviewer_kind=agent`、`human_review_completed=false`、`real_service_acceptance=false`）。
- 测试：`eval/test_after_sales_contract.py`、`test_after_sales_service.py`、
  `test_after_sales_service_quality.py`。
- 隔离栈：`scripts/dev/isolated-stack.sh` 增加 `--profile`
  （默认 `v2_langchain_mock`；`v2_langchain_bailian` 要求模板内有可读的
  `V2_PROVIDER_CONFIG`），identity 与 CONFIG_KEYS 过滤未改。
- 已知前置缺口：worktree 内 `/tmp/ecr-wp92/test.env` 不存在（任务描述称已创建），
  本 worktree 未创建 DB/env、未启动隔离栈、未跑 live；由主 agent 在 WP93 合入
  迁移 0061 后准备。
- live 前主 agent 必须：用专用 DB 建 0600 env（至少 `V2_GATEWAY_DSN`、`HTTP_ADDR`、
  `RAG_PROFILE=v2_langchain_bailian`、`V2_CUSTOMER_ORIGIN`、`V2_FACTS_BASE_URL`、
  `V2_FACTS_VERSION`、`V2_IDENTITY_USER`）；以
  `--profile v2_langchain_bailian --commerce-sim --ports 26181` 起栈；只开启
  `shop-demo` 顾客会话且**不发布知识**（runner preflight 对
  `customer_chat_disabled` / `published_knowledge_present` 失败关闭）；然后运行
  `run_after_sales_service.py --live ... --max-requests 12` 与
  `after_sales_service_quality.py`（评审需按 capture sha256 绑定）。

## 实施与验证（2026-11-07，主 agent）

- 12 个事实型 case ×1 实跑完成：6 ANSWER、2 CLARIFY、4 转人工（state=FAILED）；
  27 次 provider 请求，13,300 input tokens / 1,593 output tokens，失败 0。
- 质量门禁：9/12 通过，`review_bound_all=true`、`severe_safety_errors=0`，整体
  **NOT_PASSED**。失败项：after-015 只调用 `get_logistics`，缺 `get_order_status`；
  after-104/105（取消、退款）返回 `knowledge_missing`，期望 `unsupported_request`，
  工单原因也随之不符。这些是真实模型行为结果，未改契约或断言迁就通过。
- 实跑暴露并已修复两个 runner 缺陷：`GENERATION_INSTRUCTION` 应从 prompt registry
  导入；psycopg 会把 jsonb 解码为对象，runner 需同时接受对象与字符串。
- 报告/capture 在仓外 `/tmp/ecr-after-sales-report.json` 与
  `/tmp/ecr-after-sales-captures/`（0600）；未提交，`real_service_acceptance=false`。
- 后续：先修写请求的转人工 reason 映射，再决定 repeat=2 与知识/引用 case；
  非 mock 事实可复制仍只由单测覆盖。

## 不要做

- 不修改真实 provider 配置、不打印密钥/DSN/cookie；
- 不把 mock profile 的 `provider_failed_search` 当 ANSWER；
- 不声称非 mock 事实可复制（commerce-sim facts 为 mock）；
- 不改 `scripts/check.sh`/contracts 棘轮/smoke 基线；
- 不提交本地未跟踪的审查目录，不用 `git add .`。
