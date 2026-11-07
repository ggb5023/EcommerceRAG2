# 91–93 三轨并行协调包

- 状态：见 INDEX.md
- 依据：用户 2026-11-07「并行解决三个候选问题」；三 worktree 并行、顺序集成
- 子包：[91](91-controlled-runtime-harness.md)、[92](92-after-sales-live-eval.md)、[93](93-residual-hardening.md)

## 文件所有权

| 轨 | 独占路径 |
| --- | --- |
| 91 | customer-runtime 新脚本、`scripts/ops/selfhost.py`、`scripts/check.sh`、`frontend/tests/keycloak.browser.mjs`、必要的受控 spec、`docs/development.md`、`docs/backup.md`、WP91 |
| 92 | 新 eval 契约/runner/gate/tests、`eval/run.sh`、`eval/README.md`、隔离栈脚本、`docs/metrics.md`、WP92 |
| 93 | 迁移 0061、Go customer/gateway/admin/domain、Python fixture 图与 fake 链、`EvidencePanel.vue`、`docs/contracts.md`、[0041](../decisions/0041-customer-service-lanes-and-tickets.md)、`docs/data-ownership.md`、`docs/agent-entry.md`、`docs/install.md`（迁移行）、WP93 |
| 主 agent | `INDEX.md`、`CURRENT`、`ui-only-acceptance.md`、`check-contracts.py` 棘轮提交、DB/env 创建、集成/推送 |

## 并行约束

- 三个 worktree 只并行编码与 unit/offline 测试；任何共享 DB 服务、Docker 栈、Playwright、
  `--db`/`--smoke`/live eval/controlled e2e 由主 agent 串行执行。
- 每轨使用专用 DB/env：WP93 `ecr_v2_wp93_test`，WP92 `ecr_v2_wp92_eval` + 26181，
  WP91 自包含 Compose project + 18443。不写 `/etc`，不重启 dev 栈。
- 不修改本地未跟踪的审查目录；不用 `git add .`；提交身份固定。

## 集成顺序

1. WP93（core + migration 0061）→ 本地验收 → `--ff-only` 合入 main → 应用 61 到 test/dev →
   db/smoke → 推送/CI。
2. WP92（eval + profile flag）→ 离线/live 12×1 → 合入 → 推送/CI。
3. WP91（harness）→ Phase 1 5 spec + Phase 2 restore + login → 合入 → 推送/CI。
4. 主 agent 更新 INDEX done、`ui-only-acceptance`、本伞包结果。

## 只记录/请示

WP80 adoption 竞态（A1 advisory lock 方案待安全确认）、suggestion 审查 kind（产品/隐私）、
裸标识扫描（误报决策）、staff 与 suggestion 复制策略变更。保留在
`docs/ui-only-acceptance.md` 已知限制。
