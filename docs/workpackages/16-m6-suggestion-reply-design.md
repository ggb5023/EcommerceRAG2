# 16 M6 客服工作台建议回复——阶段 1 调研与设计

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：`ROADMAP.md` M6、`docs/product.md` 首版、`docs/decisions/0005-copyable-reply-policy.md`

## 目标

阶段 1 只做调研与设计，**不实现**。要回答三个问题，并留下可复核的调研与决策：

1. 建议回复的生成链路：复用现有 turn 引擎，还是独立轻量路径？
2. 引用卡片的数据来源：`evidence_snapshot` / `citation` 如何复用？与顾客端关系？
3. 一键复制的判定与审计：如何落实 0005 的 `can_copy`，复制动作记在哪里？

## 范围

M6 首版交互：

- 粘贴顾客问题 → 生成建议回复；
- 回复下方显示引用卡片（来源、版本、片段、引用序号）；
- 满足条件时一键复制，客服自行粘贴回电商平台；
- `/support` 队列中的顾客会话可以生成建议回复。

## 不做

- 不自动发送、不写顾客侧消息、不回写平台；
- 不做多轮建议、不做批量生成、不做模型微调；
- 不引入第三方客服 SaaS 依赖；
- 不实现代码、proto、迁移或前端页面；不做真实模型调用。

## 交付物

1. `docs/research/support-desk-suggestions.md`：成熟方案对照 + 本仓库现状盘点；
2. `docs/decisions/0031-m6-suggestion-reply.md`：生成链路、引用来源、复制判定/审计的提议；
3. 本文件：范围、验收、未覆盖；索引与 `CURRENT` 更新。

## 约束

- 0028：终态、权限、落库、事件顺序、失败分类在 Go；意图、检索、多跳、提示在 Python。
- 0005：复制必须由 Go 判定并持久化；前端只显示。只在 DONE、非 mock、ANSWER、
  至少一条通过权限/发布/有效期/披露检查的引用时允许复制。
- 0021：只记录 token 与次数，不出现货币金额。
- 0025 R1：同一事实只写一处；调研结论进 research，决策只写选择与理由。
- 外部候选按 `frontend-references` 的先例“名称即检索”，正文不写外链 URL。

## 验收命令

```bash
python3 scripts/check/check-docs.py
python3 scripts/check/check-docs-drift.py
python3 scripts/check/check-doc-paths.py
bash scripts/check.sh
```

## 验收口径

- 决策编号不撞车；`docs/decisions/INDEX.md` 有 0031；
- wp16、research、0031 三份文档互相可点达；
- 研究至少对照 2 个成熟方案，并各有“不采用理由”；
- 0031 的备选方案至少 2 个，含链接与不采用理由；
- 纯文档，`check.sh` 40 项全绿。

## 未覆盖路径

- 不验证真实模型下的建议质量、引用正确率或复制后的顾客反馈；
- 不确定平台 API（淘宝/抖店/拼多多）的发送能力，首版明确不做；
- 不设计批量、多轮、自动发送或多客服协作；
- 不评估第三方 SaaS 的采购/合规成本，只按产品定位排除依赖。
