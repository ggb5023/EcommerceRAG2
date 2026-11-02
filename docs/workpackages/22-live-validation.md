# 22 真实模型质量验证（--live）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[eval/README](../../eval/README.md)、[0021](../decisions/0021-usage-log-instead-of-budget.md)、
> [metrics](../metrics.md)、[WP20](20-customer-mobile.md)

## 目标

用真实 Provider 跑冻结 60 例（53 可评分 + 7 预拒绝）与 aligned-53 检索，
记录 token 用量；用真实 M6 建议请求验证 `can_copy=true`、引用非空、复制 200。
不改评测集、真值、代码或棘轮/基线，不做真实模型之外的行为变更。

## 口径

- 真实模型调用只使用 `--max-requests` 与 `--max-requests-per-minute` 限流；
  `usage_log` 只记 token，不出现货币金额（0021）。
- 冻结 60 例的答案质量必须来自人工/agent 复核；没有复核时只能报告
  `answer_quality_status=NOT_RUN`，不得把 `DONE` 当正确。
- 检索指标与答案质量分开：aligned-53 的 Recall@5/MRR 与 batch 的生成结果
  不混算；更差如实写更差。
- 报告/捕获写入仓外 0700 目录或 `.local/`，报告 0600；不改评测输入。

## 运行计划

1. `run_bailian_batch.py --plan` 验证 53 例、7 排除、预计请求数。
2. `run_bailian_batch.py --live --max-requests 240 --max-requests-per-minute 60`
   跑 53 例；输出/检查点/捕获使用新路径，不覆盖历史报告。
3. 离线 `run_aligned_retrieval.py` 得到 aligned-53 的 Recall@5/MRR。
4. 真实 M6：在真实 Provider 服务上调用 `/v1/support/suggestions` 与 copy，
   记录 `customer_reply.can_copy`、citation 数、copy HTTP 状态。
5. 记录 `usage_log` 前后行数/token；贴 `check.sh` 与 `--smoke`。

## 验收命令

```bash
python/.venv/bin/python eval/run_bailian_batch.py --plan --max-requests 240 --max-requests-per-minute 60
# 真实运行（使用受保护 env；输出/捕获不覆盖历史文件）
python/.venv/bin/python eval/run_bailian_batch.py --live --max-requests 240 --max-requests-per-minute 60 ...
python/.venv/bin/python eval/run_aligned_retrieval.py --revision-root /var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional
bash scripts/check.sh
bash scripts/check.sh --smoke
cd frontend && npm test && npm run build
```

## 不做

- 不改评测集/真值/对齐/契约；不调棘轮/基线；不改代码（除非出现必须修复的
  bug，先停下汇报）；不 push；不重采 smoke；
- 不把 `answer_quality_status=NOT_RUN` 写成质量通过；
- 不把顾客端 `can_copy` 统一在本包实现（仍挂 `docs/refactor-input.md` #17）。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
