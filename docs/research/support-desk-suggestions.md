# 客服工作台建议回复：成熟方案对照与本仓库盘点

> 权威等级：context　日期：2026-11-02　工作包：[16](../workpackages/16-m6-suggestion-reply-design.md)
> 结论落在 [0031](../decisions/0031-m6-suggestion-reply.md)。外部产品按名称检索官方文档，
> 正文不写外链 URL（沿用 [frontend-references](frontend-references.md) 的约定）。

## 1. 结论摘要

成熟产品的共同交互是：**建议出现在会话旁边，带可核对的来源，客服人工确认后才发送**。
M6 只借鉴这个交互骨架，不引入第三方平台、不自动发送。生成链路应复用 Go turn 引擎的
权限/终态/证据快照，引用卡片直接读 `evidence_snapshot`/`citation`，复制由 Go 按
[0005](../decisions/0005-copyable-reply-policy.md) 判定并审计。

## 2. 成熟方案对照

| 候选（检索名） | 可借鉴的交互 | 不采用为依赖的理由 |
| --- | --- | --- |
| Intercom Fin | 收件箱侧建议、知识来源提示、人工确认后发送 | SaaS 与工单体系绑定；本仓库要求自部署、商家自带资料，不引入平台依赖 |
| Zendesk AI | 工单上下文里的建议回复、宏/知识文章联想、人工编辑 | 依赖 Zendesk 工单与账号体系；会把发送与工单状态耦合进首版 |
| Salesforce Service Cloud Einstein | 案例页的建议、知识文章卡片、可信来源标注 | 企业套件过重；首版只需要“粘贴问题→建议→复制”，不需要 CRM 对象模型 |
| Freshdesk Freddy | 客服副驾式建议与知识联想 | 同样是 SaaS 工单副驾；不满足自部署与“不接平台 API”的边界 |

说明：0030 已对 Sierra、Decagon、Intercom Fin、Salesforce Agentforce 做过“只抄约束、
不抄功能”的结论；本表只解析 M6 的**工作台交互**，不重复平台能力评估。

## 3. 抄什么 / 不抄什么

**抄**

- 会话/问题与建议并排，建议不自动进入发送框；
- 建议正文 + 紧凑引用卡片（文档、版本、片段、引用序号）；
- 复制按钮有明确的可用/不可用状态与原因；
- 客服是最后发送者，系统只给建议。

**不抄**

- 自动发送、自动接管、自动关闭会话；
- 平台工单同步、平台账号/席位体系；
- 自动训练或把完整会话存档用于训练；
- 可视化 workflow builder、按结果计费。

## 4. 本仓库现状盘点

| 面 | 现状 | 位置 | M6 能复用 | 缺口 |
| --- | --- | --- | --- | --- |
| 客服队列 | 队列只含 `WAITING_HUMAN`/本人 `HUMAN`；有 claim/release/close/resume-ai/requeue 与人工消息 | `go/internal/customer/service.go`（`StaffRoutes`/`staffAction`）、`frontend/src/SupportDesk.vue` | 队列、领取、人工消息、SSE 通道 | 没有“生成建议”动作；没有建议卡片 |
| 员工 turn 引擎 | `POST /v1/conversations`、`/turns`、SSE、`GET /v1/turns/{id}`；状态机与证据落库在 Go | `go/internal/gateway/routes_customer.go`、`turn_*.go`、`result.go` | 阶段编排、终态、SSE、`evidence_snapshot`/`citation`、结果组装 | 没有“建议”执行类型；turn 默认面向员工会话而非 `/support` 顾客会话 |
| `response_purpose` | 枚举 `internal` / `customer_reply` / `customer_chat`；`customer_reply` 走顾客披露过滤 | `python/app/graph/service.py`、`go/internal/gateway/turn_retrieve.go` | `customer_reply` 可作为“可发给顾客”的建议用途 | 需要明确 M6 建议必须用哪个 purpose，不能误用 `internal` 绕过披露 |
| 复制判定 | `customer_reply.can_copy` 有列与 `blocked_reason`；约束只禁 `is_mock AND can_copy`；代码路径当前写死 `false` | `sql/migrations/0002_m1_slice.sql`、`go/internal/gateway/recovery.go`、`result.go` | 表结构、结果字段、0005 口径 | 缺少按 0005 计算 `can_copy` 的 Go 判定；缺少“DONE + 非 mock + ANSWER + 至少一条引用”的测试 |
| 引用快照 | `evidence_snapshot` 按 execution 保存证据；`citation` 保存 `evidence_id`/`citation_index`；`executionResult` 统一组装 | `sql/migrations/0002_m1_slice.sql`、`go/internal/gateway/result.go` | 卡片直接读快照；保持版本/序号稳定；复用同一组装器 | 没有面向建议的视图字段（标题/来源路径/是否外部可见）统一约定 |
| 顾客端引用 | `customer_message.citations_json` 由顾客执行写入，顾客端按 `citations/{message}` 读取 | `go/internal/customer/store.go`、`service.go` | 可复用同一引用视图形状 | 员工建议不能写 `customer_message`（不自动发送） |
| 用量 | provider 调用写 `usage_log`，只记 token/次数 | `python/app/providers/usage.py`、`0021` | 建议生成沿用同一 usage 路径 | 需要确认建议请求也走同一 provider/usage 配置 |
| 审计 | `admin_audit` 记录商家管理动作，含 actor/resource/result | `go/internal/admin/store_audit.go`、`0010` | 可记录“复制建议”动作 | 目前没有复制动作或对应 action；不能把正文写进审计 |

## 5. 复用与缺口清单

**可直接复用**

- Go 的 scope 签发与校验、turn 阶段函数、终态事务、SSE 帧；
- `evidence_snapshot` / `citation` 及其读取组装；
- `customer_reply` 结果字段（`text_plain`/`is_mock`/`can_copy`/`blocked_reason`）；
- `usage_log`（token/次数，0021）。

**必须新增（阶段 2）**

1. 员工建议的 Go 入口：支持粘贴问题与 `/support` 会话两种来源；
2. 建议执行的终态与证据快照（不得写顾客消息）；
3. 按 0005 计算并持久化 `can_copy`；
4. 复制动作的服务端授权与审计；
5. 前端建议面板、引用卡片、复制状态；
6. 契约表新增“建议结果/引用”行，并有 Go+前端或 Go+Python 两侧测试；
7. 如 0005 的约束需要加强，追加 checksum 迁移（不修改已执行迁移）。

## 6. 对 0031 的输入

- 生成链路优先复用 Go turn 引擎，不新增 Python 直连路径；
- 建议必须用 `customer_reply` 披露口径，避免员工 `internal` 权限把内部资料带进可复制回复；
- 引用卡片以 `evidence_snapshot`/`citation` 为唯一来源；
- 复制必须在 Go 侧复核 0005 并审计，不能只由前端启用按钮；
- 不自动发送、不写顾客消息、不做多轮建议。

## 7. 检索入口

上述外部产品只写名称：Intercom Fin、Zendesk AI、Salesforce Service Cloud Einstein、
Freshdesk Freddy。需要细节时按名称检索官方文档；本仓库不保存外链，避免文档出现
不存在的路径。内部依据见 [0031](../decisions/0031-m6-suggestion-reply.md)、
[0005](../decisions/0005-copyable-reply-policy.md)、[0028](../decisions/0028-orchestration-boundary.md)。
