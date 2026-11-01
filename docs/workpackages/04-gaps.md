# 04 内容缺口闭环（B3）

> 状态：见 INDEX.md　适用提交：f371072　验收：见 §验收命令
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

- `go/internal/admin/store_analytics.go`：`KnowledgeGaps(ctx, db, tenantID, from, minCount)`
  与共享的 `askingOutcome` / `clarifiedParentSet`（03 与 04 同一判定）。
- `go/internal/admin/gaps.go`：`GET /admin/v1/merchant/gaps`、`POST /admin/v1/merchant/gaps/export`；
  导出写 `.local/gap-exports/`。
- `frontend/src/admin/GapsPanel.vue` + `GapsView.vue` + AdminApp 路由挂载。
- `docs/error-taxonomy.md`：`invalid_min_count`、`gaps_unavailable`、`gap_export_failed`。

## 不碰

- 不做自动补文档（只出清单）。
- 不改评测集本身；导出物只写到 `.local/` 或提示用户复制。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
cd go && go test ./internal/admin/ -run 'KnowledgeGaps|ResolutionSummary' -count=1 -v
curl -s 'localhost:8080/admin/v1/merchant/gaps?range=30d' | jq
curl -s -X POST 'localhost:8080/admin/v1/merchant/gaps/export?range=30d' | jq
```

## 行为变更

无领域写入；export 只写 `.local/gap-exports/` 下的本地 JSONL。

## 未覆盖路径

- query 归一化只做小写、去标点、空白折叠；不做同义归并、拼写纠正或相似度聚类，
  合并质量需人工抽查。
- 导出的 JSONL 只是可复制的评测用例片段，不自动并入冻结评测集，也不写评测库。
- dev 库当前只有两个归一化簇，无法覆盖长尾聚合；聚类边界由单测覆盖。
- 未接 customer 人群；未跑 `--db` / `--e2e`，前端未做人工视觉检查。

## 实际交付 vs 计划

- 计划内：`KnowledgeGaps` 纯函数 + SQL、两个 admin handler、JSONL 导出、GapsPanel 列表/空态/导出按钮。
- 计划外：把 `askingOutcome` / `clarifiedParentSet` 从 03 抽出供 04 复用（03 测试断言未改）；
  `GapsView.vue` 拆出展示层以便 SSR 渲染测试；新增三个错误码；导出目录改为按仓库根锚定，
  避免 gateway 的 `python/` 工作目录把文件写错位置。
- 状态：04 按 INDEX 为 `active`；是否转 `done` 等审阅决定。
