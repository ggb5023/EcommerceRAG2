# 04 内容缺口闭环（B3）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 设计来源：`docs/decisions/0016-content-inspection.md`

## 目标

把 ASKING/FAILED 的 query 变成可跟踪的"待补文档清单"，补完后能导出为回归用例。

## 口径（分类判定与 03 共用同一纯函数）

- **来源桶**：`abandoned + failed`。
  - `failed`：latest execution 的 `decision.state='FAILED'`；
  - `abandoned`：`ASKING` 且超过 10 分钟宽限且没有 `parent_turn_id` 关联的更晚 `DONE`
    （与 03 的 `askingOutcome` 判定完全一致）。
- **不进缺口清单**（理由）：
  - `pending`：仍在宽限期内，顾客可能还会继续，不能算内容缺口；
  - `clarified`：已经通过追问解决；
  - `cancelled`：用户主动取消，不代表资料缺失；
  - `unknown`：没有决策记录，无法判断原因。
- **query 归一化**：小写 + 去标点 + 空白折叠；不做同义归并、不做拼写纠正。
- **聚合键**：归一化后的 query；每项记录 `count`、`last_seen`（最新 `turn.created_at`）、
  示例原句（取最近一条）、`reason`（`abandoned` | `failed`）。
- 同一归一化 query 同时来自 abandoned 与 failed 时，`reason` 取最近一条 turn 的分类。

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
