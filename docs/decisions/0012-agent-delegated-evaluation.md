# 0012 评测评审由 agent 代理

- 状态：已采纳
- 日期：2026-10-26

## 背景

单人开发无法稳定提供人工评审吞吐；操作者明确授权用 agent 做语义评审。工具的
`quality_gate` 已要求 `reviewer_kind=agent`、`review_method=delegated_agent_semantic_review_v1`、
`human_review_completed=false` 与 `agent_attestation_sha256`，不需要改实现。

## 决定

- 范围：答案质量评审由 agent 代理；`reviewer_kind=agent`、
  `review_method=delegated_agent_semantic_review_v1`。
- 不可变：`human_review_completed` 恒为 `false`；任何报告不得写成“人工复核通过”，
  只能写“agent 代理评审，尚未人工复核”。
- 评审协议：
  1. 每条判定附 `chunk id` 与原文片段，不使用截断文本；
  2. 每条判定写明“这句话在什么情况下会判错”；
  3. 禁止整批复用同一句结论；重复率超过阈值即视为无效评审。
- 反向抽样：每批抽 10% 用反向提示词复核，并在报告记录
  `adversarial_recheck`（抽查数、推翻数、处理方式）。
- 恢复人工评审的条件：接入真实商家数据，或对外发布质量声明时，必须回到 human 评审。
- attestation：sidecar 记录 agent 标识与协议版本 `delegated_agent_semantic_review_v1`，
  报告引用其 sha256。

## 后果

- 评测结论只能表述为 agent 代理评审结果，不能替代人工复核或真实商家验收。
- 报告口径固定：`reviewer_kind=agent（delegated_agent_semantic_review_v1），
  human_review_completed=false，尚未人工复核`。
