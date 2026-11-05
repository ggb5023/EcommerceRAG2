# 50 P2：通用保留策略（sse_event 等）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P2](../audits/2026-11-04-global-audit.md)、
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
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。
