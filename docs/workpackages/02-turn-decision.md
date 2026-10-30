# 02 决策理由落库（B6）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 来源：`docs/research/platform-borrowables.md` B6（采纳结论见 `docs/decisions/0030-borrowables-adoption.md`）

## 目标

每轮结束写一行 `turn_decision`，让"为什么选这条证据 / 为什么不用那条 / 为什么失败关闭"可查。

## 改动范围

- 迁移 `NNNN_turn_decision.sql`（编号在实现时取下一个可用值：0037–0038 已被稀疏占用）：
  `turn_decision(tenant_id, conversation_id, turn_id, chosen jsonb, rejected jsonb, strength text, failure_code text, confidence numeric, created_at)` + `INDEX (tenant_id, created_at DESC)`。
- `go/internal/admin/store_turn.go`（待建）（新）：`AppendTurnDecision(ctx, tx, decision)`。
- `go/internal/gateway/turn_outcome.go`（待建）（新）：三态（ASKING/DONE/FAILED）各写一次；由 `runTurn` 末尾调用。
- `docs/contracts.md` 加一行（同侧）：`turn 决策记录 | store_turn.go | —（同侧） | 单测 | ✅`。
- `docs/data-ownership.md`：声明 `turn_decision` 归 `admin/store_turn.go` 写。

## 不碰

- 不改任何现有判定与响应结构；不把 `turn_decision` 暴露给顾客端。
- 不给 Python 侧加写入（0028：落库归 Go）。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
cd go && go test ./internal/admin/ -run TurnDecision -v
# 跑一次 answer smoke 后断言：turn_decision 有行、chosen 非空、rejected 为 [] 而非 NULL
```


## 行为变更

零（纯新增写入）。

## 未覆盖路径

- FAILED 轮次的 `rejected` 可能为空（无证据可拒绝），测试需覆盖"空数组"与"NULL"的区分。
