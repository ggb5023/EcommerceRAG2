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

## 不要做

- 不修改真实 provider 配置、不打印密钥/DSN/cookie；
- 不把 mock profile 的 `provider_failed_search` 当 ANSWER；
- 不声称非 mock 事实可复制（commerce-sim facts 为 mock）；
- 不改 `scripts/check.sh`/contracts 棘轮/smoke 基线；
- 不提交 `scripts/review/`，不用 `git add .`。
