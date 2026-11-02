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

（完成后填写）

## 未覆盖路径

（完成后填写）
