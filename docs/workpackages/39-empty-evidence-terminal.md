# 39 P1-1 修复：零证据不得以 DONE 发布

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-1](../audits/2026-11-04-global-audit.md)、
> [0028](../decisions/0028-orchestration-boundary.md)、[contracts](../contracts.md)

## 目标

修复 legacy `customer_reply` 链路：Python 对零证据生成
`Decision(HANDOFF, knowledge_missing)`，Go 必须消费该 Decision，并把该轮终态定为
`FAILED/knowledge_missing`；不得把 canned 文案以 `DONE` 发布并计入 `solved`。
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
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（实现与验证后填写。）

## 未覆盖路径

（实现与验证后填写。）
