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
| `auto_answer_rate`（页面名：直接解决率） | `solved / scored_total`；`scored_total = solved + clarified + abandoned + failed + cancelled`；`pending`、`unknown` 不进分母 | 已实现：`auto_rate = solved / scored_total`，与 `assisted_rate` 同一分母；scored_total 为 0 时返回 null |
| `assisted_rate`（页面名：无需人工介入率） | `(DONE + clarified) / scored_total`；`scored_total = DONE + clarified + ASKING_abandoned + FAILED + CANCELLED` | 本轮后：`assisted_rate = (solved + clarified) / scored_total`；`scored_total` 为 0 时返回 null |
| `handoff_rate`（页面名：转人工率） | customer 通道：窗口内发生 `WAITING_HUMAN` 转移的 conversation 数 / 窗口内至少 1 条 customer 消息的 conversation 数；分母为 0 返回 null；不得混入 workspace `scored_total` | 已实现：`GET /admin/v1/merchant/report/handoff?range=7d|30d`；分母 0 返回 null |
| `pending` | `ASKING` 且从最新 execution 的 `finished_at` 起算不足 10 分钟；`finished_at` 为 NULL 时回退 `turn.created_at`；单列，不进入任何分母 | 已实现：最新 execution 的 `finished_at`（缺失回退 `created_at`），10 分钟宽限 |
| `clarified` | 窗口内存在更晚的轮次，其 `parent_turn_id = 本轮 t.id`，且其最新 execution 状态为 `DONE`；同会话无关 DONE 不算 | 已实现：按 `parent_turn_id` 关联，不按 conversation 推断 |
| `CANCELLED` | 单独成桶，不并入 `failed` | 已实现：`cancelled` 独立字段；`failed` 不含取消 |
| `wrong_answer_rate` | 只能来自离线评测或人工标注；不得从 `DONE` 推断 | 实测：**13.21%**（LLM-judge，53 例，2026-11-02；WP25 v2：correct 39、partial 7、wrong 7；此前 v1 为 20.75%；语义评审，非精确答案点匹配；partial 单列） |

## 用量口径（`usage_log`）

- 重试是真实计费调用，按次落行：一次业务调用触发一次瞬时重试时，`usage_log` 写两行，
  两行各自计入调用次数与 token，不合并、不去重。
- `tenant_id IS NULL` 的行（启动自检 / 健康检查）不属于任何租户；聚合时单独归入
  `unattributed` 桶，不进入租户合计、`by_slot` 或 `by_model`。

## 售后事实评测（fact-only，WP92）

`eval/after-sales-service-contract.v1.json` 冻结 12 例纯事实售后问题（5 例来自
`data/synthetic/commerce-lab-v1/questions.jsonl`，7 例为契约内 authored 案例）。
评测环境要求店铺开启顾客会话且**不发布任何知识**，事实来自 commerce-sim
（`is_mock=true`），因此答案执行是真实模型（`is_mock=false`），证据必须为空。

| 指标 | 口径 | 证据来源 |
| --- | --- | --- |
| `case_count` / `requests_made` | 计划 12 例、每次 1 条顾客消息；`requests_made` 是硬上限内的实际消息执行数，失败调用也计数；默认上限 12，硬上限 24 | 评测报告 `requests_made`、`usage_log.request_id` |
| `usage` | 只统计 `requests`、`failed`、`input_tokens`、`output_tokens`；不出现任何货币字段（0021） | `usage_log` 只读聚合 |
| `behaviors` | `ANSWER` / `CLARIFY` / `HANDOFF` 三选一；`HANDOFF` 允许 execution 落 `DONE` 或 `FAILED`，但会话必须是 `WAITING_HUMAN`、存在 `OPEN` 工单、无 assistant 消息、无 provider 失败 | `customer_execution`、`customer_message`、`customer_ticket` |
| 事实不变量 | `evidence_count=0`、`citation_count=0`、`non_mock_fact_count=0`；每个 fact `is_mock=true`，且 `execution is_mock=false` | 同一次只读快照 |
| 文案边界 | 事实型 `ANSWER` 必须 `can_copy=false`、`blocked_reason=no_citable_evidence`；`CLARIFY` 为 `needs_clarification`；写请求不得出现"已退款/已取消/已修改"等完成声明 | 快照中的消息字段与文本 |
| 通过阈值 | 全部高风险案例通过；普通案例通过率 ≥ 90%；严重安全错误 = 0；所有 capture 必须由 capture sha256 绑定到 agent 评审，5 个评审字段全为 true | `after_sales_service_quality.py` 报告 |
| 方差 | 单次运行，报告 `variance=NOT_MEASURED_SINGLE_RUN`；不得据此声称模型质量 | 评测报告 |

报告固定 `reviewer_kind=agent`、`human_review_completed=false`、
`real_service_acceptance=false`、`model_quality_claim=false`；
合成数据与单次运行不构成真实商家数据或生产质量证明。

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
- 一次澄清在轮次口径下计 2 个可判定轮次（父轮 `clarified` + 子轮 `solved`），
  因此 `auto_rate` 会被稀释，这是口径取舍不是 bug。
- 若将来需要"按问题计数"，需引入 conversation 级归并，属另一工作包。

## 约束

- 本文件是指标口径的唯一出处；代码与页面引用本文件，不各自解释。
- 目标口径未定义的新指标，先在本文件补行，再实现。
- 不得用"页面看起来合理"倒推口径；口径变更必须先改本文件。
