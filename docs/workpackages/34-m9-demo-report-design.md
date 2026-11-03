# 34 M9 阶段 1：演示店铺与评测报告调研与决策提议（不实现）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[ROADMAP](../../ROADMAP.md)、[product](../product.md)、
> [metrics](../metrics.md)、[0021](../decisions/0021-usage-log-instead-of-budget.md)、
> [0012](../decisions/0012-agent-delegated-evaluation.md)、[0015](../decisions/0015-agent-review-owns-quality-in-development.md)

## 目标

只做 M9 的调研与提议：确定演示店铺的一键启动形态、合成语料来源、演示身份与发布
状态，以及评测报告的指标、数据来源和可复现要求。本轮不写实现代码、不建脚本、
不加迁移/表/列、不重启服务。

## 现状盘点（先查再写）

### 合成语料

- `data/synthetic/ecommerce-demo-v1`：10 个文档，覆盖 2 个 tenant、3 个 shop；
  格式为 5 CSV、4 Markdown、1 YAML；有商品、政策、FAQ、事实快照和 ACL fixture。
  面向 `demo-tenant-a` 与 `demo-shop-east` 的只有 7 个文档，且商品 manifest 仍是旧
  `doc_type: product`，不是 M8 的 `product_table`/`table_schema`。
- `data/synthetic/customer-service-v1`：4 个文件（care、FAQ、materials、returns），
  有客服语境内容但**没有 manifest**，不在现有导入/评测包内。
- `data/synthetic/ecommerce-m2-aligned-v1`：37 个文档，全部 Markdown，单店
  `synthetic-tenant` 与 `shop-demo`，是冻结评测语料；有 7 个 product 文档，但没有
  typed product table、尺码表或演示问题集。
- 缺口：没有单一演示包；没有 typed product table + size chart；没有演示问题/答案点；
  没有一键 seed/publish 脚本。`ecommerce-demo-v1` 跨 tenant/shop，不能直接当作
  dev `v2-demo-tenant` 与 `shop-demo` 的演示店铺。

### 演示身份与发布状态

- 0017 迁移提供 `v2-demo-tenant`、`dev-user`、`shop-demo`；dev 库中 `dev-user`
  为 operator，并持有 `merchant_admin`/`knowledge_reviewer` 授权。
- 当前 dev 库有 4 个 active 文档、40 个 pending 文档、18 个 confirmed 批次；
  active 版本只有 `w2-delivery-c`（version 6），0017 的 fixture 文档
  `v2-doc-*` 有 active 文档但 `active_version_id` 为空。`wp31-products` 版本 93
  已撤回。演示店铺不能依赖现存测试残留，必须从固定合成包重建并发布。
- `seed-fixtures.sql` 只恢复旧 fixture 身份；`seed-accept-fixtures.py` 只补审核
  队列；没有“演示店铺”级的一键启动/重置脚本。

### 指标资产

- `GET /admin/v1/merchant/outcomes?range=7d|30d`：workspace `turn` 的
  `turns_total`、`scored_total`、`solved`、`clarified`、`abandoned`、`failed`、
  `cancelled`、`pending`、`unknown`、`auto_rate`、`assisted_rate`、`reasked_within_5m`。
  不含 customer 通道。
- `GET /admin/v1/merchant/gaps`：workspace 的 abandoned/failed 查询聚合；不替代转人工率。
- `wrong_answer_rate`：目前只有冻结 53 例的 LLM-judge 结果，实测 **13.21%**
  （correct 39、partial 7、wrong 7，2026-11-02）；没有 admin API，不能从 DONE 推断。
- `usage_log`/`GET /admin/v1/merchant/usage`：token、调用次数、失败次数；dev 当前
  137 次调用、输入 21,139 tokens、输出 236 tokens、0 失败。只有 token，没有金额。
- 转人工：`customer_conversation.state` 与 `customer_event` 可提供
  `WAITING_HUMAN`/handoff 计数；当前没有聚合 API。dev 现有 160 个
  `WAITING_HUMAN` session，仅为测试残留，不能作为演示指标。
- `eval/run.sh offline` 只打印通过/数字，`live` 只打印有序步骤；质量 runner 各自写
  JSON 报告，没有单一“演示店铺报告”入口。

## 关键口径冲突

ROADMAP M9 写“每千次成本”，但 0021 与项目红线禁止界面/统计出现货币金额。
本阶段推荐把报告项改为“每千次问答的 token 用量”：输入 tokens、输出 tokens、
调用次数、失败次数，并可附 `by_slot`；不出现货币符号、汇率或价格表。分母在
0035 中固定为同一报告窗口内的用户问题数；不以金额做门禁。

## 调研与决策

完整对照见 [demo-and-report](../research/demo-and-report.md)，决策见
[0035](../decisions/0035-demo-report.md)（状态：提议）。

## 验收命令

运行 `check.sh` 的文档守卫四项（docs-links、docs-drift、doc-paths、decisions-index），
再跑快速层 40 项。另需确认 0035 编号不撞车，WP34、0035 与两个 INDEX 互相链接。

## 不做

- 不实现演示脚本、报告 CLI、管理台页面或数据库表；不改服务、迁移、评测集或冻结语料。
- 不出货币金额；不改 facts provider；不做真实商家数据验收；不调棘轮/基线。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

- 调研：[demo-and-report](../research/demo-and-report.md)，对照 Dify、RAGFlow、Ragas、
  LangSmith、Promptfoo，含可借鉴约束、不采用理由和外部来源链接。
- 决策：[0035](../decisions/0035-demo-report.md)，状态“提议”；定义新
  `demo-store-v1` 合成包、0017 演示身份、一键/重置形态、报告五段结构、指标来源、
  每千次 token 口径和可复现要求。
- 关键口径：M9 的“每千次成本”改为每千次问答的输入/输出 tokens、调用次数、
  失败次数；不出现货币。
- 语料结论：不直接复用跨 tenant 的 `ecommerce-demo-v1`，不改冻结的
  `ecommerce-m2-aligned-v1`；缺 typed products/size chart、演示问题集、一键脚本和
  customer handoff 聚合接口，均留给阶段 2。
- 文档守卫四项 PASS；快速层见交付汇报。未写代码、脚本、迁移、表或页面。

## 未覆盖路径

- 未实现演示包、一键脚本、报告 CLI、管理台只读页或 customer handoff 只读接口。
- 未运行真实模型或浏览器；未验证 Dify/RAGFlow 的演示数据细节，只按官方文档名称
  对照并登记来源。
- 未做真实商家数据验收；未改冻结评测集、facts provider 或自动发布/下架。
- 候选链接未逐个抓取正文；外部工具不作为依赖。
