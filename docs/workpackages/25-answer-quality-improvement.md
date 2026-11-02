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
- 提示词修改遵守 WP15：先升 `prompts/registry.py` 版本，再改文本，同时更新
  `python/tests/test_prompt_matrix.py` 与新增 ≥12 个反例样本。
- `wrong_answer_rate` 只由 LLM-judge 语义评审给出；partial 单列，不并入 wrong。
- 真实模型重评限流 `--max-requests 300`、`30 RPM`，逐次写 usage_log；不改 0021 口径。

## 计划修复面

- 公开类拒答：`syn-025/027/028`，修改外部来源能力片段，要求“先给已有资料规则/摘要，
  再说明缺口”，不编造具体公告。
- 答非所问：`syn-017/029/031/046`，收紧 CLARIFICATION_WORKFLOW/needs_plan 与
  delivery/external 能力提示，避免事实问句被改写成订单/通用流程。
- 格式/字段：`syn-037` 与 partial `012/020/040/054`，强化 limits/plan/schema
  必填字段和关键字段枚举。
- `syn-007/019/026` 先定性；若属历史/数据缺口，只记录不硬改。

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
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
