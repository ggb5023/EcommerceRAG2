# 0027 文档与来源的归属

- 状态：提议
- 日期：2026-10-29
- 关联：[0004](0004-retained-invariants.md)（不变量 #5 一张表只有一个写入方）、[0021](0021-usage-log-instead-of-budget.md)、[error-taxonomy](../error-taxonomy.md)

## 问题

`document` 的唯一键是 `(tenant_id, logical_key)`，而 `logical_key` 由导入清单的 `document_id` 决定；
`document.source_id` 在创建时写死，之后不随版本变化（`python/app/ingest/database.py` 的导入事务）。

后果：**一个 document id 一旦属于某个 source，就永远只能由这个 source 出新版本**。
换一个 source（例如商家把资料从 A 系统迁移到 B 系统、或重新走一次带新 source 的导入）再用同一个
`document_id` 重传时，Python 侧抛 `IngestConflictError`，对外表现为
`409 version_conflict`（"该文档标识已被另一个来源占用；请使用新的标识，或撤回后先清理来源"）。

当前状态是**短期拒绝**：不自动改归属，也不允许换来源写同一 id。本记录只列语义选项，不含实现。

## 三种可能的语义

| 选项 | 行为 | 对引用历史的影响 | 对撤回的影响 | 主要风险 |
| --- | --- | --- | --- | --- |
| **A 允许新版本** | 同一 `document_id` 允许换 `source_id` 出新版本 | 引用历史跨来源续接：旧引用指向同一 document 的旧版本，读者看不到"来源已换" | 撤回仍按版本进行；但"撤回后重传"会重新落到同一 document 上 | 归属不再是稳定事实：审计里"这条资料来自哪里"会随时间漂移 |
| **B 要求新 id** | 换来源必须用新 `document_id`，旧 document 保持原归属 | 引用历史按 id 分离，旧引用不会指向新来源的内容 | 旧 id 可整体下线；新 id 从零开始 | 商家要自己维护"旧 id → 新 id"的迁移表；同一份内容可能出现两条文档 |
| **C 保持拒绝** | 换来源必须撤回并清理来源后才能复用同一 id | 引用历史保持不变 | 撤回 + 清理是显式动作，审计可见 | 运维成本高；错误信息必须可操作（已落地） |

## 倾向

短期维持 **C**（已实现：409 + 可操作文案），因为：

- 选项 A 会让"文档来源"从稳定事实变成可变状态，与不变量 #5（一张表只有一个写入方）的审计意图冲突；
- 选项 B 与 A 之间没有明显更优解，需要商家侧的真实迁移场景数据才能判断；
- C 的代价是一次显式的清理动作，可审计、可回滚。

采用 A 或 B 之前需要先回答：撤回记录保留多久、引用快照（`evidence_snapshot` / `citation`）是否随 document 归属变化重算、
以及跨来源同 id 在评测集里如何计数。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 允许换来源覆盖 `document.source_id`（选项 A 的激进版） | [PostgreSQL UPSERT](https://www.postgresql.org/docs/current/sql-insert.html) | 归属变成"最后一次写入者"，审计与引用历史都无法解释；与不变量 #5 直接冲突 |
| 引入 `document_alias` 表做 id 迁移（选项 B 的实现路线） | [refactor-input 第 9 条](../refactor-input.md) | 需要新的迁移与后台任务，属功能而非重构；且没有真实迁移场景前无法验证 |
| 用 source 名称做 `logical_key` 前缀（隐式换 id） | — | 隐式改键会让已有引用永久失联，比显式拒绝更糟 |
