# 67 机械债务批（refactor-input #3/#5/#6/#7/#11/#12/#16）

- 状态：见 INDEX.md
- 依据：docs/refactor-input.md 对应条目；2026-11-06 主 agent 独立复审（②）
- 前置：无

## 目标

清掉 refactor-input 里可机械处理或可评估后处理的七条债务。**任何 turn 引擎改动
都不得改变五条 smoke 基线**——基线变了先停下汇报，不许重采。

## 各条处理方式

1. **#16 setTrace 时机**（Python 编排层）：把 `setTrace` 提前到检索阶段结束时调用
   一次（此时 `rejected` 已确定），生成阶段只补 `chosen`。效果：ASKING（快照回退）
   与生成前 FAILED 的轮次，`turn_decision.rejected` 不再恒空。
   验证：Python 测试（三类轮次的 rejected 非空断言）+ 真实轮次查
   `turn_decision.rejected` + **五条 smoke 逐字节一致**（若 baseline 变化停下汇报）。
2. **#3 budget 命名残留**：全仓 grep `provider-budget` / `budget` 字样（scripts 注释、
   文档、变量名），统一为 usage/口径命名；文档面以 `check-docs-drift` 实测为准，
   代码面只改命名不改行为。
3. **#5/#6 usage 口径**：
   - 重试是真实计费调用：**按行记** usage_log（现状），把这句话写进
     `docs/metrics.md` 作为唯一口径出处（一行，不另起决策）；
   - 聚合接口把 `tenant_id IS NULL` 的行单独归入 `unattributed` 桶（现有聚合 SQL
     补一个分组），Go/Python 各补一个断言。
4. **#7 ledger 参数清理**：`eval/run_v2_quality_safety_http.py` 删除 `run_case` 的
   可选 `ledger` 注入参数（仅旧测试注入点），对应旧测试同步清理。
5. **#11 resolveFactIntent 纯函数化**：把"修改传入 routed"改为**返回新值**，调用方
   只读返回值；domain 测试锁定行为不变。
6. **#12 turnGuard 终结路径**：评估后实施——`finish` 闭包改为返回错误，由 `runTurn`
   统一决定终结（消除阶段外终结路径）；若评估发现风险大于收益，**记录结论并保持
   现状**，如实汇报评估理由。

## 验收命令

```bash
bash scripts/check.sh            # 44 项 FAILED 0
cd go && go test ./internal/domain/ -cover
cd python && uv run --frozen pytest -q
bash scripts/check.sh --smoke    # 47 项，五条基线逐字节一致（改 turn 引擎后必须
                                 # 重建二进制+strings+重启再跑，见 environment-facts #1/#2）
# #16 真实轮次证据：发起一轮 ASKING 或生成前失败轮次，psql 查 turn_decision.rejected 非空
```

## 不要做

- 不重采 smoke 基线；不改 CONTRACTS_* 棘轮；不加迁移/接口/依赖；
- 不动 #9/#10/#15（下一批决策包）；不改冻结评测集；
- 不 push；提交身份固定；不用 `git add .`。

## 汇报

五节格式；每条债务附 diff 行号与测试输出；#12 若选择"评估后不改"必须给出理由。
