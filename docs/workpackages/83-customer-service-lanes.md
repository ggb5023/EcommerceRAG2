# 83 顾客服务拆分 + 工单生命周期（Go）

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求售前/售后与工单
- 前置：WP81、WP82 已关

## 目标

1. 会话创建接受 `service_type`/`order_id`（默认售前；售后必须合法订单号）；
2. 首次转人工（售后）创建工单，之后 claim/release/requeue/回收/resolve/close 与
   会话状态在同一行锁内推进；工单状态只由 Go 决定；
3. 员工队列改为 union：有活动工单的售后会话 + 无工单的售前 `WAITING_HUMAN` 与
   `HUMAN` 会话；
4. 不改既有售前语义，五条 smoke 基线逐字节不变。

## 步骤

1. 服务层：`CreateConversation` 读取两项新字段；校验格式与互斥；
   幂等键复用但服务/订单不同 → 409 `conversation_idempotency_conflict`；
2. `customer/store*.go`：新增工单读写（SQL 只在此）；`queueTx` 首次转人工插入
   `customer_ticket` OPEN；claim→ASSIGNED；release/requeue/Reap/Recover→OPEN；
   新增 staff `POST .../resolve`→RESOLVED 且会话 CLOSED；close/resume-ai→CLOSED；
3. 员工队列 SQL 改 union；响应新增 `service_type`/`order_id`/`ticket_id`/
   `ticket_status`；
4. 路由与 openapi/error-taxonomy 同步；单测 + DB 集成测试。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./internal/customer ./internal/gateway ./internal/domain
bash scripts/check.sh --db
bash scripts/check.sh --smoke
```

## 不要做

- Python/管理端不决定工单状态；不新增迁移（0058 已就位）；
- 不为通过 smoke 改基线；不改棘轮。
