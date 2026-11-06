# 81 决策 0041 + 迁移 0058 + 服务轨道 scope 字段

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求售前/售后分轨与工单
- 前置：WP80 已关

## 目标

只落数据与契约，不改运行行为：

1. 决策 0041（售前/售后、工单生命周期、只读订单事实、订单号即凭证、0005 复制规则补充）；
2. 迁移 0058（只追加）：会话服务类型/订单号、两条执行链路 `facts_json`、`customer_ticket`；
3. proto `RequestContext` 增加 `service_type=29`、`customer_order_id=30` 并重新生成 pb；
4. 登记 data-ownership、decisions/INDEX、constraints（37→38）、迁移数 57→58。

## 步骤

1. `docs/decisions/0041-customer-service-lanes-and-tickets.md`；0005 补「引用或事实快照」；
2. `sql/migrations/0058_customer_service_ticket.sql`：
   - `customer_conversation`：`service_type` 默认 `pre_sales`、`order_id` 可空；
     CHECK 两轨互斥（售后必有订单、售前必无订单）；订单号格式
     `^[A-Z0-9][A-Z0-9_-]{3,39}$`；订单查询索引（部分索引）；
   - `customer_execution`、`conversation_execution`：`facts_json JSONB NOT NULL DEFAULT '[]'`；
   - `customer_ticket`：OPEN/ASSIGNED/RESOLVED/CLOSED、reason_code、assignee、
     时间戳；同会话活动工单部分唯一索引；队列/订单索引；只 GRANT `rag_v2_gateway`；
3. proto 两字段 + `buf lint/build/breaking` + 重新生成 pb（工具说明见 architecture）；
4. 文档数字同步；本包无行为变更。

## 验收命令

```bash
buf lint && buf build && buf breaking --against '.git#branch=main'
# 重新生成 pb：见 docs/architecture.md 的契约工具说明
# 从零迁移：写法见 docs/agent-entry.md §4
bash scripts/check.sh
```

## 不要做

- 不修改 0001–0057 任何迁移；不新增 SECURITY DEFINER；
- 不改顾客/员工运行逻辑、不改 smoke 基线、不改棘轮。
