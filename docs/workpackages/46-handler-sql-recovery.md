# 46 P1-4 阶段 4：recovery 与剩余 SQL 移出 handler

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-4](../audits/2026-11-04-global-audit.md)、
> [0025 R2/R3](../decisions/0025-code-level-rules.md)、
> [0028](../decisions/0028-orchestration-boundary.md)、
> [data-ownership](../data-ownership.md)

## 目标

把 gateway 剩余的写 SQL 移入 `store_*.go`，让 `recovery.go`、`turn_handlers.go`
cancel 分支及其它非 store 文件不再持有写 SQL；`HANDLER_SQL_BASELINE` 降到 0。
本阶段只做 SQL 搬迁与必要拆分，不改终态、响应、幂等或失败语义。

## 范围

- 新建 `store_recovery.go`：execution 终态 UPDATE、turn UPDATE、message/
  customer_reply 插入、SSE 追加、恢复扫描的锁定 SELECT；`persistExecutionTerminal`
  保留事务编排，只调用 store。
- `turn_handlers.go`：cancelTurn 的锁定 SELECT 与两条 UPDATE 移入 store；events/
  getConversation 的简单 SELECT 可暂留，但登记清单。
- `turn_persist.go`：sse_event/tool_call_trace 插入移入 store。
- `turn_generate.go`：citation 插入移入 store。
- `turn_retrieve.go`：evidence_snapshot 插入移入 store。
- `main.go`：createConversation 的 INSERT 移入 store。
- `support_suggestions.go`：建议 turn 插入和复制审计已在 `store_suggestion.go` /
  `admin.Store`，本包只确认没有新写 SQL；不重复建 store。
- 阶段函数 ≤80 行、不接 `*http.Request`；handler 只做解码/响应/调用。

## 验收命令

```bash
cd go && go build -o ../.local/v2-gateway ./cmd/gateway
cd go && go vet ./...
systemctl restart ecommerce-rag-dev
cd go && go test ./...

bash scripts/check.sh
bash scripts/check.sh --smoke
```

棘轮命令见 [WP43 验收命令](43-handler-sql-create-turn.md#验收命令)。Go 重建后
用 `strings` 验证新增 store 符号，重启后进程启动时间晚于最后提交。

curl：cancel 一条 EXECUTING 轮次 → 200/CANCELLED；suggestion 生成 + copy 再跑
一遍。recovery 逻辑用既有集成测试覆盖；若需要隔离库，如实写 `UNCONFIGURED`。

## 棘轮

`check-structure.py` 的 `HANDLER_SQL_BASELINE` 当前为 16；逐条确认剩余写 SQL 后
按实测精确降到 0。守卫只数写 SQL；剩余 SELECT 只登记，不强行一次搬完。

## 不做

- 不改响应体、状态码、错误文案、终态/fencing/幂等语义。
- 不改迁移、评测集、smoke 基线或其它棘轮/预算。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 后续：WP47 P1-7 恢复演练。

## 未覆盖路径

- 不新增 recovery 纯单测；用既有集成测试与 cancel/suggestion curl 证明行为不变。
- 不跑 `--db` / `--e2e` / `--all`；不验证真实模型路径。
- `events`/`getConversation` 等 SELECT 暂留，清单在交付时登记。
