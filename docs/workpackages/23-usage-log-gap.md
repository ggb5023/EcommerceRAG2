# 23 修复线上回答路径的 usage_log 缺口（0021 违例）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0021](../decisions/0021-usage-log-instead-of-budget.md)、
> [environment-facts](../environment-facts.md)、[WP22](22-live-validation.md)

## 根因（开工复现）

- `graph/runtime.py` 构造真实 Provider 时调用 `build_provider(config)`；其签名默认
  `record_usage=True`，`aliyun_bailian.py` 也确实用 `UsageRecorder.from_environment(role="ai")`。
  **不是**“构造时漏传 record_usage”。
- `usage.py.from_environment` 要求 `V2_USAGE_DSN` 或 `V2_AI_DSN`；当前 dev ai 子进程
  `/proc/<pid>/environ` 两者都不存在。`scripts/dev/v2_services.py` 的 AI service 键集合
  未包含这两个键，导致真实 Provider 的 `self.usage=None`，静默丢用量。
- `workflow.scoped` 把 `scope["tenant_id"]`（租户别名，如 `v2-demo-tenant`）传给 provider；
  `usage_log.tenant_id` 是 BIGINT，scope 里真正可用的是 `tenant_db_id`。即使 DSN 补上，
  别名也会造成写入失败/错误租户。
- WP22 的真实 M6 临时栈没有新增 usage 行，已作为第一次复现证据。
- `eval/run_bailian_batch.py` 首次 live 运行因 eval 环境未导出 `V2_AI_DSN` 而静默丢 236 行；
  运行器没有 fail-loud。

## 修复口径

1. **环境传递**：`v2_services.py` 把 `V2_USAGE_DSN` / `V2_AI_DSN` 纳入 AI 子进程环境；
   `configure` 在两者缺失时回退到 `V2_RETRIEVER_DSN`（同一 AI 角色库），不硬编码 DSN。
2. **租户贯穿**：`workflow.scoped` 优先把 `scope["tenant_db_id"]` 传给 provider 的
   `tenant_id` 参数，别名仅作为缺失回退；provider 的 `_record_usage` 因此写入数字租户。
3. **显式构造**：`load_provider_service` 显式传 `record_usage=True`，把默认值变成契约。
4. **fail-loud**：`eval/run_bailian_batch.py --live` 在发出任何请求前检查
   `UsageRecorder.from_environment(role="ai")`；缺失即 `usage_dsn_required` 失败，不静默丢用量。
5. ingest 侧继续 `role="ingest"` / `V2_INGEST_DSN`，不改；在线与 ingest 各自独立记一次。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
# 重启 ai 后，追加 V2_AI_DSN 并启动真实 Provider，触发一次 /v1/support/suggestions
# 然后查询 usage_log 的 tenant_id/slot/token
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不改 `usage_log` 表结构/口径；不调棘轮/基线；不重采 smoke；
- 不做 WP24 错答率评测；不实现顾客端 `can_copy` 统一；
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
