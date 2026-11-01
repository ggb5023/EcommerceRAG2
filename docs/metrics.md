# 指标口径（唯一出处）

> 权威等级：context（指标口径的唯一出处）　适用提交：7675ad2
> 本文件只定义指标，不实现。代码与页面必须引用本文件；改口径先改本文件，再改代码与页面。

## 人群

| 项 | 目标口径 | 当前代码行为 |
| --- | --- | --- |
| 人群 | `workspace`（工作台）/ `customer`（顾客通道）两套人群；所有指标先按人群过滤再聚合 | 只读 `turn`（workspace）；customer 通道尚未接入 |

## 指标与桶

| 指标 / 桶 | 目标口径 | 当前代码行为 |
| --- | --- | --- |
| `auto_answer_rate` | `DONE / (DONE + ASKING_abandoned + FAILED + CANCELLED)`，按人群过滤；`clarified` 不进分母 | `auto_rate = solved / scored_total`，其中 `scored_total = solved + clarified + abandoned + failed`；CANCELLED 并入 `failed`。**待改**：分母剔除 clarified，CANCELLED 单列 |
| `pending` | `ASKING` 未超过 10 分钟宽限期；单列，不进入任何分母 | 无此桶；ASKING 只按「同一 conversation 之后是否出现 DONE」分为 clarified / abandoned。**待改** |
| `clarified` | 必须用 `parent_turn_id` 关联澄清轮与被解决的后续轮 | 用「同一 `conversation_id` 之后出现 `DONE`」判断，不读 `parent_turn_id`。**待改** |
| `CANCELLED` | 单独成桶，不并入 `failed` | 并入 `failed`（`store_analytics` 的 `case "FAILED", "CANCELLED"`）。**待改** |
| `wrong_answer_rate` | 只能来自离线评测或人工标注；不得从 `DONE` 推断 | 无此指标；未从 `DONE` 推断 |

## 约束

- 本文件是指标口径的唯一出处；代码与页面引用本文件，不各自解释。
- 目标口径未定义的新指标，先在本文件补行，再实现。
- 不得用"页面看起来合理"倒推口径；口径变更必须先改本文件。
