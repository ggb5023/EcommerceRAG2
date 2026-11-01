# 03 结果度量：自动解决率（B1）

> 状态：done　适用提交：4a625b1　验收：见 §验收命令
> 前置：02-turn-decision（state 列）

## 目标

给商家一个可读的数字：窗口内提问总数、直接解决、澄清后解决、失败，
以及「自动解决率 = solved / N」和「含澄清解决率 = (solved+clarified) / N」。

## 口径（写死，实现时不许再解释）

- 窗口：?range=7d|30d，默认 30d；按 turn.created_at 过滤；租户内全店铺。
- state 取自 turn_decision.state（左连接 turn 表按 turn_id）。
- 四个互斥计数：
    solved    = state='DONE'   且 同一 conversation_id 在 5 分钟内没有更新的 turn
    clarified = state='ASKING' 且 同一 conversation_id 之后出现过 state='DONE'
    failed    = state='FAILED'
    unknown   = turn 存在但 turn_decision 无对应行（历史数据）
- N = solved + clarified + failed（不含 unknown）
- auto_rate = solved / N；assisted_rate = (solved + clarified) / N；N=0 时返回 null 而非 0
  （0 和"没有数据"必须在 UI 上可区分）。

## 不碰

- 不得出现任何货币字段或货币符号（0021）。
- 不加新表、不加视图：纯 SELECT 聚合。
- 不改 turn / turn_decision 的写入路径。

## 验收命令

详见 docs/agent-entry.md §4。本包额外：
  curl -s 'localhost:8080/admin/v1/merchant/outcomes?range=30d' | jq
  → 返回 {turns_total, scored_total, solved, clarified, failed, unknown, auto_rate, assisted_rate, truncated}
  → 与下面这条 SQL 的结果逐项相符（手算校验，不要只信接口自己）：
    SELECT
      count(*) FILTER (WHERE td.state='DONE')   AS done_all,
      count(*) FILTER (WHERE td.state='ASKING') AS asking_all,
      count(*) FILTER (WHERE td.state='FAILED') AS failed_all,
      count(*) FILTER (WHERE td.state IS NULL)  AS unknown_all
    FROM turn t LEFT JOIN turn_decision td ON td.turn_id = t.id::text
    WHERE t.created_at >= now() - interval '30 days';

## 未覆盖路径

- "5 分钟内没有新 turn" 用 turn.created_at 判断，不涉及 query 相似度；
  相似度阈值需要实测依据，本版不做。
- 跨时区/夏令时未处理（数据集是合成业务日期）。
- 聚合在 Go 侧完成（SQL 只做 join+filter），非 SQL GROUP BY；
  好处是分类逻辑可单测、不依赖数据库；代价是窗口内行数受 LIMIT 约束。
- 规格里那条对账 SQL 原写法 `td.turn_id = t.id` 类型不匹配（text vs bigint），
  实际使用 `t.id::text`；这是规格错误，已修正。

## 实际交付 vs 计划

- 计划外：`invalid_range` / `outcomes_unavailable` 两个错误码、抓取上限（`LIMIT 50000` + `truncated`）。
- 计划内未覆盖：N=0 的前端显示无组件级测试、页面未人工视觉检查。
