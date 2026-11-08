# 94–96 残留任务并行协调包

- 状态：见 INDEX.md
- 依据：用户 2026-11-08「解决残留任务」
- 子包：[94](94-controlled-runtime-login.md)、[95](95-control-prompt-write-routing.md)、
  [96](96-adoption-lock-order-family.md)

## 文件所有权

| 轨 | 独占路径 |
| --- | --- |
| 94 | 受控 runtime 脚本、隔离栈脚本、`scripts/check.sh`、登录/受控 browser spec、共享 retry、`docs/development.md`、WP94 |
| 95 | 控制提示词/registry、prompt matrix、eval runner/provenance、eval 文档、WP95 |
| 96 | `go/internal/localauth`、`go/internal/domain/fact_resource.go`、`go/internal/customer/ai.go`、Python adoption 测试、decisions 0042/0043、`docs/contracts.md`、WP96 |
| 主 agent | INDEX/CURRENT/ui-only-acceptance、`check-contracts.py` 棘轮、专用 DB/env、运行 live/controlled/login、推送 |

## 并行约束

- worktree 只并行编码与 unit/offline 测试；任何 DB/live/controlled/login 运行由主 agent 串行。
- WP96 用 `ecr_v2_test_wp96` 或共享 test DB 的定向测试；WP95 用 `ecr_v2_test_wp95` + 26181；
  WP94 login 用 `ecr_v2_test_login` + 26091。不写 `/etc`，不停止 dev 栈。
- 不修改/提交本地未跟踪的审查目录；不用 `git add .`；提交身份固定。

## 集成顺序

1. WP96 → 本地验收 → `--ff-only` 合入 → 主 agent 单独提 `CONTRACTS_CROSS` 22。
2. WP95 → 离线 + 12×1 live + gate 12/12 → 合入 → 推送/CI。
3. WP94 → login ×2 + controlled-only ×2 + 普通 e2e → 合入 → 推送/CI。
4. 主 agent 更新 INDEX（91/92 视验收转 done）、ui-only-acceptance、本伞包结果。

## 结果（2026-11-08）

- WP96 已合入并推送（`14a9916` 为棘轮提交）：contracts 22/22、fast 44/0/0；
  A1 与窄订单族检测的 Go/Python 测试全绿。
- WP95 已合入（`9ac3a24`）：12×1 完成且 gate PASS 12/12（high-risk 6/6、ordinary 6/6），
  fast 44/0/0、smoke 47/0/0；模型单次波动与两次限流截断已如实记录在 WP95 文档。
- WP94 已 ff-only 合入（`0f23ee2`）：login 连续两次 7/7、controlled-only 连续两次 49/0/0、
  常规 e2e 56/6/0、全量第二次 61/1/0（第一次 60/1/1，`v2-lifecycle` 定向重跑 13/13）、
  `--db` 51/3/0；受控 runtime 已清理。
- INDEX：WP91/92/94/95/96 均为 done；WP91 的 login/受控复核由 WP94 关闭，WP92 由 WP95
  gate 关闭。
- 仍未做：suggestion 审查 kind（保持隐藏）；repeat=2 与知识/引用 case；`v2-restart`
  所需 `V2_RESTART_RUNTIME_ENV`。

## 约束

- WP92 在 WP95 gate 12/12 前保持 `implemented`；WP91 在 WP94 login/controlled 通过前保持 `implemented`。
- 新增决策 0042/0043 后同步 `docs/decisions/INDEX.md` 与 `docs/constraints.md` 的 38→40。
- 不新增迁移/SECURITY DEFINER；不改 smoke 基线；棘轮变更单独提交。
