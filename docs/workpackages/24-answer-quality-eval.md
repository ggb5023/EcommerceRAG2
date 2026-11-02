# 24 真实错答率语义评测（LLM-judge）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[metrics](../metrics.md)、[0012](../decisions/0012-agent-delegated-evaluation.md)、
> [0015](../decisions/0015-agent-review-owns-quality-in-development.md)、
> [WP22](22-live-validation.md)

## 目标

对 WP22 的 53 例真实 generation 答案做逐例语义评审，输出
`correct / partial / wrong` 与一句理由，并计算 `wrong / 53`。
partial 单列，不并入 correct 或 wrong；不改冻结答案点，不把结果当门禁阈值。

## 数据源

- 答案：`.local/wp22/bailian-batch.json` 的 `case_results` + 对应 capture；
  capture 由 `--captures` 指定或自动在
  `/var/lib/ecommerce-rag/real-docs/captures-wp22-*` 中按 capture_id 查找。
- 问题与参考要点：冻结 revision 的
  `synthetic-m2-v1-revision-1.jsonl`；按 `case_id` 绑定，不修改。
- 答案以 capture 的 `structured.answer_points` 为准；capture 的 SHA-256 必须与
  batch 报告一致。

## Judge 口径

- 固定系统提示词与用户模板；`temperature=0`；输出 JSON
  `{"verdict":"correct|partial|wrong","reason":"一句中文理由"}`。
- 每个 case 一次 judge 调用（Provider 内部瞬时重试另计）；`--max-requests 300`、
  `--max-requests-per-minute 30`。每次调用使用真实 Provider 的 `generation` slot，
  由 `UsageRecorder` 写入 `usage_log`；judge 没有数据库租户，`tenant_id=NULL`（unattributed）。
- `wrong_answer_rate = wrong_count / 53`；`partial_count` 单独输出。
- 任何 schema/parse/调用失败都 fail-loud，不把错误当 wrong 或 correct。

## 验收命令

```bash
python/.venv/bin/python eval/judge_answer_quality.py
# 默认输出 .local/wp24/answer-quality.json
python/.venv/bin/python -m pytest -q eval/test_judge_answer_quality.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不改评测集/答案点/真值；不把 judge 结果当门禁阈值；不调棘轮/基线；不重采 smoke；
- 不做 WP24 之外的模型质量声明；不把 LLM-judge 说成人工复核；
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

- 新增 `eval/judge_answer_quality.py`：固定提示词、`temperature=0`，逐例输出
  `correct/partial/wrong` + 一句理由；输出 `.local/wp24/answer-quality.json`（0600）。
- 数据绑定：`.local/wp22/bailian-batch.json` 的 53 个 capture（SHA-256 校验）+
  冻结 revision 的问题/答案点；capture 查询哈希与 case 绑定校验通过。
- 真实 judge：模型 `qwen3.7-plus`，`temperature=0`，53 次请求，
  `limits={max_requests:300, max_requests_per_minute:30}`；未超限。
- 结果：**correct 38、partial 4、wrong 11**；
  `wrong_answer_rate = 11/53 = 20.754717%`，`partial_rate = 4/53 = 7.547170%`。
  partial 单列，不并入 correct 或 wrong。
- usage_log（eval DB）：judge 前 236 行 / 132439 输入 / 4177 输出 →
  judge 后 **289 行 / 144316 输入 / 6420 输出**；新增 **53 行 / 11877 输入 / 2243 输出**，
  `slot=generation`、`tenant_id=NULL`（judge 无数据库租户，unattributed）。
- 非正确逐例摘要：

| case | 判定 | 理由摘要 |
| --- | --- | --- |
| syn-007 | wrong | 颜色未明确时拒答，但有尺寸依据，应指代前文型号 |
| syn-012 | partial | 只讲尺寸，未说明高低款用途 |
| syn-017 | wrong | 未答配送区域/时效接口，转到订单号 |
| syn-019 | wrong | 未沿用上一轮实体并查库存，错误澄清 |
| syn-020 | partial | 未提价格快照与业务规则 |
| syn-025 | wrong | 有公开来源要求却错误拒答 |
| syn-026 | wrong | 未答 USB-C 标准变化，错误拒答 |
| syn-027 | wrong | 未总结行业报道，错误拒答 |
| syn-028 | wrong | 未答儿童防晒成分，错误拒答 |
| syn-029 | wrong | 只给方法论，未答规则更新/日期/范围 |
| syn-031 | wrong | 未答最新版本，给通用方法论 |
| syn-037 | wrong | 未提弱外部需求只补检一次 |
| syn-040 | partial | 遗漏“公开或店内范围”，替换为配送范围 |
| syn-046 | wrong | 未先澄清日期/地区/对象，答非所问 |
| syn-054 | partial | 将“问题类型”弱化为“对象” |

- prompt SHA-256 与逐例理由见 `.local/wp24/answer-quality.json`；
  `review_method=delegated_agent_semantic_review_v1`，不是人工复核。
- 门禁：`python pytest` **405 passed / 30 skipped**；eval pytest **292 passed**；
  `check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**；`--smoke` **45 PASS**。

## 未覆盖路径

- 这是 LLM-judge 语义评审，不是人工复核；`human_review_completed=false`，不能写成
  “人工复核通过”。
- judge 的 tenant 是 NULL（unattributed）；53 次请求的 token 已记录，但没有商家租户归属。
- judge 结果不参与质量门禁阈值，不用于 WP24 之外的模型质量声明。
- 未改冻结答案点/评测集；未重跑 53 例 batch；未覆盖 7 条 safety 用例。
- 未改代码质量门禁/棘轮/基线；未重采 smoke；未 push。
