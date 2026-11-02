# 0031 M6 客服建议回复：生成链路、引用与复制

- 状态：已采纳
- 日期：2026-11-02
- 关联：[0005](0005-copyable-reply-policy.md)（复制条件）、[0028](0028-orchestration-boundary.md)（编排边界）、
  [0021](0021-usage-log-instead-of-budget.md)（只记 token）、[0030](0030-borrowables-adoption.md)（平台结论）、
  [调研](../research/support-desk-suggestions.md)、[工作包 16](../workpackages/16-m6-suggestion-reply-design.md)

## 背景

M6 首版要让客服把顾客问题粘贴到工作台，拿到带出处的建议回复，并一键复制回电商平台。
现有 `/support` 队列只支持领取、人工消息和状态切换；员工 turn 引擎已有权限、终态、
SSE、`evidence_snapshot`/`citation`，但 `customer_reply.can_copy` 仍是写死的 false。
本决策只定阶段 2 的实现边界，不写代码。

## 决定

### 1. 生成链路：复用 Go turn 引擎，不新增 Python 直连路径

- 新增两个 Go 入口：
  - `POST /v1/support/suggestions`：粘贴问题生成建议；
  - `POST /v1/support/conversations/{id}/suggestions`：从 `/support` 会话的最后一条
    顾客消息生成建议。
- 两个入口复用 turn 引擎的阶段函数（Understand/Search/Generate）、scope 签发/校验、
  SSE 与终态事务；**不复制**一套 Python 调用链。
- 建议执行使用 `response_purpose=customer_reply`，确保走顾客披露过滤；禁止用
  `internal` 生成可复制建议，避免内部资料进入顾客回复。
- 建议执行不写 `customer_message`，也不改变 `customer_conversation` 状态；只有客服
  显式点击现有“发送人工回复”才写顾客侧消息。
- 为了容纳“无 turn 的粘贴建议”，阶段 2 可以使用独立的 suggestion 归属（新 checksum
  迁移）或隐藏的 staff suggestion conversation；具体 schema 在阶段 2 工作包给出，
  但**不得**把建议伪装成顾客消息或自动 turn。

### 2. 引用卡片：只读 `evidence_snapshot` + `citation`

- 卡片数据来自本次建议执行的 `evidence_snapshot`（content、document/version、
  disclosure、citation_index）与 `citation`（引用序号）；不直接用 Python 的临时
  candidates。
- 复用 `go/internal/gateway/result.go:executionResult` 的授权过滤与视图组装；
  员工端和顾客端只共享**引用视图形状**，不共享发送动作。
- 卡片必须显示文档标识、版本、片段与引用序号；不显示内部路径、向量或 provider 回执。
- 建议生成使用 `usage_log` 记录 token/次数；不新增货币账本（0021）。

### 3. 复制：Go 判定 + 服务端审计

- Go 在建议终态事务中按 0005 计算并持久化 `can_copy` 与 `blocked_reason`：
  DONE、非 mock、行为 ANSWER、至少一条通过权限/发布/有效期/披露检查的引用。
  前端只显示，不自行把按钮设为可用。
- 新增 `POST /v1/support/suggestions/{request_id}/copy`：
  - 重新校验当前 scope、`can_copy` 和引用集合；
  - 成功时返回正文，并写 `admin_audit`：`action=support.suggestion.copy`、
    `resource_type=execution`、`resource_id=request_id`、`result=success`、
    metadata 只含 `reply_sha256`、`citation_count`、`can_copy_reason`；
  - 拒绝时写 `result=denied`，不返回正文；
  - 审计**不写正文**，避免把顾客对话复制进管理审计。
- 复制动作仍由客服在平台侧完成；本决策不新增平台 API 或自动发送。

### 4. 明确不做

- 不自动发送、不替客服点击复制、不写顾客侧消息；
- 不做多轮建议、批量建议、主动追问生成；
- 不对接淘宝/抖店/拼多多 API，不训练模型；
- 不引入第三方客服 SaaS 作为运行时依赖。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 复用 Go turn 引擎，新增建议入口（采纳） | [0028](0028-orchestration-boundary.md) | 终态、权限、证据快照和 SSE 已有单一出处；建议只需换入口与输出形态 |
| 独立 Go 轻量路径，直接调用 Python Understand/Search/Generate | [customer/ai.go](../../go/internal/customer/ai.go)、[0028](0028-orchestration-boundary.md) | 会复制终态、授权、证据落库与失败分类，违反 0028 与 0025 R1；顾客端路径正是这种重复的边界 |
| 前端直接调用 Python 生成建议 | [0026](0026-proxy-fronted-deployment.md)、[0025](0025-code-level-rules.md) | 前端绕过 Go 的 scope/终态/审计；现有边界守卫也禁止前端直连 Python |
| 引入 Intercom Fin / Zendesk AI / Salesforce Einstein 的 agent-assist 作为依赖 | [调研](../research/support-desk-suggestions.md)、[0030](0030-borrowables-adoption.md) | SaaS 与平台绑定，违背自部署与“不接平台 API”；只借鉴侧栏建议/引用/人工确认交互 |
| 复用顾客聊天路径并自动发送建议 | [0005](0005-copyable-reply-policy.md) | 0005 要求客服是最后发送者；自动发送没有人工确认，且复制条件无法在发送前审计 |

## 后果

- 阶段 2 需要：建议端点与终态/证据归属、`can_copy` 计算、复制审计、前端建议面板、
  契约表新增“建议结果/引用”行及两侧测试；如 schema 需要，追加 checksum 迁移。
- 0005 的“新迁移调整约束”在阶段 2 落地；不得修改已执行迁移。
- 建议链路与 turn 链路共享终态/证据代码，新增测试必须覆盖 mock/非 DONE/ANSWER/
  引用为空/scope 变化/复制拒绝等失败路径。
- 真实模型质量仍需按既有 `--live` 流程单独评测；本决策不承诺建议质量。
