# 28 M7 阶段 2（二）：版本有效期实现

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0033](../decisions/0033-document-validity.md)、
> [data-ownership](../data-ownership.md)、[contracts](../contracts.md)

## 目标

在版本级实现生效/失效日期：迁移、ingest validity command、admin API 与传播；
不做过期提示前端（WP29）、不自动下架、不改检索 SQL。

## 口径（0033）

- `document_version` 增加可空 `effective_from` 与 `effective_to`；chunk 保留物化字段。
- admin 只做授权、日期校验、confirm 和审计转发；实际 SQL 由 ingest command 在
  同一事务内写 `document_version` + 全部 chunk + `meta_json`。
- `expected_fencing_epoch` 不匹配拒绝；日期 `to > from`，空表示无界。
- `effective_to` 是失效日（不含当天），检索已有 SQL 不改。

## 验收命令

迁移 0048 三库 sha256 与 from-zero；proto 生成与 lint/build/breaking；Go/Python
全测；多 chunk 版本设置后 SQL 断言；接口 curl；快速层 40 与 smoke 45。


## 不做

- 不做过期/即将过期提示前端（WP29）；不改检索 SQL；不自动下架；
- 不调棘轮/基线；不重采 smoke；不引依赖；
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- 迁移 0048：`document_version.effective_from/effective_to` 与范围约束；三库 48/48
  应用、sha256 无差异，from-zero PASS（48 files）。
- proto：新增 `INGEST_COMMAND_ACTION_VALIDITY = 7`；`ExecuteCommandRequest` 新增
  `effective_from = 11` / `effective_to = 12`；`IngestCommandResult` 新增
  `effective_from = 10` / `effective_to = 11`；`buf lint/build/breaking` exit 0。
- ingest：`version.validity` 在同一事务更新 version 两列 + 全部 chunk 两列 +
  `meta_json` 同名字段；旧 `expected_fencing_epoch` 拒绝；结果写 command result。
- admin：新增 `POST /admin/v1/merchant/versions/{id}/validity`；日期校验、
  confirm、permission revision、expected fence、审计；实际写入经 ingest command，
  admin 不写 SQL。旧 fence 重放返回 409 `precondition_failed`。
- 实测（dev 多 chunk 版本 38，2 chunks）：
  - 设置 `2026-01-01` / `2027-01-01` 返回 **200**，`fencing_epoch` 1→2；
  - document_version 日期一致；2/2 chunks 的列与 `meta_json` 一致；
  - admin_audit 新增 `version.validity` success，metadata 日期一致；
  - 用旧 fence=1 重放返回 **409**，`status=rejected`、`code=precondition_failed`；
  - 同 Idempotency-Key 重放返回 `idempotent=true`。
- Go `go test ./...` 全绿；Python **421 passed / 30 skipped**；proto exit 0。
- 重启 Go/Python 后启动时间晚于最后提交（见汇报验证结果）。
- 门禁：`check.sh` 40 PASS；`--smoke` 45 PASS，五条逐字节一致。

## 未覆盖路径

- 只验证一个 ready 版本；未在 active/superseded 版本上逐状态验证。
- 未做 WP29 的过期/即将过期提示前端；未自动下架；未改检索 SQL。
- 日期校验是日期字符串，不做时区换算；业务日期语义仍由 0033/scope 负责。
- 未做真实模型或浏览器 UI 验证；未 push。

