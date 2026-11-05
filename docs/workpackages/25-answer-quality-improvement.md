# 25 答案质量改进：修 false refusal 与答非所问

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[metrics](../metrics.md)、[0028](../decisions/0028-orchestration-boundary.md)、
> [0025](../decisions/0025-code-level-rules.md)、[WP15](15-prompt-assets.md)、
> [WP24](24-answer-quality-eval.md)

## 目标

针对 WP24 的 11 个 wrong 与 4 个 partial，先定性三名“有依据却拒答”案例，
再修提示词/门控，最后用同一 LLM-judge 重评；目标 wrong 率下降且 correct 不减少，
更差时如实报告。不改冻结评测集/答案点、检索/嵌入/索引或门禁阈值。

## 口径

- 先查证据：从 `.local/wp22/bailian-batch.json` 的 `generation_evidence` 与 revision
  corpus 回查；结论分「检索没召回」「召回了但 generation 没看见」「fail-closed 误触发」。
- 若根因是检索漏召，停止，不在这里改检索；若证据本身不含答案点，不编造，记录为
  数据/冻结输入缺口。
- 提示词修改遵守 WP15：先升 `python/app/graph/prompts/registry.py` 版本，再改文本，同时更新
  `python/tests/test_prompt_matrix.py` 与新增 ≥12 个反例样本。
- `wrong_answer_rate` 只由 LLM-judge 语义评审给出；partial 单列，不并入 wrong。
- 真实模型重评限流 `--max-requests 300`、`30 RPM`，逐次写 usage_log；不改 0021 口径。

## 计划修复面

- 公开类拒答：`syn-025`、`syn-027`、`syn-028`，修改外部来源能力片段，要求“先给已有资料规则/摘要，
  再说明缺口”，不编造具体公告。
- 答非所问：`syn-017`、`syn-029`、`syn-031`、`syn-046`，收紧 CLARIFICATION_WORKFLOW/needs_plan 与
  delivery/external 能力提示，避免事实问句被改写成订单/通用流程。
- 格式/字段：`syn-037` 与 partial `012`、`020`、`040`、`054`，强化 limits/plan/schema
  必填字段和关键字段枚举。
- `syn-007`、`syn-019`、`syn-026` 先定性；若属历史/数据缺口，只记录不硬改。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
# 真实 batch（新输出路径，不覆盖 WP22 证据）
python/.venv/bin/python eval/run_bailian_batch.py --live --max-requests 240 --max-requests-per-minute 60 ...
python/.venv/bin/python eval/judge_answer_quality.py --report <new report> --captures <new captures> --output .local/wp25/answer-quality.json
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不改冻结评测集/答案点；不改检索/嵌入/索引；不重采 smoke 基线；
- 不把 judge 结果当门禁阈值；不把 LLM-judge 写成人工复核；
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 三例证据类定性（只调查）

| case | evidence | 结论 |
| --- | --- | --- |
| syn-007 | 8 chunks，含 `aligned-product-storage-v1` 的尺寸段和“晴野蓝色收纳箱规格”标题段 | **召回了、generation 没看见/误触发**：蓝色与尺寸都在证据里；batch 没有前文实体，模型仍拒答。提示词可缓解，但无历史时不能保证指代成功。 |
| syn-019 | 8 chunks，含 `SKU-EAR-200` 库存快照与澄清规则 | **输入缺少前文**：batch harness 没有传 `quality-scenarios.v2` 的前置对话；模型按无历史澄清是正确行为。不改检索/不猜实体。 |
| syn-026 | 8 chunks，含 USB-C 通用规则但无具体标准版本、日期和接口 | **语料/答案点缺口**：检索命中了相关文档，但文档本身没有答案点要求的细节；fail-closed 正确，不用提示词编造。 |

结论：syn-026 不是检索漏召而是证据内容不足；syn-019 是 batch 未带前文；
syn-007 是 generation 误判可用证据。后两类没有靠提示词强行改写为“正确”。

## 实际交付

- 公开类、答非所问、格式字段的提示词已更新，registry 版本：
  `generation_base=v2`、`generation_plan=v2`、`generation_limits=v2`、
  `generation_delivery=v2`。
- prompt-matrix 原有 9 个样本，新增 **12 个**反例样本（公开 4、答非所问 4、
  格式/partial 4），参数样本共 21；另有 registry 版本断言。`pytest` 为
  **418 passed / 30 skipped**。
- 新 batch（新输出/新 capture）236 请求 PASS；新 judge 53 请求。
- 结果对比：

| 指标 | WP24 v1 | WP25 v2 |
| --- | ---: | ---: |
| correct | 38 | **39** |
| partial | 4 | 7 |
| wrong | 11 | **7** |
| wrong_answer_rate | 20.75% | **13.21%** |
| partial_rate | 7.55% | 13.21% |

- 逐例 verdict 变化：

| case | 旧 | 新 | 备注 |
| --- | --- | --- | --- |
| syn-008 | correct | partial | 未直接回答哪把更适合海边 |
| syn-012 | partial | correct | 低/高款区分已补 |
| syn-017 | wrong | partial | 仍缺“配送区域/承诺时效接口” |
| syn-025 | wrong | correct | 先给已有规则与缺口 |
| syn-027 | wrong | correct | 报道摘要不再整段拒答 |
| syn-031 | wrong | correct | 指出缺标准名称/版本/日期 |
| syn-043 | correct | partial | 缺“按来源等级和授权处理” |
| syn-052 | correct | partial | 缺“标记冲突/不隐藏证据” |

- 仍非 correct：syn-007/019/026（上述定性）、syn-028、syn-029、syn-037、syn-046
  （wrong）与 syn-017/020/040/043/052/054/008（partial）。原因见
  `.local/wp25/answer-quality.json`。
- usage_log（eval DB）：WP25 新 batch 236 行 / 140833 输入 / 4350 输出；
  judge 53 行 / 12173 输入 / 2263 输出；累计行数 289 → 578。
- 真实模型成本：WP25 共 236 + 53 = 289 次 Provider 请求；token 为真实响应值。
- 未被 retry 改变的 smoke：`--smoke` 45 逐字节一致；`check.sh` 40 PASS。

## 未覆盖路径

- wrong 率从 20.75% 降至 13.21%，correct 从 38 增至 39；但 **syn-008/043/052 从
  correct 退为 partial**，属于逐例 regression，不能只说总数改善。
- syn-007/019 的根因包含 batch 未带历史；syn-026 是冻结语料缺答案点细节。
  本轮没有改检索、没有编造事实，也没有强行把这些判成 correct。
- 真实模型成本：WP25 236 次 batch + 53 次 judge，不含此前 WP22/WP24 的调用。
- LLM-judge 仍是语义评审，不是人工；judge 有模型偏差，partial 单列不做门禁。
- 未重跑 7 条 safety、HTTP/SSE 53 例；未改冻结集/答案点/检索；未引依赖；未 push。
