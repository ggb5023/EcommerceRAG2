# 09 决策账本语义（重开 02）

> 状态：pending　适用提交：5b9dd5d　验收：见 §验收命令
> 重开原因：02 的 `turn_decision` 以 turn 为键，ASKING 行会挡住后续 DONE；
> 终态与账本分两个事务写，取消/恢复路径可能留下与 execution 不一致的账本。

## 口径

1. **账本按 execution 建键**
   - 迁移 `0042`：`turn_decision` 增 `execution_id`，删除旧主键，改唯一键
     `(tenant_id, execution_id)`；旧行按 turn 反查 execution 回填，无法回填的保留
     NULL 并在查询中排除（NULL 行不能被主键约束，故用唯一索引）。
   - `AppendTurnDecision` 的冲突处理改为 `ON CONFLICT (tenant_id, execution_id) DO UPDATE`：
     同一 execution 后写覆盖（终态只算一次，重写不应发生，但重放安全）。

2. **终态只算一次，三处写入同一事务**
   - `recordTurnDecision` 从 `newTurnSink` 的 finish 包装移入 `finishTurn`，
     在 `state` 被 CANCELLED 覆盖之后调用；
   - execution、turn、turn_decision 在同一事务提交；
   - `newTurnSink` 不再写账本，`setTrace` 只把 chosen/rejected 传给 `finishTurn`。

3. **CANCELLED 入状态域**
   - 0042 的列注释、`store_analytics` 分桶、docs（agent-entry / data-ownership）同步；
   - 指标中 CANCELLED 计入 `failed` 桶（不新增响应字段，保持 03 的字段契约；
     cancelled 与 failed 都属于"未完成"，等 03 口径复核时再决定是否拆开）。

4. **恢复路径写账本**
   - `recoverInterruptedExecutions` 在 `persistExecutionTerminal` 之后调用同一个
     `appendTurnDecision`，与终态同一事务。

## 不碰

- 不改 03 页面与响应字段；
- 不碰 04；
- 不动 customer 通道（P1-1 另立工作包）；
- 不修改任何已在库上执行过的迁移（`0001`–`0041`），只新增 0042。

## 验收命令

```bash
cd go && go test ./internal/gateway/ -run 'Resume|Cancel|Recovery' -v
cd go && go test ./internal/admin/ -run TurnDecision -v
# 三库应用迁移后逐文件比对 sha256 与 schema_migration.checksum_sha256（41 → 42）
bash scripts/check.sh          # 39
bash scripts/check.sh --smoke  # 44
curl -s 'localhost:8080/admin/v1/merchant/outcomes?range=30d' | jq
# → turns_total = scored_total + unknown 且 metric_gap=false
```

## 三条生命周期测试

- `Resume`：ASKING 后同一 turn 新建 execution 并 DONE，最终账本行是 DONE 而非 ASKING；
- `Cancel`：ctx 取消，execution 与账本都是 CANCELLED（不是 DONE/FAILED）；
- `Recovery`：中断执行走恢复流程后，账本有行且 state 与 execution 一致。

## 未覆盖路径

- 旧账本若在同一 turn 上已有 ASKING 行且后续 DONE 行曾被 `DO NOTHING` 丢弃，
  历史数据无法还原 DONE；回填按 state 匹配 execution，匹配不到才取最后一次 execution。
- 恢复测试运行 `recoverInterruptedExecutions` 的全局扫描，必须使用隔离测试库并避免与
  其他会创建 EXECUTING 行的集成测试并发。
- 03 的 `failed` 桶在口径复核前包含 CANCELLED；页面文案未改。
