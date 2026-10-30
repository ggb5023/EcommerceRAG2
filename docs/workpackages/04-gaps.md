# 04 内容缺口闭环（B3）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 设计来源：`docs/decisions/0016-content-inspection.md`

## 目标

把 ASKING/FAILED 的 query 变成可跟踪的"待补文档清单"，补完后能导出为回归用例。

## 改动范围

- `go/internal/admin/store_analytics.go`（待建）：`KnowledgeGaps(ctx, tenantID, from, to)`（归一化 query + 计数 + 最近出现时间）。
- `go/internal/admin/outcomes.go`（待建）：`GET /admin/v1/merchant/gaps`、`POST /admin/v1/merchant/gaps/export`（导出为评测用例片段）。
- `frontend/src/admin/GapsPanel.vue`（待建）（新）+ 挂载。

## 不碰

- 不做自动补文档（只出清单）。
- 不改评测集本身；导出物只写到 `.local/` 或提示用户复制。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
cd go && go test ./internal/admin/ -run KnowledgeGaps -v
```


## 行为变更

零（纯读 + 前端）。

## 未覆盖路径

- query 归一化（去标点/大小写/同义）只做最简版本；相似问题合并效果需人工抽查后记录。
