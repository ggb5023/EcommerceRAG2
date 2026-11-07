# 89 数据集真实感修复（退货 SKU / 时间顺序）

- 状态：见 INDEX.md
- 依据：WP88 交付报告「数据集 returns/tickets 时间与 SKU 归属不一致」待办；用户 2026-11-07 授权
- 前置：WP88 已关（`a2e583f`）

## 目标

修复 `commerce-lab-v1` 中可被查询发现的内部矛盾，重建确定性数据集并加断言：

1. `returns.csv` 的 `sku` 必须属于该退货订单的 `order-items.csv`；
2. 退货请求时间不早于该订单签收时间（因而也不早于下单时间）；
3. 退货的 `resolved_at` 不早于 `requested_at`；
4. 工单 `created_at` 不早于订单 `placed_at`，`closed_at` 不早于 `created_at`；
5. 顺带修复同一类时间矛盾：物流轨迹 `event_at` 不早于 `shipped_at`，且同一运单按 `seq` 单调；
6. 新断言覆盖以上规则与「所有时间不晚于参考日期」。

## 改动范围

- 数据集生成器（WP82 的脚本；本轮只改数据构造与 README 说明）
  - 记录每单的 SKU 集合与签收时间；
  - 退货 SKU 从该订单行中选，退货请求基于签收时间偏移；
  - 工单时间基于下单时间偏移；
  - 物流事件基于发货时间偏移；
  - 生成器 README 增加规则说明。
- `data/synthetic/commerce-lab-v1/`：用固定 seed 重新生成，更新 manifest 的 hashes/rows。
- `python/tests/test_commerce_dataset.py`：新增 SKU 归属与时间顺序断言。
- `docs/workpackages/89-dataset-realism.md`、`INDEX.md`、`CURRENT`。

## 验收命令

生成器可复现校验沿用 WP82 的验收命令（`docs/workpackages/82-commerce-dataset-simulator.md`），
本轮其余命令：

```bash
cd python && uv run --frozen pytest tests/test_commerce_dataset.py -q
cd python && uv run --frozen pytest -q
cd go && go test ./internal/commercesim -count=1
bash scripts/check.sh
bash scripts/check.sh --smoke
```

数据集不涉及 SQL / proto / 迁移，本轮不跑 `--db`。

## 不要做

- 不改 `SEED`、参考日期、商品/价格/知识文档内容（README 说明除外）；
- 不重采 `.local/w2/` 下任何 smoke 基线；
- 不改 Go/Python 生产逻辑、不新增迁移、不新增表/列；
- 不为了通过而放宽或删除断言；
- 不修改、不提交 `scripts/review/`；
- 不清洗 e2e 环境债，只在交付报告里评估（用户要求「评估是否清理」）。

## 风险

- 数据集字节变化会使 `--check` 与 manifest 旧值不一致，必须同时提交重建后的全部文件；
- Go `commercesim` 测试只动态挑选 shipped/delivered 订单，不硬编码行号；重建后需实跑确认。
