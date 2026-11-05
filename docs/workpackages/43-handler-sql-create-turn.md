# 43 P1-4 阶段 1：createTurn 的 SQL 移出 handler

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-4](../audits/2026-11-04-global-audit.md)、
> [0025 R2/R3](../decisions/0025-code-level-rules.md)、
> [0028](../decisions/0028-orchestration-boundary.md)、
> [data-ownership](../data-ownership.md)

## 目标

把 `createTurn` 中的 SQL 与事务编排移入 gateway store，新增不接
`*http.Request` 的 `startTurn` 用例函数；handler 只解码、调用、写响应。
本阶段只处理 `createTurn`，不改响应码、响应体、错误文案或幂等语义。

## 范围

- 新建 gateway store 文件 `store_turn_create.go`，承载：
  - 会话 `FOR UPDATE`、shop 校验、历史权限复核；
  - parent turn 查询（request_id / id / status / execution）；
  - 幂等查询；
  - active turn 检查；
  - turn_no 与 turn / conversation_execution / turn_idempotency / message 插入；
  - commit 后的 conversation title 更新。
- 新建 gateway 用例文件 `turn_create.go`，提供
  `startTurn(ctx, store, sessionData, input)`；参数为显式结构体，不接
  `*http.Request`，不访问 `g.` 字段。
- `go/internal/gateway/turn_handlers.go` 的 `createTurn` 只做路径/JSON/请求头解码，
  调用 `startTurn`，映射响应与启动异步执行；handler ≤80 行。
- 保持既有分支逐字一致：404、403、400、409 的 code/message，幂等重放 200，
  新 turn 202 响应字段；既有测试断言不改。

## 验收命令

```bash
cd go && go build -o ../.local/v2-gateway ./cmd/gateway
cd go && go vet ./...
systemctl restart ecommerce-rag-dev
cd go && go test ./...

python3 scripts/check/check-structure.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

Go 重建后用 `strings` 验证新增 store/usecase 符号，重启后进程启动时间必须晚于
最后提交。curl 四组：正常创建 202、同 key 重放 200、不同 payload 同 key 409
`idempotency_conflict`、会话有 active turn 时 409 `turn_in_progress`。

## 棘轮

`check-structure.py` 的 `HANDLER_SQL_BASELINE` 当前为 34；搬走 `createTurn` 的 5 条
写 SQL 后按实测值精确下调，只许降。若仍有未计入守卫的 SELECT，在本文件记录清单，
留到后续阶段，不在本包强行搬完。

## 不做

- 不搬 `resumeTurn`、customer、recovery 或其它 handler 的 SQL。
- 不改响应体、状态码、错误文案、幂等键算法或事务边界。
- 不改迁移、评测集、smoke 基线或其它棘轮/预算。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。
- 后续阶段顺序：resumeTurn → customer/recovery；本包只交付 createTurn。

## 未覆盖路径

- 本阶段不处理 `authorizeConversationHistory` 等被其它轮次 handler 复用的 SELECT；
  其剩余清单在交付时登记。
- 不新增 createTurn 的纯单测；用既有 Go 测试与四组 curl 证明行为不变。
- 不跑 `--db` / `--e2e` / `--all`；不验证真实模型路径。

## 实际交付

- 代码：`88c1361` 新增 `store_turn_create.go` 与 `turn_create.go`，`createTurn`
  改为只解码、调用 `startTurn`、映射响应并启动异步执行；handler 41 行，
  `startTurn` 75 行，均 ≤80。
- 事务内 SQL 全部移入 store：会话锁、历史权限、parent turn、幂等、active turn、
  turn_no、四张表插入、提交后的 conversation title 更新；`createTurn` 内剩余
  SELECT 为 0。
- 响应保持：正常 202、同 key 重放 200 且返回旧 request、不同 payload 同 key
  409 `idempotency_conflict`、active turn 409 `turn_in_progress`、不存在会话
  404 `not_found`。四组 curl 响应见交付汇报。
- 棘轮：`05b3572` 将 `HANDLER_SQL_BASELINE` 从 34 精确降到 29；
  `python3 scripts/check/check-structure.py` 输出
  `PASS structure: handler SQL 29/29`。
- 测试：`go test -count=1 ./...` 全部 ok；`go vet ./...` 通过；Go 重建并重启后
  进程启动时间晚于 `05b3572`；`strings` 命中 `startTurn`/`turnCreateStore`/
  `insertTurnAggregate` 共 6 处。
- curl 测试数据已清理：临时会话、turn、execution、SSE、消息、幂等与 trace 行
  均删除，残留计数为 0；未改 smoke 基线。

## 剩余 SELECT 清单

`createTurn` 内为 0。后续阶段仍需处理的非 store SELECT（按文件，含嵌套 SELECT）：

```text
turn_handlers.go 14（resumeTurn/getConversation/events/cancelTurn）
authorization.go 3（历史权限、执行权限）
result.go 5
support_suggestions.go 5
recovery.go 5
main.go 3
turn_persist.go 3
turn_route.go 3
identity.go 1
wire.go 1
metrics_http.go 1
```

后续顺序：resumeTurn → customer/recovery；本包不搬这些路径。
