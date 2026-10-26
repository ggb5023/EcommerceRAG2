# 0014 质量门原因区分 failed 与 not_measured

- 状态：已采纳
- 日期：2026-10-26

## 背景

`quality_gate.py` 把“未测量”和“真实失败”混在同一个 `gate_reasons` 列表里：
batch 轨道没有 HTTP 引用字段，引用有效性必然是 `None`；component 轨没有逐条评审，
`semantic_review_incomplete` 也会出现。二者被当成失败，会误读质量结论。

## 决定

- `gate_reasons` 改为 `{failed: [...], not_measured: [...]}`：
  - `citation_validity_below_0.95` 在 citation 率为 `None` 时归入 `not_measured`，低于 0.95 才归 `failed`；
  - `semantic_review_incomplete`、缺失的 safety/e2e 证据、e2e 评审缺失归入 `not_measured`；
  - 高风险用例失败、安全错误、执行/回放失败、证据/绑定无效等仍归 `failed`。
- `answer_quality_status`：有 `failed` → `NOT_PASSED`；无 `failed` 但有 `not_measured` →
  `NOT_MEASURED`；两者都无 → `PASS`。component 状态只看 component 轨的 `failed`，
  component 的未测量不再拖累 e2e 完成度判定。

## 事实意图缺标识时的回退（行为修正）

`factual`/`realtime_business` 意图在缺少 SKU/订单号时不再直接澄清：先执行语料检索；
命中已发布快照 → 按“快照值 + 时效声明（来自快照/不能保证当前状态）”作答并附引用；
无证据 → 维持澄清；拿到标识后仍走 facts 接口，快照只作兜底，不得冒充实时事实。
这是产品行为修正，不改变期望要点、冻结集或阈值。

## 后果

- batch 无 HTTP 引用字段不再被写成“引用失败”；component 未评审不再阻挡 e2e 语义轨完成。
- 真实高风险失败（本轮的 3 条事实用例）仍是 `failed`，结论保持 `NOT_PASSED`。
