# 03 结果度量：自动解决率（B1）

> 状态：见 INDEX.md　适用提交：9bd72a0　验收：见 §验收命令
> 前置：02-turn-decision（state 列）、09-turn-decision-semantics（execution 键）

## 目标

给商家一个可读的结果分布：直接解决、澄清后解决、未解决、失败、已取消、进行中，
以及「直接解决率」与「无需人工介入率」。口径以 `docs/metrics.md` 为唯一出处，本文件不复制定义。

## 不碰

- 不得出现任何货币字段或货币符号（0021）。
- 不加新表、不加视图：纯 SELECT 聚合。
- 不改 turn / turn_decision 的写入路径。
- 不接 customer 通道。

## 验收命令

详见 docs/agent-entry.md §4。本包额外：

```bash
curl -s 'localhost:8080/admin/v1/merchant/outcomes?range=30d' | jq
# → {turns_total, scored_total, solved, clarified, abandoned, failed, cancelled,
#    pending, unknown, reasked_within_5m, auto_rate, assisted_rate, truncated, metric_gap}
```

与下面这条 SQL 的分桶结果逐项相符（`psql -v tenant_id=<id>`；手算校验，不要只信接口自己）：

```sql
WITH latest AS (
  SELECT t.id, t.conversation_id, t.parent_turn_id, t.created_at,
         d.state, COALESCE(e.finished_at, t.created_at) AS clock
  FROM turn t
  LEFT JOIN LATERAL (
    SELECT e.id, e.finished_at
    FROM conversation_execution e
    WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id
    ORDER BY e.execution_no DESC LIMIT 1
  ) e ON true
  LEFT JOIN turn_decision d ON d.tenant_id=t.tenant_id AND d.execution_id=e.id
  WHERE t.tenant_id=:tenant_id AND t.created_at >= now() - interval '30 days'
), classified AS (
  SELECT latest.*,
         EXISTS (
           SELECT 1 FROM latest child
           WHERE child.parent_turn_id=latest.id
             AND child.created_at > latest.created_at
             AND child.state='DONE'
         ) AS clarified
  FROM latest
)
SELECT
  count(*) AS turns_total,
  count(*) FILTER (WHERE state='DONE') AS solved,
  count(*) FILTER (WHERE state='ASKING' AND clarified) AS clarified,
  count(*) FILTER (WHERE state='ASKING' AND NOT clarified AND now()-clock >= interval '10 minutes') AS abandoned,
  count(*) FILTER (WHERE state='FAILED') AS failed,
  count(*) FILTER (WHERE state='CANCELLED') AS cancelled,
  count(*) FILTER (WHERE state='ASKING' AND NOT clarified AND now()-clock < interval '10 minutes') AS pending,
  count(*) FILTER (WHERE state IS NULL) AS unknown
FROM classified;
```

对账关系：

- `scored_total = solved + clarified + abandoned + failed + cancelled`
- `turns_total = scored_total + unknown + pending`
- `auto_rate = solved / (solved + abandoned + failed + cancelled)`；分母为 0 → null
- `assisted_rate = (solved + clarified) / scored_total`；scored_total 为 0 → null

## 未覆盖路径

- `pending` 是右删失：超过 10 分钟宽限后按 abandoned 计，顾客之后回来也不会回填。
- 子轮 DONE 落在窗口外或 `LIMIT 50000` 截断处，父轮会被判 abandoned 而不是 clarified。
- `/resume` 原地续接的 DONE 计入 solved，不产生 clarified（设计）。
- 前端 N=0 与截断提示仍无组件级测试；页面未人工视觉检查。
- 未接 customer 人群；`wrong_answer_rate` 仍只来自离线评测的口径定义，代码未实现。

## 实际交付 vs 计划

- 本轮口径落地：`auto_rate` 分母剔除 clarified；新增 `assisted_rate` 的 scored_total 定义；
  `pending`（10 分钟右删失）、`clarified`（parent_turn_id）、`cancelled`（从 failed 拆出）全部实现。
- 计划外：`OutcomesSummary.vue` 拆出展示层以便 SSR 渲染测试；`api.test.ts` 增加字段渲染用例；
  响应新增 `pending` / `cancelled` 字段；`TestResolutionSummarySQLReconciliation` 在
  test-integration 库用手写 SQL 对账 Go 分类（含 parent 链接、无关 DONE、/resume DONE）。
- 计划内未覆盖：customer 人群、页面人工视觉检查、`--db` / `--e2e`。
