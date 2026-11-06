# 60 内容巡检（L3，挂账 b：0016 巡检页）

- 状态：见 INDEX.md
- 依据：决策 0016（L3 内容巡检设计草案，本轮实现 v1 纯规则 + 管理台页）
- 前置：WP59 已关

## 目标

落地 0016 的 L3：四条纯规则巡检 + `inspection_report` 落库 + 管理台巡检页
（待办卡片 / 证据面板 / 三态动作）。巡检是**发现与提示**：只读业务表、
只写 `inspection_report`，不改变任何发布状态；不接模型、不做定时任务（cron
属部署状态不进仓库）。

## 迁移 0053（只追加；先登记再写库）

1. 先在 `docs/data-ownership.md` 登记 `inspection_report` 表与唯一写者
   （Go 管理域，`rag_v2_gateway` 角色，走 `admin/store_*.go`）。
2. `0053_inspection_report.sql`：
   - 建表按 0016 草案（id/tenant_id/shop_id/kind/severity/status/subject_type/
     subject_id/fingerprint/evidence_json/detected_at/resolved_at）；
   - **同迁移内 GRANT**（environment-facts #4）；
   - 去重索引修正 0016 草案：`UNIQUE (tenant_id, fingerprint) WHERE status='open'`
     （草案的 UNIQUE 含 detected_at 与"未处理按 fingerprint 去重不重复刷屏"矛盾，
     以去重要求为准，迁移注释写明）。
   - kind CHECK：`expired_not_removed` / `newer_version_conflict` /
     `unanswered_high_frequency` / `citation_invalid`；severity high/medium/low；
     status open/acknowledged/resolved/dismissed。

## 四条规则（v1 纯规则，只读现有表；阈值唯一出处=本包指定的 Go 常量文件）

阈值钉死：`INSPECTION_WINDOW_DAYS = 30`、`INSPECTION_MIN_CLUSTER_TURNS = 2`，
定义为 `go/internal/admin/inspection.go`（待建）（或 store 文件）中的常量，不在别处重复。

| kind | 判定 |
| --- | --- |
| `expired_not_removed` | `document_version.status='active'` 且 `effective_to < business_date`（business_date 复用 M7 口径：shop.timezone + scope 同源函数，不得另写一份） |
| `newer_version_conflict` | 同一 document 存在更新的 active/pending 版本，且旧 active 版本的 chunk 在近 30 天仍被顾客/员工引用（customer_reply / customer_message 快照） |
| `unanswered_high_frequency` | 复用 `store_analytics.go` 现有缺口聚合（不得重写一套）：近 30 天 ≥2 次的转人工/无命中问题簇，且无对应已发布资料 |
| `citation_invalid` | 已发布回答（customer_reply/customer_message 的 evidence 快照）引用的 chunk/version 已撤回、过期或对顾客不可见（复用 `auth.CustomerEvidenceAllowed` 同一授权检查，不得另写一套） |

severity：expired_not_removed、citation_invalid = high；newer_version_conflict =
medium；unanswered_high_frequency = medium。fingerprint =
`tenant/kind/subject_type/subject_id/规则版本` 的稳定值；重跑同 fingerprint 的 open
行做 upsert（更新 evidence_json/detected_at），不新增重复行。

## API（全部管理域，`store_*.go` 承载 SQL，handler 不许写 SQL）

- `POST /admin/v1/inspections/run` —— 立即巡检：跑四条规则、按 fingerprint upsert、
  返回 `{run_id, by_kind: {…}}` 摘要（本次新增/更新/关闭数）。
- `GET /admin/v1/inspections?status=open` —— 待办列表，high/medium 分组字段齐备，
  含 subject 概要（资料标题/缺口文案），不含顾客对话正文。
- `GET /admin/v1/inspections/{id}` —— 证据面板（evidence_json + 最近检测时间）。
- `POST /admin/v1/inspections/{id}/status` —— `acknowledged|resolved|dismissed`；
  resolved/dismissed 写 `resolved_at`；open 行唯一索引保证已关项可再检出（重开）。

`docs/openapi.yaml` 同步这 4 个 path（openapi 守卫双向比对，不写会红）。

## 前端（管理台）

- 新路由 `/admin/inspections`（挂 AdminShell）：待办卡片按 high/medium 分组，
  显示 kind、严重度、受影响资料/答案、建议动作、检测时间；
- 卡片展开证据面板；三个动作按钮（确认/已处理/忽略）调 status 接口；
- 顶部「立即巡检」按钮调 run 接口并显示摘要；
- 任何界面不出现货币金额（0021）。

## 测试

- Go：`inspection_test.go` 表驱动——每条规则构造 0/1/N 命中 fixture（含阈值边界：
  30 天整、29 天；簇 1 次 vs 2 次）；fingerprint 幂等（重跑不重复）；status 迁移
  状态机（open→acknowledged→resolved 后重检出可重开）。
- 前端：api 层调 4 个端点 + 卡片渲染的组件测试。
- 不需要 contracts 新行（同进程 Go↔前端走 HTTP，openapi 守卫已钉）。

## 验收命令

```bash
bash scripts/check.sh          # 44 项 FAILED 0（含 openapi、ownership、migration-grants）
cd go && go test ./internal/admin/ -run Inspection -v
cd frontend && npm test -- --run
# 真实链路：dev 库制造一条 expired active 版本 → POST run → GET 列表见 expired_not_removed
# → status 动作 → 列表移除；再 run 一次可重开（若仍过期）
bash scripts/check.sh --smoke  # 47 项，基线逐字节一致（改 Go 后必须先重建二进制+重启）
```

## 不要做

- 不接模型、不做定时任务、不写 cron、不自动下架/撤回任何资料；
- 不新增"agent 自动处理"动作；
- 不动 L1/L2 守卫与 `tenant_agent_review` 开关；
- 不改 CONTRACTS_* 棘轮；不新增依赖；不改冻结评测集；
- 迁移只新增 0053，不改任何历史迁移。

## 汇报

五节格式；附迁移 GRANT 行、ownership 登记行、四条规则各一次真实命中/未命中证据、
openapi 守卫输出、smoke 输出、前端测试输出。
