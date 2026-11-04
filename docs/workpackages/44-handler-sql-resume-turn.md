# 44 P1-4 阶段 2：resumeTurn 的 SQL 移出 handler

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-4](../audits/2026-11-04-global-audit.md)、
> [0025 R2/R3](../decisions/0025-code-level-rules.md)、
> [0028](../decisions/0028-orchestration-boundary.md)、
> [data-ownership](../data-ownership.md)

## 目标

把 `resumeTurn` 中的 SQL 与事务编排移入 gateway store，新增不接
`*http.Request` 的 `resumeTurnExecution` 用例函数；handler 只做授权刷新、
路径/JSON/请求头解码、调用与响应。本阶段只处理 `resumeTurn`，不改响应码、
响应体、错误文案或 resume 幂等语义。

## 范围

- 新建 gateway store 文件 `store_turn_resume.go`，承载：
  - turn/conversation 的 `FOR UPDATE` 与 shop 校验；
  - 历史权限检查（复用 `conversationHistoryStale`）；
  - resume 幂等查询与旧 execution 查询；
  - ASKING execution 查询；
  - 新 execution 插入、turn 状态更新、幂等行与 message 插入。
- 新建 gateway 用例文件 `turn_resume.go`，提供
  `resumeTurnExecution(ctx, store, sessionData, input)`；参数为显式结构体，
  不接 `*http.Request`、不访问 `g.` 字段。
- `go/internal/gateway/turn_handlers.go` 的 `resumeTurn` 保持：
  `requireWriteRole` → `authority.Refresh` → `isV2Profile` 前置检查 → 路径/JSON/
  key 解码 → 用例调用 → 响应映射；handler ≤80 行。
- 保持既有分支逐字一致：`resume:` 前缀、202/200/409/404/403、响应字段、
  事务边界与 `is_mock`/`execution_no` 语义；既有测试断言不改。

## 验收命令

```bash
cd go && go build -o ../.local/v2-gateway ./cmd/gateway
cd go && go vet ./...
systemctl restart ecommerce-rag-dev
cd go && go test ./...

bash scripts/check.sh
bash scripts/check.sh --smoke
```

棘轮命令见 [WP43 验收命令](43-handler-sql-create-turn.md#验收命令)。

Go 重建后用 `strings` 验证新增 store/use-case 符号，重启后进程启动时间必须晚于
最后提交。curl 四组：成功 resume 202、同 key 重放 200 返回旧 execution、非 ASKING
409 `turn_not_asking`、不存在轮次 404；必要时先用 SQL 造一条 ASKING 轮次。

## 棘轮

`check-structure.py` 的 `HANDLER_SQL_BASELINE` 当前为 29；搬走 `resumeTurn` 的写 SQL
后按实测值精确下调，只许降。`createTurn` 已在 WP43 移出，本包不重复搬。

## 不做

- 不搬 customer/recovery 或其它 handler 的 SQL。
- 不改响应体、状态码、错误文案、`resume:` 幂等键算法或事务边界。
- 不改迁移、评测集、smoke 基线或其它棘轮/预算。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 后续顺序：customer service/ai → recovery 与剩余 SQL + 恢复演练。

## 未覆盖路径

- 不处理 `authorizeExecution`、events/getConversation/cancelTurn 等剩余 SELECT；
  其清单在交付时登记。
- 不新增 resumeTurn 的纯单测；用既有 Go 测试与四组 curl 证明行为不变。
- 不跑 `--db` / `--e2e` / `--all`；不验证真实模型路径。
