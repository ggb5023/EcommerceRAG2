# 03 结果度量：自动解决率（B1）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 前置：02-turn-decision（需要 strength 字段）

## 目标

给商家一个能看懂的数字：本月 N 次提问中，直接解决 X、澄清后解决 Y、失败 Z，自动解决率 = (X+Y)/N。

## 改动范围

- `go/internal/admin/store_analytics.go`（待建）（新）：`ResolutionSummary(ctx, tenantID, from, to)`。
  口径：DONE 计为解决；ASKING 后同会话 5 分钟内出现相似 query 视为未解决；FAILED 计入失败。
- `go/internal/admin/outcomes.go`（待建）（新）：`GET /admin/v1/merchant/outcomes?range=`。
- `frontend/src/admin/OutcomesPanel.vue`（待建）（新）+ `admin/api.ts` 加调用 + `AdminApp.vue` 挂载。
- `docs/contracts.md`：无新增跨进程契约（页面直连 Go）。

## 不碰

- **不得出现任何货币金额**（0021）：只统计次数与 token。
- 不引入新服务、不做实时聚合（按请求聚合即可）。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```
管理台结果页：三个计数与自动解决率可读；页面内不出现任何货币符号或金额字段
```


## 行为变更

零（纯读）。

## 未覆盖路径

- "同会话 5 分钟内相似 query"的相似度阈值需给出实测依据；首版可用精确归一化匹配，并在报告里说明局限。
