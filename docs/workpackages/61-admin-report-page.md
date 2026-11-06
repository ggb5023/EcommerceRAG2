# 61 管理台报告页（读 0035 同一份 JSON）

- 状态：见 INDEX.md
- 依据：决策 0035 §3（评测报告五段）；WP36 已产出 CLI 与 JSON
- 前置：WP60 已关

## 目标

管理台只读展示报告 CLI（用法唯一出处见 36-m9-report.md）生成的**同一份** JSON，不重算任何指标。
报告由 CLI 生成（.local/wp36/report.json），管理台新增只读接口 + 页面。

## 跨进程契约（先登记，后实现）

`docs/contracts.md` 新增一行（类型 `跨进程`）：

- 契约名：评测报告 JSON 五段结构
- Go 侧：`go/internal/admin/report.go`（待建）：只读透传 + 顶层键白名单
  五个顶层键（`input`、`operations`、`handoff`、`quality`、`tokens`）
- Python/前端侧：报告 CLI 五段输出（status=MEASURED/NOT_MEASURED，
  分母 0 为 null，无正文/无 prompt/无 DSN/无货币）
- 两侧测试：Go `TestAdminReportWhitelistsSections`（多余顶层键被剔除/拒绝）/
  Python `python/tests/test_demo_report_structure.py`（待建：五段必在、NOT_MEASURED
  不写 0、null 分母、无货币符号）
- 状态 ✅（两侧测试落地后标）；不写则按 ❌ 交付并说明

## Go 侧（admin 域，SQL 不需写——纯文件读取）

1. `go/internal/admin/report.go`（待建）：
   - 路径来自环境变量 `ECR_REPORT_PATH`（未设置或文件不存在 → 404
     `{"code":"report_unavailable"}`，不报 500）；
   - 防御性读取：必须 regular file、大小 ≤ 1 MiB、解析 JSON 失败 → 404；
   - **顶层键白名单**：只透传 五个顶层键（`input`、`operations`、`handoff`、`quality`、`tokens`），
     其余顶层键丢弃（防止未来 CLI 多写字段时泄漏新字段）；
   - 不打印文件路径/内容到日志。
2. handler：`GET /admin/v1/report`（`requireAdmin`，merchant 及以上）。
   路由注册进 `handler.go`；`docs/openapi.yaml` 加此 path（openapi 守卫双向比对）。
3. 本地服务启动脚本（v2_services）的认可键集合补 `ECR_REPORT_PATH`
   （configure 不接受未知键）；`/etc/ecommerce-rag/dev.env` 增加
   `ECR_REPORT_PATH=/data/app/EcommerceRAG2/.local/wp36/report.json`（仓外配置，
   不是仓库改动）；`docs/development.md` 环境表同步一行。

## 前端（管理台）

- 新路由 `/admin/report`（挂 AdminShell，导航加「评测报告」）：
  1. **输入**：git commit、演示包 sha256、manifest/version id、business_date、
     人群、窗口、冻结评测报告 sha256；
  2. **运营结果**：`outcomes` 各计数 + `auto_rate`/`assisted_rate`；
  3. **质量**：`wrong_answer_rate` + `reviewer_kind=agent`、
     `human_review_completed=false`、来源报告 sha256；
  4. **转人工**：customer handoff 计数/分母/比率（分母 0 → null 显示「未测量」）；
  5. **token**：输入/输出 tokens、调用次数、失败次数、每千次归一值、
     `by_slot`/`by_model`。
- 规则：`NOT_MEASURED`/null **显示「未测量」，绝不显示 0**；任何位置不出现
  货币符号/汇率/价格；接口 404 时显示「未生成报告」+ 提示运行
  报告 CLI（按 36-m9-report.md 记录的用法运行）。

## 测试

- Go：表驱动（正常 JSON 五段透传 / 缺文件 404 / 非 regular 404 / 超 1 MiB 404 /
  坏 JSON 404 / 多余顶层键被剔除 / 权限：非 merchant 403）。
- 前端：api 层 + 报告页组件（五段渲染、NOT_MEASURED 文案、404 兜底、无货币断言）。
- Python：`test_demo_report_structure.py`（待建）锁五段键集与 fail-closed 语义。

## 验收命令

```bash
bash scripts/check.sh          # 44 项 FAILED 0（openapi 必须过）
cd go && go test ./internal/admin/ -run Report -v
cd frontend && npm test -- --run
# 真实链路（dev）：先按 WP36 记录的用法运行报告 CLI 生成 JSON（命令唯一出处是
# 36-m9-report.md，此处不重复陈述），再：
curl -s http://127.0.0.1:8080/admin/v1/report | python3 -m json.tool | head -20
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/admin/v1/report   # 200
# 未授权（无 session cookie）应为 401/403
bash scripts/check.sh --smoke  # 47 项基线逐字节一致（改了 Go 需重建+重启，见 environment-facts #1）
```

## 不要做

- 不重算/不聚合任何指标（页面只渲染 CLI 已算好的值）；
- 不重复陈述报告 CLI 的用法（单一出处是 WP36 与 development.md，docs-drift 预算 0）；
- 不写业务表、不加迁移、不加依赖；
- 不把报告 JSON 提交进仓库；不打印报告内容/路径到日志；
- 不改 CONTRACTS_* 棘轮（新增 ✅ 行 NOTE 由主 agent 单独提交）；
- 不动报告 CLI 的生成逻辑（只允许为其补测试文件）。

## 汇报

五节格式；附契约行内容、白名单剔除测试输出、真实 curl 五段头几行、前端测试输出、
smoke 输出。
