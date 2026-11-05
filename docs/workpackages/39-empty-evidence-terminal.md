# 39 P1-1 修复：零证据不得以 DONE 发布

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P1-1（2026-11-04 本机私有快照，未入库）、
> [0028](../decisions/0028-orchestration-boundary.md)、[contracts](../contracts.md)

## 目标

修复 legacy `customer_reply` 链路：Python 对零证据生成
`Decision(HANDOFF, knowledge_missing)`，Go 必须消费该 Decision，并把该轮终态定为
`FAILED` + `knowledge_missing`；不得把 canned 文案以 `DONE` 发布并计入 `solved`。
不改 customer 通道，不映射 CLARIFY。

## 测试先行

Go 单测使用伪造 Generate 流，不依赖真实 Python：

- Decision(HANDOFF, knowledge_missing) + 无 citation + Done → 终态
  `FAILED`、failure_code=`knowledge_missing`、不可复制；
- Decision(ANSWER) + citation + Done → 终态 `DONE`（回归保护）；
- generateAnswer 必须记录 Decision；忽略 Decision 的旧实现对 HANDOFF 用例会失败；
- outcome 纯测试：FAILED 计 `failed`、不计 `solved`，零证据轮次不出现在 solved。

## Python 侧

`python/app/rpc/rag.py:Generate` 当前只对 `response_purpose == 'customer_chat'`
发送 Decision；改为 `customer_chat` 与 `customer_reply` 都发送 graph 的
`reply_decision`。ANSWER/CLARIFY/HANDOFF 三态照传，未知 behavior 仍按 HANDOFF 兜底，
不新增错误码。

## Go 侧

- `generateAnswer` 消费 `GenerateResponse_Decision`，记录 `ReplyDecision`；
- `runTurn` 在生成成功后按 behavior 决定终态：
  - HANDOFF → `finish("FAILED", reason_code, answer)`，保留 answer 文本；
  - ANSWER → `finish("DONE", "", answer)`；
  - CLARIFY → 本批不改终态，仅记录（映射 ASKING 另立决策）；
- 不改 customer 通道。

## 契约与只读核对

`docs/contracts.md` 新增/更新「generate decision」跨进程行，写明 Go 消费点、
Python 发送点及两侧测试文件，状态 ✅。

只读 SQL：统计 `state=DONE` 且零 citation 的 turn 数（dev 预期 0）；若大于 0，
列出前 5 条并停止，不回填、不改数据。

## 验收命令

```bash
go build -o .local/v2-gateway ./cmd/gateway
strings .local/v2-gateway | grep -c 'GenerateResponse_Decision'
cd go && go test ./...
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：重启 gateway+ai 且启动时间晚于最后提交；实跑语料外问题，断言
`state=FAILED`、`code=knowledge_missing`；outcomes `solved` 不增、`failed` 增；
gaps 出现该问题；契约守卫输出。

## 不做

- 不映射 CLARIFY；不改 customer 通道、检索、提示词或评测集。
- 不回填历史数据；不改 `can_copy` 协议；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- 新增 Go 伪造 Generate 流测试：HANDOFF + 无 citation + Done 映射为
  `FAILED`/`knowledge_missing`，ANSWER + citation + Done 保持 `DONE`，并断言
  Decision 不得被静默丢弃；outcome 测试断言 FAILED 计 `failed`、不计 `solved`。
- Python `Generate` 对 `customer_reply` 与 `customer_chat` 都发送
  `reply_decision`；`customer_reply` 的 HANDOFF 用例通过。
- Go `generateAnswer` 记录 `Decision`，`runTurn` 使用 `generationTerminal`：
  HANDOFF → `FAILED` + reason_code，ANSWER → `DONE`，CLARIFY 保持 `DONE` 仅记录。
  同时修复 mock DONE 的 `can_copy` 回归：数据库 `customer_reply_check` 不允许
  mock 可复制。
- 契约：`docs/contracts.md` 新增 `generate decision` 行；`CONTRACTS_CROSS`
  11 → 12 单独提交，`check-contracts.py` PASS。
- 447 只读核对：

```text
 id  | status | state
-----+--------+-------
 447 | DONE   | 
```

  其 `turn_decision.state` 为 NULL，不符合 `decision.state=DONE`，归 unknown，
  不计 solved。带 `decision.state=DONE` 且零 citation 的真实轮次查询结果：

```text
 real_done_zero_citation
-------------------------
                       0
```

  未回填、未删除、未修改 447 或任何历史行。

- 已跑完整 Go 测试、Python 测试、`check.sh` 41 和 `--smoke` 45；五条 smoke
  逐字节一致，未重采基线。
- 对当前 dev 语料尝试语料外问题 `ZXQV-998877-UNKNOWN`：dense 检索仍返回
  `db-chunk-8`（w2-delivery-c），最终是 `DONE`，无法在该 dev 数据上触发
  “零证据”路径；因此 §6.2 的 FAILED/outcomes/gaps 现场断言未能完成。该限制
  不影响伪造 Generate 流的 P1-1 单测，但本轮不声称已完成实链路零证据验收。

## 未覆盖路径

- **CLARIFY 未映射**：`generationTerminal` 对 CLARIFY 仍返回 DONE，映射 ASKING
  需要另立决策。
- **can_copy 死协议**：Python 仍发送 `can_copy` 事件，Go 未消费；本轮只修复了
  FAILED/mock 不可复制的持久化约束。
- **447 夹具未回填**：确认是 WP17 手工 curl 验证夹具（request_id
  `wp17-final-nocite-*`），decision.state 为 NULL；按裁定未回填、未删除、未改库。
- **实链路零证据**：当前 dev 只有 active `w2-delivery-c`，dense 检索对任意 query
  返回该 chunk，无法现场构造 Python `HANDOFF`；未获得 outcomes before/after 或
  gaps 命中证据，未声称该项通过。
- 未改 customer 通道、评测集、检索、提示词或其他棘轮；未 push。

