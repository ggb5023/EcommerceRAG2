# 0033 资料生效与失效时间

- 状态：提议
- 日期：2026-11-02
- 关联：[26 M7 阶段 1](../workpackages/26-m7-expiry-design.md)、
  [0016 内容巡检](0016-content-inspection.md)、
  [0027 文档与来源归属](0027-document-source-ownership.md)、
  [data-ownership](../data-ownership.md)

## 背景

商家需要能说“这份资料在某日生效、某日失效”。当前 `chunk` 已有
`effective_from` / `effective_to`，检索按业务日期过滤；导入 manifest 也能带日期并传播到
chunk。但 `document_version` 没有日期字段，管理台没有设置入口，也没有过期/即将过期提示。
这只缺“版本级设置、传播入口、提示”，不是缺少检索过滤。

## 决定（建议）

1. **粒度：版本级**。商家在 `document_version` 上设置 `effective_from` / `effective_to`；
   chunk 保留同名字段作为检索物化结果。理由：商家的心智是“这份资料从某日生效/失效”，
   同一版本的所有 chunk 应共享有效期；chunk 级手改会产生同版本内不一致，难以审计。
2. **存储**：阶段 2 以新增迁移为版本增加两列（迁移号不在本提议中预先占用）；不修改
   0001–0046 已执行迁移；chunk 不改结构。
3. **设置入口**：admin API + 版本详情表单；建议入口
   `POST /admin/v1/merchant/versions/{id}/validity`（仅提议，不实现），要求
   `effective_from`、`effective_to`、`expected_hash`、`expected_fencing_epoch`、
   `permission_revision`、`reason`、`confirm`。日期可空；非空时 `to > from`。
4. **写者归属**：`document_version` 的写入方仍是 `ingest`（见 data-ownership）。
   admin handler 只做授权、参数校验、确认和审计转发；实际更新由 ingest 侧执行，
   **不允许 admin 直接 SQL 写 `document_version`/`chunk`**。阶段 2 需要新增 ingest
   command action（proto）或在现有命令上增加 validity 子命令；这是实现前置。
5. **传播**：ingest 命令在同一事务内更新 `document_version` 的两个日期，并更新该版本
   全部 `chunk.effective_from/effective_to`（以及 `meta_json` 中的同名字段）；不允许部分
   传播。并发控制复用 `expected_fencing_epoch`，并写入审计原因。
6. **过期语义**：`effective_to` 按当前检索 SQL 是**失效日（不含当天）**；当天起不再参与
   回答。过期不自动下架，`expired` 只用于检索过滤与管理台提示。即将过期阈值建议
   `N=30` 天，先在管理台提示“N 天内失效”，不做自动发布/撤回；0016 的
   `expired_not_removed` 仍是巡检提示，不替代商家动作。
7. **业务日期**：当前 `LangGraphRagService` 使用显式参数、`SYNTHETIC_BUSINESS_DATE`，
   否则用 UTC 当天；Go 还没有把商家当地业务日期作为 scope 字段传入。阶段 2 必须先决定
   生产业务日期的来源，再启用“即将过期”提示；不能把 UTC 日常默认值冒充商家日期。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| chunk 级直接编辑（admin 直接 UPDATE 多行） | [PostgreSQL UPDATE](https://www.postgresql.org/docs/current/sql-update.html) | 商家要维护同版本所有 chunk，易漏行；admin 直接写 chunk 违反单写者与审计要求；版本级语义丢失 |
| 数据库触发器从版本日期自动改 chunk | [PostgreSQL triggers](https://www.postgresql.org/docs/current/triggers.html) | 隐藏写路径，难以解释谁在何时改了有效期；与“阶段之间只通过显式数据通信”冲突；仍需要新迁移 |
| 物化视图/额外日期表 + 检索时 join | [PostgreSQL materialized views](https://www.postgresql.org/docs/current/rules-materializedviews.html) | 额外对象与刷新策略；检索已经按 chunk 日期过滤，版本级传播更简单；不能解决 admin 设置入口 |
| 定时任务自动下架过期版本 | [crontab(5)](https://man7.org/linux/man-pages/man5/crontab.5.html) | cron 属于部署状态，不进仓库；0016 明确只提示、不自动下架；自动改变发布状态不可回滚审计 |

## 后果

- 有效期成为版本属性，chunk 日期仍由 ingest 在导入和 validity command 中物化；
- 检索行为保持现状，不因本提议改变 SQL；
- 过期提示和 0016 巡检可以读取版本日期，但都不自动改变发布状态；
- 阶段 2 需要新增迁移、proto command 和管理台表单；本阶段不实现。

## 不实现（阶段 1）

- 不新增迁移/表/列，不改已执行迁移；
- 不写 admin API、前端表单或 ingest command；
- 不接 cron/自动下架；不修改检索 SQL 或 smoke 基线。
