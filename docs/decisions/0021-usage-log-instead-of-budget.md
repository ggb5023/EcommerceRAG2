# 0021 用 token 用量替代货币账本

- 状态：已采纳
- 日期：2026-10-27
- 取代：[0006](0006-provider-usage.md)

## 背景

本工程是商家自部署的轻量 RAG 项目：模型由商家自选，费用由商家与其模型平台结算。
此前用共享预算账本按"币种 + 价格表"做门禁，实测暴露出两类问题。

- 门禁口径是 `cap − Σ预留上界`，与实花脱节：一次批量评测实花 0.378 元，却永久占用 12.34 元额度；
  验收期被迫三次上调 cap（20 → 40 → 60 → 80）。
- 价格表追不上现实：调价、中转加价、免费额度都会让"算出来的钱"与账单不符；
  补下去只会让账本更复杂，精度并不提高。

## 决定

运行时只把每次模型调用的 token 用量写入 `usage_log`（tenant / provider / slot / model /
input_tokens / output_tokens / http_status / error_code / created_at），不做货币换算，
也不以金额做门禁。单次运行的失控保护由运行器的 `--max-requests` 与
`--max-requests-per-minute` 承担——它们是运行参数，不是账本。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 继续精算货币成本 | [LiteLLM model cost map](https://docs.litellm.ai/docs/proxy/model_cost_map) | 需要一份持续维护的价格表，且覆盖不了中转加价与免费额度，算出来仍不等于账单 |
| 保留金额作展示、token 做门禁 | 本工程 0021 之前的 `cap_micro` 口径 | 护栏还在，但仍展示金额，读者会把估算当账单——本工程已因此误判三次 |
| 引入网关统一计费 | [one-api / new-api](https://github.com/westgenesis/one-api) | 多一个必须运维的服务，与"商家自部署、轻量"的定位冲突 |
| **只记 token 用量**（采纳） | 主流 agent 工具（DSH、Claude Code 等）的做法 | — |

## 后果

- `usage_log` 由 Python 的 AI / ingest 角色写入，Go 网关只读；不授予 UPDATE / DELETE。
- 历史 `window` / `attempt` 两张表保留只读，不再被代码引用，不迁移、不换算。
- 报告口径改为「N 请求 / 输入 X tokens / 输出 Y tokens / 失败 M 次」，不出现任何货币数字。
- 花费的解释责任移交给商家的模型平台；本工程不再给出"花了多少钱"的结论。
- `usage_log.tenant_id` 允许 NULL，仅用于启动自检等无租户调用，聚合时单列 unattributed。
