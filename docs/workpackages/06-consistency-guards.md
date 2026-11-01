# 06 一致性收尾与机械护栏（P-D）

> 状态：见 INDEX.md　适用提交：7316674　验收：见 §验收命令
> 详细规格：`docs/archive/backend-refactor-plan.md` §3 P-D

## 目标

消除重复的响应写出路径，并新增结构守卫防止回流。

## 改动范围

- gateway / customer / localauth 的裸 `writeJSON` / `writeError` 统一为 `httpapi.WriteJSON` / `httpapi.WriteError`。
- 新增 `scripts/check/check-structure.py` 并接入 `check.sh`（39 → 40 项；`--smoke` 44 → 45）。
  - A：gateway/customer 非 store 文件的 INSERT/UPDATE/DELETE 棘轮，初值 34，只许降。
  - B：依赖方向（gateway/customer → admin 仅保留 2 个既有组合点；admin 不得反向 import）
    与 frontend/src 直连 Python gRPC 端口/DB DSN 痕迹。
- `docs/data-ownership.md` 登记 11 张核心热表，`UNDECLARED_BUDGET` 36 → 25。

## 不碰

- 不改响应体结构与状态码。
- 不改既有守卫的判定逻辑。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
python3 scripts/check/check-structure.py
```


## 行为变更

零。

## 未覆盖路径

- 新守卫需做一次负向验证（故意引入违规 → 断言 FAIL），否则视为未生效。

## 实际交付 vs 计划

- 新增 `check-structure.py`：A 基线 `HANDLER_SQL_BASELINE=34`；B 的
  `GATEWAY_ADMIN_BASELINE` 登记 `routes_admin.go`、`turn_outcome.go` 两个既有组合点。
  未新增源文档之外的函数行数检查。
- 统一响应写出：`httpapi.WriteJSON` 37 处、`httpapi.WriteError` 79 处；删除 gateway 与
  localauth 的本地 helper，响应体/状态码未变。
- ownership：登记 `turn`、`conversation`、`turn_idempotency`、`conversation_execution`、
  `message`、`sse_event`、`evidence_snapshot`、`citation`、`customer_conversation`、
  `customer_execution`、`customer_message`；未声明预算 36 → 25。
- T1 amendment：`check.sh` 39 → 40 项、`--smoke` 44 → 45 项；这是新增门禁，不是调高既有棘轮。
- 负向验证已完成：A 加一条 INSERT → FAIL；B 加一个 gateway→admin import → FAIL；
  ownership 在 `auth` 加一条 `INSERT INTO turn` → FAIL；撤销后均恢复 PASS。
- 06 保持 `active`；是否转 `done` 等主 agent 复核。
