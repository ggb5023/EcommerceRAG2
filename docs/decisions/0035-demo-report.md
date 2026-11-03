# 0035 演示店铺与评测报告

- 状态：已采纳
- 日期：2026-11-03
- 工作包：[34](../workpackages/34-m9-demo-report-design.md)
- 调研：[demo-and-report](../research/demo-and-report.md)
- 关联：[0021](0021-usage-log-instead-of-budget.md)、[0012](0012-agent-delegated-evaluation.md)、
  [0015](0015-agent-review-owns-quality-in-development.md)、[0034](0034-product-table-import.md)

## 1. 目标与边界

M9 要让演示店铺可一键启动、可重置，并产出同一合成数据上可复现的评测报告。
本记录只定演示包、启动形态、报告口径与可复现要求；阶段 2 才实现脚本、接口和页面。

明确不包含：真实商家数据验收、货币成本、自动发布/下架、修改冻结评测集、
新增检索/生成模型或第二套演示身份。

## 2. 演示店铺

### 2.1 数据包

新建 `data/synthetic/demo-store-v1/`（待建），作为唯一演示输入：

- `manifest.yaml`：单一 tenant/shop、固定 business_date、`synthetic=true`、
  `provisional=true`、`license=internal-generated`；
- `products.csv`：M8 `product_table_v1`，至少 7 个 SKU，覆盖颜色/尺码/容量；
- `size-chart.csv`：M8 `size_chart_v1`，与商品行 `size_chart_code` 对应；
- `policies.md`、`faq.md`、`returns.md`：发货、退换、护理和常见问题；
- `questions.jsonl`：演示问题、期望行为（ANSWER/CLARIFY/HANDOFF）和答案点；
  这是新的合成输入，**不写入 `eval/` 的冻结 53 例**。

不从 `ecommerce-demo-v1` 直接启动：它跨 2 tenant/3 shop，且商品 manifest 仍是旧
`doc_type: product`。不修改 `ecommerce-m2-aligned-v1`；它是冻结评测语料。
`customer-service-v1` 的 care/returns/FAQ 可作为新包的合成素材，但不直接作为包。

### 2.2 身份与发布状态

固定使用 0017 的 `v2-demo-tenant`、`dev-user`、`shop-demo`；不新增演示身份。
启动后通过现有管理 HTTP API 完成上传、预检、确认、审核和发布，目标状态：

- 演示包的全部内容文档 `status='active'`、`review_status='approved'`、
  `active_version_id` 指向本次版本；
- 无 pending 演示版本；源哈希与 manifest 哈希可查询；
- 商品表/尺码表使用 0034 的类型化行与 chunk 投影，事实工具仍走 facts provider，
  不把商品表冒充实时库存/价格。

### 2.3 一键启动与重置

阶段 2 提供单个 wrapper（名称暂定 `scripts/dev/demo-shop.sh`，待建）：

1. 启动隔离栈（复用 `isolated-stack.sh` 的 development identity 与固定端口）；
2. 通过管理 API 导入 `demo-store-v1`、确认、审核、发布；
3. 校验 active 文档/版本数、business_date、无 pending；
4. 重复执行幂等：同源哈希跳过，源变化生成新版本，不覆盖历史；
5. `--reset` 只撤回/软删除本演示 source 的未发布和已发布版本，不 DROP 共享库，
   不删除其他租户或测试数据。

启动失败必须非零退出并保留诊断；不能以“已有任意 pending/active 数据”冒充成功。

## 3. 评测报告

### 3.1 输出形态

阶段 2 先做 CLI（暂定 `scripts/dev/demo-report.py`，待建），输出 JSON + Markdown；
管理台后续只读取同一份报告，不重算指标。报告写入受限目录，权限 0700/0600，
不改业务表，不写冻结评测集。

报告由五段组成：

1. **输入**：git commit、演示包 sha256、manifest/version id、business_date、
   人群（workspace/customer）、窗口、冻结评测报告 sha256；
2. **运营结果**：workspace 的 `outcomes` 字段（`turns_total`、`scored_total`、
   `solved`、`clarified`、`abandoned`、`failed`、`cancelled`、`pending`、`unknown`、
   `auto_rate`、`assisted_rate`）与独立的 customer handoff 段；
3. **质量**：冻结 53 例的 `wrong_answer_rate`（当前 13.21%），标注
   `reviewer_kind=agent`、`human_review_completed=false` 与来源报告 sha256；
