# 演示店铺与评测报告：成熟方案对照与本仓库盘点

> 权威等级：context　日期：2026-11-03　工作包：[34](../workpackages/34-m9-demo-report-design.md)
> 结论落在 [0035](../decisions/0035-demo-report.md)。外部产品按名称检索官方文档；
> 正文不写外链 URL，来源链接集中在本文件最后一节，沿用 [frontend-references](frontend-references.md) 的约定。

## 1. 结论摘要

演示店铺应是一个**固定合成包 + 幂等 seed + 明确发布状态**，而不是复用 dev 库里的
测试残留。评测报告先做 CLI（JSON + Markdown），把“可复现的输入哈希、指标口径、
数据来源和失败关闭”写清楚；管理台页面只读取同一报告，不另算一套指标。

报告中的“每千次成本”必须按 0021 改为“每千次问答的 token 用量”：
输入 tokens、输出 tokens、调用次数、失败次数，按问题数归一；不出现任何货币符号、
汇率或价格表。

## 2. 成熟方案对照

| 候选（检索名） | 可借鉴的约束 | 不采用为依赖的理由 |
| --- | --- | --- |
| Dify | 创建示例应用、上传示例数据集、对话/命中测试的“开箱即用”路径 | 通用知识库平台，不是本仓库的导入/审核/发布管线；引入会复制一套数据模型与运维面。只借演示入口和示例数据的组织方式 |
| RAGFlow | 数据集/示例文档、解析预览、chunk 命中与引用查看 | 完整 RAG 平台，表格/解析器配置对商家过重；没有本仓库的版本审核、发布状态和 token 报告语义。只借预检/引用交互 |
| Ragas | 把答案正确性、上下文覆盖、引用支持拆成独立评测维度并输出结构化结果 | 引入新的评测依赖和判定实现，会与 0012/0015 的 agent 评审及冻结 53 例契约冲突；本仓库已有 LLM-judge + quality_gate。只借指标命名与报告分节 |
| LangSmith | 运行级 trace、评测报告与对比视图 | SaaS/账号依赖，报告可能离开自部署边界；本仓库要求离线可复现、报告只含 id/hash/计数。只借“输入、指标、逐例结果、差异”四段式 |
| Promptfoo | 命令行评测、结果 JSON、对比基线 | 只解决单次 prompt 评测，不覆盖导入、发布、审核和 customer handoff；本仓库的 eval/run.sh 已负责运行步骤。只借 CLI 报告结构 |

说明：Dify/RAGFlow 的价值在“一键体验 + 预览 + 引用”，不在其通用知识模型；Ragas/
LangSmith/Promptfoo 的价值在“报告结构和可比较性”，不在新增依赖。本仓库的演示店铺
必须复用现有 Go 授权、Python ingest、审核/发布和 usage_log，不另起平台。

## 3. 演示店铺形态

推荐一个独立的合成包 `data/synthetic/demo-store-v1/`（待建）：

- `manifest.yaml`：单一 tenant/shop，固定 business_date，显式披露级别；
- `products.csv` + `size-chart.csv`：M8 typed `product_table_v1`/`size_chart_v1`；
- `policies.md`、`faq.md`、`returns.md`：售后、发货、退换和常见问题；
- `questions.jsonl`：演示问题与期望行为/答案点（合成，不写入冻结评测集）；
- 所有文件 `synthetic=true`、`provisional=true`、`license=internal-generated`。

现有语料不能直接当演示店铺：

- `ecommerce-demo-v1` 跨 2 tenant/3 shop，商品 manifest 仍是旧 `doc_type: product`，
  没有 typed size chart；
- `customer-service-v1` 没有 manifest，只有 4 个客服文档；
- `ecommerce-m2-aligned-v1` 是冻结评测语料，37 个 Markdown、无 typed CSV/尺码表，
  不能为了演示修改它。

一键启动推荐一个 wrapper（阶段 2 实现）：

