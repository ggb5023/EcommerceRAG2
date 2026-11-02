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

- 真实 batch：`run_bailian_batch.py --live` **PASS**；53 可评分、7 排除（7 条未执行，见限制）；
  `actual_request_count=236`，`limits={max_requests:240,max_requests_per_minute:60}`；
  输出 `.local/wp22/bailian-batch-usage.json`，捕获在仓外 0700 目录。
- usage_log（eval DB）：基线 0 行 → **236 行**；输入 **132439** tokens、输出 **4177**
  tokens、失败 0。分槽：embedding 130 行 / 50321 输入；rerank 53 / 57832 输入；
  generation 53 / 24286 输入 / 4177 输出。
- 真实检索（batch 的 `rerank_top5`）：Recall@5 **1.000000**、MRR **0.981132**、
  nDCG@5 0.986073；`embedding_top5` 为 Recall@5 0.962264、MRR 0.884277。
- aligned-53 离线词法检索：Recall@5 **0.981132**、MRR **0.981132**、nDCG@5 0.981132；
  与批次的 rerank 对比：Recall 更高（1.0 vs 0.981），MRR 持平；embedding 更低。
- 错答率：batch 的 `answer_quality_status=NOT_RUN`；没有人工/agent 语义复核，
  因此**不能给出错答率**。精确答案点匹配诊断为 0/53（全部
  `generation_rewrite_mismatch`），属于字符串/改写诊断，不作为质量结论。
- M6 真实建议（`新区配送需要核对哪些信息？`）：create **202**，turn **DONE**，
  `customer_reply.can_copy=true`、`blocked_reason=""`、citations **1**，
  copy **200**、copy citations **1**；完整响应在 `.local/wp22/m6-suggestion-delivery.json`。
  真实 Provider 栈启动在临时端口，验证后已停止并恢复 dev（当前 profile mock）。
- 前端未改：`npm test` **49 passed**，`npm run build` **79.54KB** gzip。
- 门禁：`check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**；`--smoke` **45 PASS**。

## 未覆盖路径

- **真实模型成本**：第一次 batch（236 请求）因 usage DSN 未正确导出而没有记入 usage_log，
  修正后重跑 236 请求；加 3 条 M6 建议，实际约 **484 次 Provider 请求**（以平台账单为准）。
  不要把 236 行 usage_log 误读成总调用量。
- **错答率未测**：无人工/agent 复核；`answer_quality_status=NOT_RUN`，不把
  `DONE` 或精确字符串匹配当成正确率。
- M6 的 AI 子进程由 supervisor 启动时未获得 usage DSN，故 M6 调用没有新增
  `usage_log` 行；上面 236 行只对应 batch。这是本轮实测到的记录缺口。
- 冻结 60 例中只执行了 53 条可评分 batch；7 条 safety 预拒绝用例需要 live lifecycle/e2e 报告，本轮未跑，
  因此不能宣称“60 例全部通过”。
- 未跑完整 live lifecycle（37 文档重导入/发布）、HTTP/SSE 53 例和 7 条 safety HTTP；
  M6 使用开发库现有 37 个 active 文档与临时真实 Provider 栈。
- 临时真实栈使用 dev 库，写入过 M6 建议会话；dev 服务已恢复为 mock profile。
- 未改评测集/真值/对齐/契约、代码、棘轮/基线/smoke；未 push。
