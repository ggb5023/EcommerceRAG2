# 59 顾客端 can_copy / blocked_reason 统一（挂账 c）

- 状态：见 INDEX.md
- 依据：`docs/refactor-input.md` #17；决策 0005（可复制条件）；0028（终态/发布由 Go 决定）
- 前置：无

## 背景与问题

`customer_reply` 表已有 `can_copy` / `blocked_reason` 列，Go 侧也在持久化与响应里
透出（`go/internal/gateway/result.go:134`、`recovery.go:93` 完成帧、`turn_outcome.go:65`
citation_count），但两处口径漂移：

1. **Go 侧顾客规则过松**：`turn_generate.go:184 terminalCanCopy` 只判 `state=="DONE"`，
   不看引用数，也不看回复行为（CLARIFY 的追问不该复制）。
2. **前端自推口径**：WP20 顾客端移动版按「AI 回答 + 至少一条引用」自行推导复制可用性，
   未消费服务端字段——同一个事实（能不能复制）在两个地方各说各话，且顾客端 SSE/结果
   路径已有字段却无人消费。

目标：**把 0005 的四条可复制条件统一为唯一口径，Go 判定并持久化，前端只读服务端字段**。
0005 原文"只适用于员工工作台"的后缀与实际已上线的顾客端 UI（WP20）一致化——顾客端
UI 早就按"回答+引用"显示复制，本包把这条规则从前端推导挪进 Go，不改变顾客看到的
既有行为。

## 口径（唯一出处：本工作包；落库后同步 0005 后果段）

顾客回复可复制 ⇔ 同时满足：

1. 执行成功结束且非 mock；
2. 回复行为是 ANSWER（追问 CLARIFY / 转人工 HANDOFF 不可复制）；
3. 至少 1 条通过权限/发布状态/有效期/披露检查的引用（沿用现有 citation 计数口径，
   `turn_outcome.go` 的 citationCount 同一来源，不得另写一套）；
4. Go 判定并持久化到 `customer_reply.can_copy`，前端只读。

`blocked_reason` 取值（与员工侧 0005 一致）：无引用 → `没有资料依据`（沿用现有英文
枚举或与员工侧同值，以员工侧 `support_suggestions.go` 现有值为准）；追问 → `需要追问`；
其余保持现有 `incomplete` 等值不动。

## 步骤

1. **Go**：`terminalCanCopy` 改为接收「state + kind + citationCount」：
   DONE + ANSWER + citationCount≥1 才可复制；否则 false 且给出对应 `blocked_reason`。
   调用点（`turn_generate.go` 与恢复路径 `store_recovery.go`）同步传参。
2. **确认暴露面**：`/v1/turns/{requestID}`（result.go:134）、完成 SSE 帧
   （recovery.go:93）已带 `can_copy`/`blocked_reason`——核对顾客端对话恢复接口与
   事件流路径上所有出口字段一致（如 events 端点还缺则补）。
3. **前端**：`frontend/src/customer-api.ts` 与移动版组件删除「回答+引用」自推逻辑，
   改为消费服务端 `can_copy` / `blocked_reason`；复制按钮不可用时显示原因。
4. **测试**：
   - Go：`terminalCanCopy` 表驱动（DONE+ANSWER+1 引用=可复制；DONE+0 引用、CLARIFY、
     HANDOFF、FAILED、mock 均不可复制且 reason 正确）；
   - 前端：api 测试改为断言消费服务端字段（含缺字段时的兜底行为）；
   - contracts.md：把 `can_copy`/`blocked_reason` 语义并入现有「SSE 帧格式」行或
     新起一行（类型 `跨进程`），两侧测试列名填写本包用例。
5. **文档**：0005「后果」补一句顾客端同口径（一行，不另起决策）；INDEX 同步。

## 验收命令

```bash
bash scripts/check.sh                # 44 项 FAILED 0
cd go && go test ./internal/gateway/ -run "CanCopy|Copy" -v
cd frontend && npm test -- --run 2>&1 | tail -5
curl -s http://127.0.0.1:8080/v1/turns/<真实 requestID> | python3 -m json.tool | grep can_copy
```

