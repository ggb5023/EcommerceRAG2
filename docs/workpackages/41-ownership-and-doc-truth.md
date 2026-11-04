# 41 P1-2/P1-3：单写者覆盖与文档数字真实性

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-2/P1-3](../audits/2026-11-04-global-audit.md)、
> [data-ownership](../data-ownership.md)、[constraints](../constraints.md)

## 目标

把脚本实测的 23 张未声明被写表全部登记，`UNDECLARED_BUDGET` 降为 0；同时校正
审计指出的文档数字漂移，并新增轻量数字守卫。只改文档和守卫脚本，不改产品代码。

## P1-2 单写者

- 先运行 `python3 scripts/check/check-ownership.py`，逐条抄出 23 张表及写者；
- 在 `docs/data-ownership.md` 补行：允许写者严格按脚本实测，读取方能从代码确认的
  写清，无法确认的写“未逐一审计”，理由与检查列不可留空；
- `UNDECLARED_BUDGET` 与文档顶部预算均改为 0；
- 多写者表如实列出全部写者，不写 `any`；
- 负向验证：在非写者文件临时加一条 `INSERT`，断言 FAIL，撤销后 PASS。

23 张表（脚本实测）：

```text
chunk (ingest), control_command (ingestclient), customer_command (customer),
customer_event (customer), customer_identity (customer), customer_reply (gateway),
customer_retrieval_trace (retrieval), import_batch (admin), import_item (admin),
ingest_artifact_object (ingest), ingest_command_result (ingest), ingest_event (ingest),
ingest_job (ingest), ingest_job_item (ingest), ingest_payload (ingest),
ingest_publication (ingest), ingest_worker_heartbeat (ingest),
local_auth_event (localauth), review_suggestion (admin), shop (admin),
source (ingest), tenant_agent_review (admin), v2_asking_checkpoint (graph)
```

## P1-3 文档数字

以本批实测为准校正：

- `docs/agent-entry.md`：迁移数、快速层门禁数、admin SQL 数、domain 覆盖率；
- `docs/contracts.md`：domain 覆盖率硬数字；
- `docs/constraints.md`：决策文件数、ownership 预算；
- `docs/data-ownership.md`：预算；
- `docs/architecture.md`：删除/更新“无商品表结构、无时效”的过时差距；
- `docs/ui-only-acceptance.md`：按后端实际路由和前端缺口校正，或删除硬计数改为
  链接到唯一出处。

原则：能删的硬计数改为命令/链接；必须保留的数字由 `check-doc-numbers.py` 校验。

## 数字守卫

新增 `scripts/check/check-doc-numbers.py`，校验：

1. `sql/migrations/*.sql` 数量；
2. `scripts/check.sh` 默认 fast 层 `run fast` 项数；
3. `docs/decisions/` 决策文件数；
4. `check-ownership.py` 的 `UNDECLARED_BUDGET` 与 `data-ownership.md` 顶部预算一致。

不校验 domain 覆盖率（动态且慢），在脚本注释和 WP 中写明。接入 `check.sh` 默认
fast 层，41 → 42；`--smoke` 保持 45 项。同步更新 `CLAUDE.md`、`agent-entry` 与
`environment-facts` 中保留的计数。

负向验证：临时改错数字 → FAIL → 撤销 → PASS。

## 验收命令

```bash
python3 scripts/check/check-ownership.py
python3 scripts/check/check-doc-numbers.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：四类文档守卫全过；贴 23 表清单与预算 diff；数字负向验证两段输出。

## 不做

- 不改产品代码、迁移、评测集、ROADMAP M7 勾选或其他棘轮/基线。
- 不重采 smoke；不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（实现与验证后填写。）

## 未覆盖路径

（实现与验证后填写。）
