# 36 M9 阶段 2（二）：handoff 聚合与评测报告 CLI

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0035](../decisions/0035-demo-report.md) §3、[metrics](../metrics.md)、
> [environment-facts](../environment-facts.md)

## 目标

交付 customer handoff 只读聚合接口与 `scripts/dev/demo-report.py`：读取演示店铺的
workspace outcomes、customer handoff、冻结 53 例质量结果和 `usage_log`，生成同一输入
可复现的 JSON + Markdown 五段报告。不做管理台报告页，不新增写路径。

## handoff 只读接口

`GET /admin/v1/merchant/report/handoff?range=7d|30d`，默认 `30d`；沿用管理端会话与
shop scope，不接收客户端 tenant。

- 分子：窗口 `[window_start, window_end)` 内 `customer_event.event_type='state'` 且
  `data_json->>'state'='WAITING_HUMAN'` 的去重 conversation 数；
- 分母：同一窗口内至少一条 `customer_message.source='customer'` 的去重 conversation 数；
- `handoff_rate = 分子 / 分母`；分母 0 时为 `null`，不是 0；
- 响应：`range`、`window_start`、`window_end`、`handoff_conversations`、
  `customer_conversations`、`handoff_rate`；
- SQL 只在 `store_*.go`；customer 写路径与表结构不变；查询错误返回 503，报告端
  按 `NOT_MEASURED` 处理。

单测覆盖分子、分母和分母为 0 的 null；SQL 只读、参数化、按 tenant/shop scope 过滤。

## 报告 CLI

`scripts/dev/demo-report.py` 默认读取 `ECR_DEMO_BASE_URL` 或
`http://127.0.0.1:26081`，输出到 `--output-dir`（默认 `.local/wp36/`），目录 0700、
文件 0600。报告五段：

1. **输入**：git commit、演示包聚合/manifest sha256、问题集 sha256、active version id、
   business_date、人群、窗口、冻结评测报告 sha256；
2. **运营**：`GET /outcomes` 全字段；
3. **质量**：冻结 53 例 `wrong_answer_rate`，标注 `reviewer_kind=agent`、
   `human_review_completed=false`、来源 sha256；缺报告写 `NOT_MEASURED`，不写 0；
4. **转人工**：handoff 接口的分子/分母/`handoff_rate`；
5. **token**：`usage_log` 的输入/输出 tokens、调用/失败次数，按问题数 10 归一为
   每千次，另列 `by_slot`/`by_model`；不写货币、金额、汇率或价格表。

报告不写顾客正文、prompt、凭据或 DSN；`real_service_acceptance=false`。缺数据时
保留字段并写 `NOT_MEASURED`/`null`。相同输入两次运行比较计数、分母、比率；时间戳
不参与比较；token 差异记录在 `input.reproducibility`（任选 `--previous`）。

## 验收命令

```bash
cd go && go test ./... && go vet ./...
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
```

报告命令按 README 快速开始执行两次。

另需：Go 重建后 `strings` 验证 handoff 标记并重启；两次报告计数/分母/比率 diff；
报告目录/权限；`grep` 确认无货币符号；缺质量报告时 `NOT_MEASURED`；handoff 分母 0
时 `null`。

## 不做

- 不做管理台报告页；不新增写路径；不改 customer 通道业务行为。
- 不调棘轮/基线；不重采 smoke；不改冻结 53 例、迁移或评测语料。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

- handoff 只读接口：`GET /admin/v1/merchant/report/handoff?range=7d|30d`，SQL 位于
  `go/internal/admin/store_handoff.go`；只读 `customer_event`/`customer_message`，
  按 tenant 与 shop scope 过滤，`event_type='state'` 且
  `data_json->>'state'='WAITING_HUMAN'` 去重计数；分母为同窗口
  `customer_message.source='customer'` 的去重 conversation；分母 0 返回 null。
- 单测 `TestHandoffRate...` 覆盖 2/8、0/4、0/0；集成测试
  `TestCustomerHandoffSQLCounts` 实库插入 1 个人工转移会话和 1 个仅消息会话，
  得到 1/2 = 0.5，空窗口得到 0/0/null，均 PASS。
- 示例栈 26081 实测：
  `{"range":"30d","handoff_conversations":0,"customer_conversations":0,"handoff_rate":null}`；
  systemd dev 实测分子 163 条、分母 163 条，rate `1`（dev 测试残留，不作为演示指标）。
- 报告 CLI `scripts/dev/demo-report.py`：输出五段 JSON + Markdown；默认质量报告为
  `.local/wp25/answer-quality.json`（实际 13.21%，verdict 39/7/7，
  source sha `3b828cb5...`）。`.local/wp24/answer-quality.json` 当前是 v1
  `0.207547`（38/4/11），不是 13.21%；报告不修改 `.local`，可用
  `--quality-report` 显式选择。
- 26081 最终两次报告（报告运行时 HEAD）：status=MEASURED；
  business_date=2026-09-29；active versions 282–286；handoff 0/0/null；
  quality `0.132075`、case_count 53、`reviewer_kind=agent`、
  `human_review_completed=false`；token 输入 0、输出 0、调用 65、失败 0，
  按 10 个问题归一，calls_per_1000=6500.0；输入包/manifest/questions sha256 与
  冻结报告 sha256 均写入 `input`。
- 两次运行 `diff -u` 归一化投影（去掉 generated_at/窗口时间戳）为空，
  `counts_equal=True`、`handoff_counts_equal=True`、`quality_equal=True`，
  token_delta 全 0；`input.reproducibility` 记录了 previous report sha 与解释。
- 缺质量报告运行输出 `status=NOT_MEASURED`、`wrong_answer_rate=null`、exit 2，
  未把缺失写成 0。
- 权限：输出目录 0700，`demo-report.json`/`demo-report.md` 0600；grep 未命中
  货币符号、currency、price、价格、金额或汇率。
- 验证：`go test ./...` PASS、`go vet ./...` PASS；Python
  `451 passed, 33 skipped`；handoff 集成测试 PASS；`bash scripts/check.sh`
  PASSED 40；`bash scripts/check.sh --smoke` PASSED 45（逐字节一致）。
- Go 重建 `.local/v2-gateway`，`strings` 命中 `customer_handoff_readonly_v1`；
  systemd dev 与 26081 均重启，启动时间晚于代码提交。未改 customer 写路径、
  表结构、迁移、冻结 53 例、棘轮/基线或 smoke 基线。

## 未覆盖路径

- 演示栈当前没有 customer 消息，因此 handoff 现场只验证了 0/0/null；分子/分母
  非零由 `TestCustomerHandoffSQLCounts` 实库单测覆盖，未用真实顾客会话。
- 未运行 `check.sh --db`、`--e2e`、`--all`；未做浏览器级页面验证。
- 报告未在真实模型/provider 流量下运行；token 为现有隔离栈 usage_log 快照，
  两次差异为 0，未验证真实调用差异解释。
- 报告窗口由服务端 `time.Now()` 生成；两次 JSON 的 `generated_at`/窗口时间戳
  不同，按 0035 只比较计数、分母、比率，不比较时间戳。
- 未实现管理台报告页（后续读取同一份 JSON）；未新增 customer 写路径。
- 未验证多实例并发运行报告；报告是只读快照，不保证跨实例边界完全同秒。
