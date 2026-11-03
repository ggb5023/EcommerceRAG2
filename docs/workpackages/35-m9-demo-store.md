# 35 M9 阶段 2（一）：demo-store-v1 数据包与一键启动

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0035](../decisions/0035-demo-report.md) §2、[0034](../decisions/0034-product-table-import.md)、
> [environment-facts](../environment-facts.md)

## 目标

按 0035 §2 交付固定合成演示包 `demo-store-v1` 与 `scripts/dev/demo-shop.sh`（待建）：
一键启动隔离栈、经现有管理 API 导入/预检/审核/发布、幂等重跑、按演示 source 重置。
不实现 handoff 聚合、报告 CLI 或管理台报告页（WP36）。

## 数据包（待建时按 0034 列）

- `data/synthetic/demo-store-v1/`（待建）：
  - `manifest.yaml`：单一 `v2-demo-tenant`/`shop-demo`，`business_date=2026-09-29`，
    `synthetic=true`、`provisional=true`、`license=internal-generated`；
  - `products.csv`：`product_table_v1` 18 列，8 个 SKU，覆盖颜色、尺码、容量；
  - `size-chart.csv`：`size_chart_v1` 10 列，S/M/L 与商品 `size_chart_code` 对应；
  - `policies.md`、`faq.md`、`returns.md`：发货、护理、退换和 FAQ；
  - `questions.jsonl`：新的演示问题集（ANSWER/CLARIFY/HANDOFF + 答案点），
    不写入 `eval/` 冻结 53 例。
- 所有内容为合成资料；不修改 `ecommerce-m2-aligned-v1`，不复制冻结评测集。

## 一键脚本

`scripts/dev/demo-shop.sh`（待建）：

1. 复用 `isolated-stack.sh --identity development` 与固定端口（默认 26081）；
   已有健康栈则复用，否则启动；不碰 systemd dev。
2. 读取 manifest 的文档哈希；经 `/admin/v1/merchant/imports` 上传、断言
   `blocks=0`，确认批次，等待 worker。
3. 按尺码表 → 商品表 → 其他文档的顺序审核/发布；验证所有演示文档
   `status=active`、`review_status=approved`、无 pending，business_date 一致。
4. 幂等：所有演示文档已有同源哈希 active 版本则跳过；源变化生成新版本，不覆盖历史。
5. `--reset`：只对本演示 source 的版本调用现有 revoke API，不 DROP 库、不删除或
   修改其他租户/测试数据；失败非零退出并保留诊断。

## 验收命令

```bash
bash -n scripts/dev/demo-shop.sh
shellcheck -S warning scripts/dev/demo-shop.sh
bash scripts/dev/demo-shop.sh --ports 26081
bash scripts/dev/demo-shop.sh --ports 26081       # 幂等
bash scripts/dev/demo-shop.sh --ports 26081 --reset
bash scripts/dev/demo-shop.sh --ports 26081        # 重置后首发
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：本地/隔离栈预检 0 block；products/size-chart 逐列对照 0034；打印数据包
sha256 与 manifest 哈希；precheck_detail 无 block；active 文档/版本断言输出；
幂等与 reset 输出；冻结评测集未被修改。

## 不做

- 不做 customer handoff 聚合或报告 CLI（WP36）；不做管理台报告页。
- 不新增演示身份，不新增导入/审核/发布写路径，不直接 SQL 写库。
- 不修改 `eval/` 冻结集、`ecommerce-m2-aligned-v1` 或已执行迁移。
- 不调棘轮/基线；不重采 smoke；不自动发布/下架真实数据。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（数据和脚本提交后填写；未完成项在汇报第 4 节点名。）

## 未覆盖路径

（数据和脚本提交后填写。）
