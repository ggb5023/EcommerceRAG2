# 45 P1-4 阶段 3：customer service/ai 的 SQL 移出 handler

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-4](../audits/2026-11-04-global-audit.md)、
> [0025 R2/R3](../decisions/0025-code-level-rules.md)、
> [0028](../decisions/0028-orchestration-boundary.md)、
> [data-ownership](../data-ownership.md)

## 目标

把 customer `service.go` / `ai.go` 中的 SQL 移入现有 `store.go`，让 HTTP
handler 只做解码、调用、响应；保持顾客状态机、epoch/租约/fencing、幂等
replay 和终态语义不变。本阶段只处理 customer service/ai，不搬
`recovery` / `authorization`。

## 范围

- 扩展 `go/internal/customer/store.go`，不新建重复 store 文件，承载：
  - 会话解析与 idle 续期、公开店铺/建会话/建 conversation 查询写入；
  - conversation 列表、staff queue、消息/citations 读取；
  - queue/claim/release/requeue/handoff 状态机已有 SQL（保留在 store）；
  - `customer_execution` 的 EXECUTING/FAILED/终态 SQL；
  - `customer_message` 插入（已有 `addMessage`）；
  - `ai.go` 的历史、权限锁、execution 查询与终态 UPDATE。
- `service.go` / `ai.go` 不再包含 SQL；handler 只解码/响应/调用业务函数。
- 新抽取的业务函数只收显式参数，不接 `*http.Request`；每个 ≤80 行。
- 行为零变更：`conversation_busy`、replay/幂等、epoch/租约/fencing、
  202/409/404/403、响应字段与终态语义逐字保持；既有测试断言不改。

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
用 `strings` 验证新增 store 符号，重启后进程启动时间必须晚于最后提交。

顾客链路 curl：`createSession` → `send` → `handoff` → `claim` →
`release` → `messages`，贴关键响应并与既有行为一致。必要时用 SQL 造
WAITING_HUMAN/lease 数据，测试后清理。

## 棘轮

`check-structure.py` 的 `HANDLER_SQL_BASELINE` 当前为 24；搬走 customer service/ai
的写 SQL 后按实测值精确下调，只许降。剩余 SELECT 清单在交付时逐文件登记。

## 不做

- 不搬 recovery/authorization、不改 `store.go` 之外的状态机语义。
- 不改响应体、状态码、错误文案、幂等键或 epoch/租约/fencing 规则。
- 不改迁移、评测集、smoke 基线或其它棘轮/预算。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
- 后续顺序：WP46 recovery + 剩余 SQL（目标 baseline 0）→ WP47 P1-7 恢复演练。

## 未覆盖路径

- 不新增 customer 纯单测；用既有 Go 测试与顾客链路 curl 证明行为不变。
- 不跑 `--db` / `--e2e` / `--all`；不验证真实模型路径。
- 不把 `stream` 的 SSE 写入搬进 store；只搬其查询 SQL。