1. 启动隔离栈（现有 `isolated-stack.sh`，development identity、固定端口）；
2. 通过管理 HTTP API 上传 `demo-store-v1`、确认预检、审核并发布；
3. 校验 active 文档/版本数、固定 business_date、无 pending；
4. `--reset` 只清理该演示 source/版本（走撤回/软删除），不 DROP 共享库；
5. 重复执行必须幂等：源哈希相同则跳过，源变化则生成新版本，不覆盖历史。

演示身份固定为 0017 的 `v2-demo-tenant`、`dev-user`、`shop-demo`；不新建第二套演示
身份。当前 dev 库的 40 个 pending 文档、版本 93 的撤回状态只是测试残留，不能作为
演示初始化状态。

## 4. 报告形态

推荐 CLI 为唯一计算入口（阶段 2）：`scripts/dev/demo-report.py`（待建；名称实现时定稿），
输出 JSON + Markdown；管理台只读同一 JSON/只展示，不重算。报告分成五段：

1. **输入**：git commit、演示包 sha256、manifest/version id、business_date、人群
   （workspace/customer）、窗口、冻结评测版本；
2. **运营结果**：复用 outcomes 的 `scored_total`、`solved`、`clarified`、`abandoned`、
   `failed`、`cancelled`、`pending`、`unknown`、`auto_rate`、`assisted_rate`；
3. **质量**：`wrong_answer_rate` 只来自冻结 53 例的 LLM-judge，标注
   `reviewer_kind=agent`、`human_review_completed=false`、报告 sha256；不从 DONE 推断；
4. **转人工**：customer 通道 `customer_conversation`/`customer_event` 的 handoff 计数
   与分母，和 workspace 的 `abandoned` 分栏，不混算；
5. **token**：`usage_log` 按 slot/model 聚合输入/输出 tokens、调用次数、失败次数，
   再按问题数算每千次；不出现货币。

现有 `eval/run.sh offline` 只输出评测通过/数字，`live` 只打印有序步骤；各 runner 写
各自的 JSON，没有“演示店铺”聚合报告。`outcomes` 只读 workspace，customer handoff
没有聚合 API；`wrong_answer_rate` 没有 API，当前唯一实测是冻结 53 例的 13.21%。这些
是阶段 2 要补的只读接口/报告层，不是本轮实现。

## 5. 报告可复现要求

- 固定 `business_date`、数据包 sha256、seed/version id 和 git commit；
- 两次报告在相同输入上，计数和比率必须相同；时间戳字段不参与比较；
- 指标分母为 0 时输出 null，不把 null 写成 0；
- 报告失败关闭：缺 usage_log、缺 judge 报告、缺 customer handoff 数据时输出
  `NOT_MEASURED`，不推断、不补数；
- 任何报告保持 `real_service_acceptance=false`，不声称真实商家验收；
- 冻结 53 例、其答案点和对齐文件不得为演示报告修改；演示问题集是新的合成输入。

## 6. 对 0035 的输入

- 演示包用新 `demo-store-v1`，不直接复用跨 tenant 的 `ecommerce-demo-v1`，不修改
  `ecommerce-m2-aligned-v1`；
- 一键脚本只走 HTTP API 完成导入/审核/发布，幂等并可重置；
- 报告 CLI 先行，管理台只读；指标分人群，质量与运营分栏；
- 每千次“成本”改为每千次 token 用量，禁止货币；
- 报告与冻结评测只共享“质量结果引用”，不共享可变的演示数据。

## 7. 外部来源（按名称检索）

| 名称 | 链接 |
| --- | --- |
| Dify | https://docs.dify.ai/ |
| RAGFlow | https://github.com/infiniflow/ragflow |
| Ragas | https://docs.ragas.io/ |
| LangSmith | https://docs.smith.langchain.com/ |
| Promptfoo | https://www.promptfoo.dev/ |

这些链接只用于核对名称对应的官方文档；正文与决策不复制外部 URL，避免仓库出现
无意义的外部路径和版本漂移。
