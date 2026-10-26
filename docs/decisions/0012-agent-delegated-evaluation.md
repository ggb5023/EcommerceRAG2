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

## 指标适用性

`exact_substring_v1` 不适用于生成本身会重写措辞的模型：它只说明“是否原样复述期望要点”，
不能作为答案质量指标。生成答案与期望要点词面不一致时，工具分类为
`generation_rewrite_mismatch`（重写不匹配），不是答错；不得据此宣称质量低或为了提升该数字
去改 prompt 要求复述要点。答案质量必须由语义评审（HTTP 轨 + agent）给出。

## 分轨口径

- batch 轨道（`bailian-batch-v1`）能回答“答案要点是否覆盖”，不能回答“交付时引用是否有效”：
  它没有 HTTP 交付字段（citations、customer_reply、delivered context），
  `build_human_review_sidecar.py --reviewer-kind agent` 会以
  `agent_review_requires_per_case_http_fields` 拒绝该轨道。
- 双 track 分数只能由 `v2-quality-e2e-v2` + `quality_gate.py` 产出；不得用 batch 指标冒充。
- 未跑 HTTP 轨道前，报告只能写“答案要点覆盖 X%，引用有效性未评估”，并注明
  `answer_quality_status=NOT_RUN`、评审方式 agent 但未产生逐条评审产物。

## 后果

- 评测结论只能表述为 agent 代理评审结果，不能替代人工复核或真实商家验收。
- 报告口径固定：`reviewer_kind=agent（delegated_agent_semantic_review_v1），
  human_review_completed=false，尚未人工复核`。
