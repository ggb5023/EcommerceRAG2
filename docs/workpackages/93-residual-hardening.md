# 93 深层残留硬化（推荐子集）

- 状态：见 INDEX.md
- 依据：WP90 后候选 3；用户确认推荐子集
- 前置：WP90 已关（`68685a2`）

## 目标

1. **staff behavior 持久化**：迁移 0061 增加 nullable `conversation_execution.behavior`；
   终态写入；管理台读取真实值，legacy NULL 才回退状态推断。修 CLARIFY→DONE 被显示为 ANSWER。
2. **fixture 图消费 facts**：fixture 图的生成节点与 fake 链使用 facts，
   事实型回答产出 `reply_decision=ANSWER/grounded`；无 facts/evidence 时 HANDOFF；不产出
    Python `state`/`terminal`。
3. **显式订单号守卫**：Go domain 纯函数识别“订单号/订单编号/order id”后的字面量；任何不等于
   绑定订单的值 → `order_clarification`；不做裸标识扫描。
4. **contracts facts 行**：`docs/contracts.md` 增加 Go/Python 事实工具/资源绑定与快照复验行，
   两侧测试名齐全；棘轮 `CONTRACTS_CROSS` 21 由主 agent 单独提交。
5. **死代码清理**：删除无生产调用的 `terminalCanCopy` 与未引用的 `EvidencePanel.vue`；
   保留/补充 staff user 事实可复制、suggestion facts 不可复制、mock facts 不可复制测试。

## 验收

- Go targeted + `go test ./...`、`go vet`；Python 全量；frontend tests/lint/build。
- 专用 DB `ecr_v2_wp93_test` 上 `bash scripts/check.sh --db`；from-zero 61。
- `check-contracts.py` 先 NOTE 21；主 agent 棘轮提交后 PASS 21/21。
- `bash scripts/check.sh` 44/0/0；`--smoke` 逐字节不变。

## 集成结果（2026-11-07，主 agent）

- 0061 已应用到 dev/test，from-zero 61 PASS；`check.sh` 44/0/0、`--db` 52/2/0（隔离栈运行时）、
  `--smoke` 47/0/0。
- contracts 行入主干，`CONTRACTS_CROSS` 在单独提交 `aa50580` 上调到 21，PASS 21/21。
- 新增行为/订单守卫/fixture facts/死代码清理的定向测试全绿；smoke 逐字节未变。
- 仍未做：WP80 竞态、suggestion 审查 kind、裸标识扫描。

## 不要做

- 不改 0058–0060 或任何已执行迁移；不新增 SECURITY DEFINER；
- 不做 WP80 竞态、suggestion 审查 kind、裸标识扫描（记录并请示）；
- 不改变 staff/suggestion 复制策略；
- 不重采 smoke 基线、不改断言迁就代码；
- 不提交本地未跟踪的审查目录，不用 `git add .`。