前端验证用真实轮次：发起一次顾客问答（DONE 且有引用），确认 `can_copy=true`；
再触发一次 0 引用 HANDOFF→FAILED 轮次，确认 `can_copy=false` 且 reason 非空。

## 不要做

- 不新增迁移（列已存在）；不改 `customer_reply` 的 CHECK 约束；
- 不动员工侧建议回复的 0005 实现（`support_suggestions.go` 只作为 reason 取值的参照）；
- 不动 Python（判定归属 Go，0028）；
- 不改冻结评测集、不加接口、不新增依赖；
- 不改 CONTRACTS_* 棘轮（新增 ✅ 行若触发 NOTE，由主 agent 单独提交上调）。

## 汇报

按 CLAUDE.md 五节格式，附真实命令输出、真实轮次 curl 结果、前端测试输出、
contracts 行内容、0005 改动行号。
## 验证结果（2026-11-06）

- 纯规则与 gateway 调用：`go test ./internal/gateway/ -run "CanCopy|Copy" -v`
  的 `TestTerminalCanCopy`、`TestFinishGenerationPassesReplyBehavior` 全部通过；
  `TestPersistExecutionTerminalSetsCopyability` 的 `done_clarify_is_never_copyable` 子测试也通过。
- Go 全量：`go test -count=1 ./...` 全部 package 通过，`go vet ./...` 通过。
- 前端：`npm test -- --run` → 59 passed（含新增 `customer-api.test.ts`）；
  `npm run lint` 0 warning；`npm run build` 通过。
- `bash scripts/check.sh` → `PASSED 44 / UNCONFIGURED 0 / FAILED 0`。
- `bash scripts/check.sh --smoke` → `PASSED 47 / UNCONFIGURED 0 / FAILED 0`，五条 diff 逐字节一致。
- contracts：新增 `can_copy / blocked_reason` 行；`check-contracts` 打印
  `NOTE contracts: 跨进程 rose to 15; raise CONTRACTS_CROSS`，本轮不改棘轮。
- `0005-copyable-reply-policy.md` 后果段已补顾客端同口径一行。
- 真实 curl（dev，mock profile）：

```text
DONE  v2-1793889150748880103  status=DONE  can_copy=false blocked_reason=mock
FAILED v2-1793889151260381860 status=FAILED can_copy=false blocked_reason=incomplete
ASKING v2-1793889150438466138 status=ASKING can_copy=false blocked_reason=needs_clarification
```

- 可复制 true 的真实非 mock 历史轮次 `v2-1793629375054156504` 需要临时把
  `permission_revision` 调到当前值才能通过现有授权检查；curl 得到：

```text
status=DONE can_copy=true blocked_reason=""
```

  验证后已恢复原 revision `auth-65127aa814d86ddb877b361b`。dev 当前是 mock profile，
  所有新生成轮次都是 mock，无法在不改运行时 profile 的情况下自然得到 true。

## 未覆盖路径与限制

- **顾客对话快照与 `customer_reply` 的存储关系**：gateway 的 `persistExecutionTerminal`
  持久化 `customer_reply.can_copy/blocked_reason`；`/v1/customer/conversations/{id}` 的
  `publicMessage` 则从已持久化的 `customer_execution.state/behavior/is_mock` 和已验证引用
  现算 `can_copy` / `blocked_reason`，再返回给前端。`customer_reply` 只以外键关联
  gateway `conversation_execution`，与 customer_execution 没有列/外键关系；不新增迁移的
  约束下不能把 customer chat 的结果写入该表。
- `blocked_reason` 新增 `needs_clarification`（追问）和 `not_answer`（非 ANSWER 行为）；
  无引用继续用 `no_citable_evidence`，mock/incomplete 保持原值。
- dev 是 mock profile；true 的 curl 使用了历史非 mock 轮次并临时修正其权限版本，验证后已恢复。
- 不新增迁移、不改 customer_reply CHECK、不改 Python、不改员工侧建议回复实现。
- 远端 Actions、e2e、真实模型/OSS/Keycloak 不在本轮范围。
