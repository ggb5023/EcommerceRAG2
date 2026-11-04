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

（实现与验证后填写。）

## 未覆盖路径

（实现与验证后填写。）