4. **转人工**：customer 通道的 handoff 计数与分母；
5. **token**：`usage_log` 的输入/输出 tokens、调用次数、失败次数及每千次归一值。

### 3.2 指标口径

- **自动解答率/无需人工介入率**：只使用 `docs/metrics.md` 已定义的 workspace
  `outcomes` 口径；不把 customer 的 handoff 混进 `scored_total`。
- **错答率**：只能来自冻结 53 例的 LLM-judge（或未来经批准的人工标注）；
  不从 `DONE` 推断，不用演示对话临时生成"正确率"。没有 judge 报告时输出
  `NOT_MEASURED`，不输出 0。
- **转人工率**：`handoff_rate = 窗口内发生过 WAITING_HUMAN 转移的 customer
  conversation 数 / 窗口内至少有 1 条 customer 消息的 customer conversation 数`；
  分母为 0 返回 null。字段来源为 `customer_conversation` 与 `customer_event`；
  当前没有聚合 API，阶段 2 才补只读查询。
- **每千次 token**：`input_tokens / question_count * 1000`、
  `output_tokens / question_count * 1000`、`calls / question_count * 1000`，
  另列 `failed_calls`。`question_count` 是该报告运行的固定演示问题数；
  `usage_log` 按 tenant/shop/窗口取数，并输出 `by_slot`/`by_model`。
  报告不得出现货币符号、汇率或价格表。

### 3.3 与冻结评测的关系

冻结 53 例仍是质量唯一可比较基线；演示数据与演示问题集是独立合成输入。
报告引用冻结评测的 `wrong_answer_rate` 和 report sha256，但不修改、重采样或
重写冻结用例。演示问题集的答案与评审不能替代冻结评测结论；两者都在报告中
分节，禁止合成一个总分。

### 3.4 可复现要求

- 固定 business_date、演示包 sha256、version id、问题集 sha256、git commit；
- 相同输入两次报告，计数、分母、比率一致；token 数允许模型调用差异，但必须
  在报告中记录两次值和差异原因；
- 缺数据、缺 judge、缺 customer handoff、分母为 0 时 fail-closed：写
  `NOT_MEASURED`/null，不推断；
- 报告保持 `real_service_acceptance=false`，只声明合成演示；
- 报告不得写正文、prompt、顾客原文、凭据、DSN 或货币。

## 4. 不做

- 不做货币成本、金额换算或价格表；不把“每千次成本”作为门禁。
- 不修改冻结 53 例、答案点、对齐或 `ecommerce-m2-aligned-v1`。
- 不做真实商家数据验收、平台 API、自动发布/下架、实时库存/价格。
- 不新建演示身份、不复制第二套导入/审核/发布管线。

## 5. 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 直接复用 `ecommerce-demo-v1` 启动 | [ecommerce-demo-v1 README](../../data/synthetic/ecommerce-demo-v1/README.md) | 跨 2 tenant/3 shop，商品仍是旧 `doc_type`，没有 typed size chart；会污染 dev 的其他测试数据 |
| 直接用 `ecommerce-m2-aligned-v1` 作为演示店铺 | [RAGFlow 数据集示例](https://github.com/infiniflow/ragflow) | 它是冻结评测语料（37 Markdown），没有 typed CSV/尺码表；为演示改它会破坏冻结评测 |
| 只做管理台页面，不做 CLI | [Dify 控制台示例](https://docs.dify.ai/) | 无法在 CI/脚本中复现，也无法固定输入哈希和两次报告对比；先 CLI 才能证明可复现 |
| 继续输出“每千次货币成本” | [LiteLLM 价格表](https://docs.litellm.ai/docs/proxy/model_cost_map) | 违反 0021 与项目红线：价格表追不上账单，且界面不得出现货币金额 |
| 引入 Ragas/LangSmith 作为评测依赖 | [Ragas](https://docs.ragas.io/)、[LangSmith](https://docs.smith.langchain.com/) | 会与 0012/0015 的 agent 评审、冻结 53 例和现有 quality gate 形成第二套口径；只借报告结构，不引入服务或依赖 |

## 6. 责任与状态

- **阶段 2 实现**：演示包、一键脚本、报告 CLI、只读 customer handoff 聚合；
  不改变既有导入/审核/发布写入方。
- **商家/操作者**：启动、重置、确认报告；不是自动发布或自动验收。
- **本记录**：已采纳；WP35 实现 §2，WP36 实现 §3 报告与 handoff 聚合。
