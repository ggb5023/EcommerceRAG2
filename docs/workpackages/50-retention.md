# 50 P2：通用保留策略（sse_event 等）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P2（2026-11-04 本机私有快照，未入库）、
> [0021](../decisions/0021-usage-log-instead-of-budget.md)、
> [backup](../backup.md)、[data-ownership](../data-ownership.md)

## 目标

为 append-only 事件/轨迹表建立可执行的 tenant + 时间保留口径，避免无限增长；
先盘点实测数据并确定口径，口径确认后再实现独立清理脚本。审计表与 usage_log
不在默认删除范围。

## 范围

- 盘点 `sse_event`、`customer_event`、`customer_retrieval_trace`、`retrieval_trace`、
  `routing_trace`、`tool_call_trace`、`ingest_event`、`local_auth_event`、`usage_log`、
  `admin_audit` 等表的行数、大小、每轮平均行数与索引风险。
- 提出保留期、清理入口、dry-run/execute 语义、FK 删除顺序与索引建议。
- 口径确认后（下一轮）才实现独立脚本；不在本工作包删除任何行。

## 验收命令（§3，待口径确认后执行）

```bash
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：dev 库 dry-run 的行数/表分布；小范围正式清理前/后行数；脚本不触碰
`admin_audit`、`usage_log`、`local_auth_event`；备份/恢复文档说明保留与备份关系。

## 不做

- 不在本工作包删除行、不改迁移、不新增分区或索引。
- 不删审计表、usage_log；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实测盘点（2026-11-05，dev 库，只读）

| 表 | 行数 | 总大小 | 每轮/每单位平均 | 现有索引与风险 |
| --- | ---: | ---: | ---: | --- |
| `sse_event` | 12,523 | 5,984 kB | 15.25 / turn（821 turn） | PK/idx_sse_replay 是 tenant+execution+sequence；无 tenant+created_at |
| `customer_event` | 621 | 336 kB | 3.01 / customer_execution（206） | PK 是 tenant+conversation+sequence；无时间索引 |
| `customer_retrieval_trace` | 398 | 248 kB | 1.93 / customer_execution | tenant+execution+id；无时间索引 |
| `retrieval_trace` | 940 | 384 kB | 1.14 / turn | 只有 PK；无 tenant/time 索引 |
| `ingest_event` | 47 | 72 kB | 1.88 / ingest_job（25） | 只有 PK；无 tenant/time 索引 |
| `tool_call_trace` | 0 | 16 kB | — | 只有 PK |
| `routing_trace` | 0 | 16 kB | — | 只有 PK |
| `local_auth_event` | 0 | 32 kB | — | 有 created_at 时间线，无 tenant |
| `usage_log` | 137 | 96 kB | — | 有 tenant+created_at 索引 |
| `admin_audit` | 257 | 160 kB | — | 有 tenant+created_at 索引 |

当前数据窗口仅 2026-10-28 至 2026-11-05；上述事件表 `older_30d=0`、
`older_90d=0`。因此按 90 天 dry-run 当前将删除 0 行；删除行为需在隔离库加
合成旧行或在小范围副本上验证，不能为了“有删除数字”改 dev 数据。

## 保留口径提案（待主 agent 确认）

- **默认事件/轨迹 90 天**：`sse_event`、`customer_event`、
  `customer_retrieval_trace`、`retrieval_trace`、`routing_trace`、
  `tool_call_trace`、`ingest_event`。理由：覆盖一个季度的事故回放/排障窗口；
  当前 12.5k 行、5.9 MB，90 天按当前速率约 140k 行/67 MB，规模仍可
  用普通表管理。删除后旧 turn 不再有 SSE/轨迹回放，但 `message` /
  `customer_message` / `turn` 保留。
- **usage_log 400 天**：token 使用是年度报告与对账依据；默认脚本不删该表，
  单独的人工/后续任务处理。
- **admin_audit 永久**：审计不参与清理，也不能改写。
- **local_auth_event 400 天**：登录安全审计，默认脚本不删；单独处理。
- **不处理**：`turn`、`conversation_execution`、`message`、`customer_message`、
  `turn_decision`、`feedback`、`review_suggestion`；`review_suggestion` 已有
  自己的保留逻辑。
- **分区**：本阶段不做。当前最大表 <10 MB；先做 tenant+时间索引和独立删除脚本。
  若 `sse_event` 持续 >10 GB 或删除锁时间不可接受，再另立分区迁移。
- **索引**：建议仅在 `sse_event` 增加 `(tenant_id, created_at)`；其余事件表
  <1 MB / <1k 行，顺序扫描可接受，等单表 >100 MB 再评估。
- **脚本**：`retention.sh`（待实现），默认 `--dry-run`，必须显式 `--execute`；
  `--tenant` 必填或 `--all-tenants`；从 `ECR_RETENTION_DSN` 读取迁移/owner 角色，
  缺 DSN exit 2（UNCONFIGURED）。固定表 allowlist，不接收动态表名；单事务按
  叶子表顺序删除（目标表之间无 FK 子表，父 turn/execution 保留）。不引入 cron；
  文档给出手动/systemd timer 入口。
- **执行顺序**：`sse_event`、`customer_retrieval_trace`、`customer_event`、
  `retrieval_trace`、`tool_call_trace`、`routing_trace`、`ingest_event`；
  usage/admin/local_auth 永不进入默认 allowlist。

## 待确认问题（§3 前）

1. 90 天事件窗口 / usage_log 400 天 / admin_audit 永久 / local_auth_event 400 天
   是否接受？
2. 是否允许为 `sse_event` 增 `(tenant_id, created_at)` 索引（需新迁移 0051）？
3. 是否允许清理脚本使用迁移/owner 角色 `ECR_RETENTION_DSN`，还是必须另建最小
   retention 角色（后者需新迁移授权）？
4. 是否同意本阶段不做分区，按 90 天普通表删除；分区留作后续选项？
5. 验证删除时是否允许在隔离 scratch 库加合成旧行，避免改 dev 现有数据？

## 实际交付

- 迁移 0051：`CREATE INDEX idx_sse_event_tenant_created ON sse_event
  (tenant_id, created_at)`；from-zero `PASS 0051` / `PASS 51 migration checksums`。
  dev/test-integration/eval 三库均 51 行，0051 checksum 与文件
  `f289d18dad53769188dc39af26fe4b3a10d24f01507b22667b6c3dfe236215d4` 一致。
- `scripts/ops/retention.sh`：默认 dry-run；`--execute` 才删；`--tenant` 或
  `--all-tenants` 必选；`ECR_RETENTION_DSN` 缺失 exit 2；固定七表 allowlist；
  单事务按叶子表顺序删除；`bash -n` 与 `shellcheck -S warning` 通过。
- scratch 验证（合成 100 天旧行 + 当前新行；usage/admin 各 2 行）：
  ```text
  DRY-RUN sse_event 1, customer_retrieval_trace 1, customer_event 1,
          retrieval_trace 1, tool_call_trace 1, routing_trace 1, ingest_event 1
  DRY-RUN total 7
  DELETED ... 每表 1，DELETED total 7
  执行后七张目标表各剩 1 条新行；usage_log=2、admin_audit=2 未动
  ```
- scratch 库已在取证后 drop；dev 库未执行 retention，未删除任何 dev 行。
- 文档：`backup.md` 增加保留与备份关系、运行方式、恢复后先 dry-run；
  `constraints.md` 将事件/轨迹保留期与个人信息保留期标为已实现。
- TODO：生产上线前把 `ECR_RETENTION_DSN` 从迁移/owner 角色换成最小 retention
  角色（本轮按授权不新建角色）。
- 分区触发条件：`sse_event` >10 GB 或删除锁时间不可接受时另立分区迁移；本阶段
  不分区。
