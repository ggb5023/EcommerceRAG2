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
bash scripts/dev/demo-report.py --base-url http://127.0.0.1:26081 \
  --output-dir .local/wp36-a
bash scripts/dev/demo-report.py --base-url http://127.0.0.1:26081 \
  --previous .local/wp36-a/demo-report.json --output-dir .local/wp36-b
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 重建后 `strings` 验证 handoff 标记并重启；两次报告计数/分母/比率 diff；
报告目录/权限；`grep` 确认无货币符号；缺质量报告时 `NOT_MEASURED`；handoff 分母 0
时 `null`。

## 不做

- 不做管理台报告页；不新增写路径；不改 customer 通道业务行为。
- 不调棘轮/基线；不重采 smoke；不改冻结 53 例、迁移或评测语料。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（实现与验证后填写；未完成项在汇报第 4 节点名。）

## 未覆盖路径

（实现与验证后填写。）
