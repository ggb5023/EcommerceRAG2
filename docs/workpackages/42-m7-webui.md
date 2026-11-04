# 42 P1-5：M7 WebUI 闭环与只读页面

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P1-5](../audits/2026-11-04-global-audit.md)、
> [0033](../decisions/0033-document-validity.md)、[0035](../decisions/0035-demo-report.md)

## 目标

闭合 M7 的 WebUI：在审核详情设置版本有效期；新增只读报告页展示 usage 与
customer handoff；gap 导出改为浏览器下载。不新增写业务路径、不改报告字段。

## 有效期 UI

- 在版本详情增加「设置有效期」表单：effective_from / effective_to（可空）、
  reason、confirm；使用版本详情的 `fencing_epoch` 与 session 的
  `permission_revision`；
- POST `/admin/v1/merchant/versions/{id}/validity` 带 `Idempotency-Key`；
- 成功后刷新详情、版本历史和状态徽标；冲突显示错误并提示重新加载；
- data-testid：`validity-form`、`validity-from`、`validity-to`、`validity-submit`、
  `validity-error`。

## 报告页

- 新增 `/admin/reports`，读取 `GET /admin/v1/merchant/usage?range=7d|30d` 和
  `GET /admin/v1/merchant/report/handoff?range=7d|30d`；
- 展示 by_slot/by_model 输入/输出 tokens、调用、失败，以及 handoff 分子/分母/rate；
  rate=null 显示「无数据」；
- 不展示货币符号、汇率或价格表（0021）。

## gap 下载

- `POST /admin/v1/merchant/gaps/export?download=1` 返回 JSONL 正文和
  `Content-Disposition: attachment`；不带 download 保持旧 `{path,count}`；
- GapsView 通过 Blob + `a[download]` 触发下载；更新 `docs/openapi.yaml`。

## 验收命令

```bash
cd go && go test ./...
cd frontend && npm test && npm run build
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 重建 + `strings` + 重启；live 有效期成功/冲突、usage/handoff 取数、
gap 下载 curl/浏览器证据；截图存 `.local/redesign/`。

## 不做

- 不改 metrics 口径、检索、评测集、迁移、报告字段；不自动发布/下架。
- 不改 ROADMAP M7 勾选（另立验收）；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

- 有效期 UI：版本详情增加 `data-testid=validity-form` 表单，提交
  `effective_from`/`effective_to`/`reason`/`confirm`/`expected_fencing_epoch`/
  `permission_revision`，带 `Idempotency-Key`；成功刷新详情/版本历史，409 显示
  “版本状态或 fencing epoch 已变化，请重新加载”。
  live 成功：
  ```text
  HTTP/1.1 200 OK
  {"audit_result":"success","command_status":"applied","effective_from":"2026-11-01",
   "effective_to":"2026-12-31","fencing_epoch":2,"status":"updated","version_id":"55"}
  ```
  live fencing 冲突：
  ```text
  HTTP/1.1 409 Conflict
  {"audit_result":"conflict","code":"precondition_failed","command_status":"rejected",
   "message":"version hash or fencing epoch changed","version_id":"55"}
  ```
  刷新详情后 `effective_from=2026-11-01`、`effective_to=2026-12-31`、
  `fencing_epoch=2`。
- 报告页：`/admin/reports` 读取 usage 与 handoff 的 7d/30d 数据，展示
  by_slot/by_model 与 handoff 分子/分母/rate；rate=null 显示“无数据”；无货币符号。
- gap 下载：`POST /admin/v1/merchant/gaps/export?range=30d&download=1` 返回
  `Content-Type: application/jsonl`、`Content-Disposition: attachment;
  filename=gaps-...jsonl` 和 JSONL 正文；不带 `download` 仍返回 `{path,count}`。
  GapsView 使用 Blob + `a[download]`，浏览器实测触发下载。
- 前端：`npm test` 14 files / 57 tests 全绿；`npm run build` 通过。
- Go：`go test ./...` 全绿，`go vet ./...` 通过；Go 重建并重启后验证启动时间。
- 截图存于 `.local/redesign/`：
  `validity-form.png`、`report-page.png`、`gap-download.png`、
  `gaps-download.jsonl`（截图不提交）。
- `docs/openapi.yaml` 已补 `gaps/export` 的 download/legacy 两种响应。
- `bash scripts/check.sh` PASSED 42；`bash scripts/check.sh --smoke` PASSED 45，
  五条 smoke 与 `.local/w2/` 基线逐字节一致。

## 未覆盖路径

- 浏览器截图使用临时 Vite + Playwright 脚本，未跑完整 `frontend/tests/*.browser.mjs`
  矩阵；截图只覆盖有效期表单、报告页和 gap 下载入口。
- fencing 冲突通过 curl 与前端 `validityError` 单测验证，未在浏览器表单内实际提交
  一条 409 冲突；UI 的冲突展示由同一错误处理函数覆盖。
- 未做全页面无货币 grep 的浏览器 DOM 断言；已对报告组件源码/渲染测试做无货币检查。
- 旧 export 兼容已用 curl 验证 `{path,count}`，未在旧前端调用方上做回归。
- 未改 metrics/检索/评测集/迁移；未运行 `--db`/`--e2e`/`--all`；未 push。

