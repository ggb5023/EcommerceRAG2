# 指标口径（唯一出处）

> 权威等级：context（指标口径的唯一出处）　适用提交：9bd72a0（本轮重开 03）
> 本文件只定义指标，不实现。代码与页面必须引用本文件；改口径先改本文件，再改代码与页面。

## 人群

| 项 | 目标口径 | 当前代码行为 |
| --- | --- | --- |
| 人群 | `workspace`（工作台）/ `customer`（顾客通道）两套人群；所有指标先按人群过滤再聚合 | 只读 `turn`（workspace）；customer 通道尚未接入 |

## 指标与桶

| 指标 / 桶 | 目标口径 | 当前代码行为 |
| --- | --- | --- |
| `auto_answer_rate`（页面名：直接解决率） | `DONE / (DONE + ASKING_abandoned + FAILED + CANCELLED)`，按人群过滤；`clarified`、`pending`、`unknown` 不进分母 | 本轮后：`auto_rate = solved / (solved + abandoned + failed + cancelled)`；分母为 0 时返回 null |
| `assisted_rate`（页面名：无需人工介入率） | `(DONE + clarified) / scored_total`；`scored_total = DONE + clarified + ASKING_abandoned + FAILED + CANCELLED` | 本轮后：`assisted_rate = (solved + clarified) / scored_total`；`scored_total` 为 0 时返回 null |
| `pending` | `ASKING` 且从最新 execution 的 `finished_at` 起算不足 10 分钟；`finished_at` 为 NULL 时回退 `turn.created_at`；单列，不进入任何分母 | 已实现：最新 execution 的 `finished_at`（缺失回退 `created_at`），10 分钟宽限 |
| `clarified` | 窗口内存在更晚的轮次，其 `parent_turn_id = 本轮 t.id`，且其最新 execution 状态为 `DONE`；同会话无关 DONE 不算 | 已实现：按 `parent_turn_id` 关联，不按 conversation 推断 |
| `CANCELLED` | 单独成桶，不并入 `failed` | 已实现：`cancelled` 独立字段；`failed` 不含取消 |
| `wrong_answer_rate` | 只能来自离线评测或人工标注；不得从 `DONE` 推断 | 无此指标；未从 `DONE` 推断 |

## ASKING 的判定优先级

1. 存在 `parent_turn_id` 关联的更晚 DONE 子轮 → `clarified`；
2. 否则，仍在 10 分钟宽限内 → `pending`；
3. 否则 → `abandoned`。

## 边界与已承认的限制

- `/resume` 原地续接产生的是同一 `turn` 的新 execution，最终 `DONE` 计入 `solved`，
  不产生 `clarified`。这是设计，不是漏算。
- 子轮 `DONE` 若落在统计窗口之外（窗口下界或 `LIMIT 50000` 截断），父轮会被判为
  `abandoned` 而不是 `clarified`；本版承认该右删失误差。
- 两个比率的分母为 0 时返回 `null`，与真实的 0% 区分。
- `scored_total = solved + clarified + abandoned + failed + cancelled`；
  `turns_total = scored_total + unknown + pending`。

## 约束

- 本文件是指标口径的唯一出处；代码与页面引用本文件，不各自解释。
- 目标口径未定义的新指标，先在本文件补行，再实现。
- 不得用"页面看起来合理"倒推口径；口径变更必须先改本文件。
