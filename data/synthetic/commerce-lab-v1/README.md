# commerce-lab-v1

拟真电商数据集，供本地 `commerce-sim`（facts 契约 + dev REST）与演示店铺使用。

- 参考日期：2026-09-29；固定 seed，可复现。
- 内容：32 个 SKU、库存、订单、订单行、物流与轨迹、退货、模拟工单、知识文档、示例问题。
- 无真实个人信息；订单号与顾客编号均为合成值。
- 订单/退货表不含金额汇总；价格仅作为 `get_price` 的单一事实来源。

重新生成并校验：

```bash
python3 scripts/dev/generate_commerce_dataset.py --write
python3 scripts/dev/generate_commerce_dataset.py --check
```
