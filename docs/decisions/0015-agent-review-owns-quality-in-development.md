# 0015 开发阶段由 agent 评审承担质量门槛

- 状态：已采纳
- 日期：2026-10-26

## 决定

开发阶段（P1–P4 全程）以 agent 评审承担质量门槛：`reviewer_kind=agent`、
`review_method=delegated_agent_semantic_review_v1` 的评审通过，即视为质量门槛达成；
不安排人工复核，也不因缺少人工复核阻塞任何阶段推进。

## 不变量

- `human_review_completed` 恒为 `false`。
- 不得声称 `real_service_acceptance=true`。
- 报告写“agent 评审通过”，不写“人工复核通过”。

## 商家阶段

商家真实使用时采用“agent 辅助审查 + 人工只审必要部分”，必要人工清单见
[0013](0013-merchant-delegated-agent-review.md)；本决策不重复商家阶段规则。

## 理由

单人开发下，agent 在覆盖率和一致性上优于开发者自测；评审产物包含逐条证据与
attestation，将来需要人工增量补做时可以直接基于现有产物复核，不必重跑评测。

## 后果

- 质量门继续要求 `reviewer_kind=agent` 且 `human_review_completed=false`，无需改代码。
- 接入真实商家数据/试点、对外发布质量声明或写入交付物、以质量结论作为上线或付款依据时，
  按 0013 启动必要人工审查。
