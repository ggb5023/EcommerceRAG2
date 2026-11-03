# 35 M9 阶段 2（一）：demo-store-v1 数据包与一键启动

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0035](../decisions/0035-demo-report.md) §2、[0034](../decisions/0034-product-table-import.md)、
> [environment-facts](../environment-facts.md)

## 目标

按 0035 §2 交付固定合成演示包 `demo-store-v1` 与 `scripts/dev/demo-shop.sh`：
一键启动隔离栈、经现有管理 API 导入/预检/审核/发布、幂等重跑、按演示 source 重置。
不实现 handoff 聚合、报告 CLI 或管理台报告页（WP36）。

## 数据包（按 0034 列）

- `data/synthetic/demo-store-v1/`：
  - `manifest.yaml`：单一 `v2-demo-tenant`/`shop-demo`，`business_date=2026-09-29`，
    `synthetic=true`、`provisional=true`、`license=internal-generated`；
  - `products.csv`：`product_table_v1` 18 列，8 个 SKU，覆盖颜色、尺码、容量；
  - `size-chart.csv`：`size_chart_v1` 10 列，S/M/L 与商品 `size_chart_code` 对应；
  - `policies.md`、`faq.md`、`returns.md`：发货、护理、退换和 FAQ；
  - `questions.jsonl`：新的演示问题集（ANSWER/CLARIFY/HANDOFF + 答案点），
    不写入 `eval/` 冻结 53 例。
- 所有内容为合成资料；不修改 `ecommerce-m2-aligned-v1`，不复制冻结评测集。

## 一键脚本

`scripts/dev/demo-shop.sh`：

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

- 数据包 `data/synthetic/demo-store-v1/`：单一 `v2-demo-tenant`/`shop-demo`，
  `business_date=2026-09-29`；`synthetic=true`、`provisional=true`、
  `license=internal-generated`。manifest sha256
  `2ddfeb368739ad134f8d7c231208c53ab4da8c672d4e7300b69ae7e6c14b78cb`；
  数据包（8 个文件）聚合 sha256
  `4924129a618e79f1fd098656f6d00c21b53140dc2b183b2ae7f57b7170c91cca`。
- 商品表 8 行、18 列（`product_table_v1`，覆盖杯子/衣物/毛巾/包，颜色、尺码、
  容量）；尺码表 3 行、10 列（`size_chart_v1`，S/M/L 与商品
  `DEMO-APPAREL-SIZES` 对应）；`policies.md`、`faq.md`、`returns.md`。
- `questions.jsonl` 10 条新合成问题：6 ANSWER、1 CLARIFY、3 HANDOFF，含答案点与
  期望文档；未写入 `eval/` 冻结集。包内 README 声明为虚构资料。
- `scripts/dev/demo-shop.sh`：默认固定端口 26081；复用健康隔离栈，否则调用
  `isolated-stack.sh --identity development --keep`；全部写入走管理 HTTP API。
  上传 manifest/文件后断言 `blocks=0`，确认批次，按 `job_id` 等待并选择本批次版本；
  尺码表先于商品表审核/发布；验证每个演示文档 active、approved、属于本 job、
  business_date 一致，且无 ready/pending 残留。相同源哈希全部 active 时跳过。
  `--reset` 只撤回演示文档的 active/ready 版本，跳过 revoked/superseded；不 SQL、
  不 DROP、不碰其他 tenant/shop。重置后运行时 manifest 增加一次性 nonce，避免
  幂等键复用已撤回 job；包 manifest 哈希保持不变。失败非零退出并带 HTTP 诊断。
- 实跑（最终 validation）：reset 后 `batch=56 job=149`，active 版本 282–286；
  重复执行 `SKIP ... manifest_sha256=2ddfeb...`。此前同一路径已跑过
  `batch=52 job=145`、`batch=53 job=146` 与 `batch=55 job=148`，均 active 且
  重跑 SKIP。临时改变 `faq.md` 后 `batch=54 job=147`，active 272–276，新 FAQ
  版本 275（hash `bc60c0ee...`），旧 FAQ 版本 270 为 superseded，证明源变化
  生成新版本且保留历史。所有 precheck 均
  `blocks=0 item_count=5 business_date=2026-09-29`。
- 验证：`bash scripts/check.sh` PASSED 40；`bash scripts/check.sh --smoke`
  PASSED 45（五条 smoke 与 `.local/w2/` 逐字节一致）；`go build ./...` 与
  `go vet ./...` 通过；`bash -n` 与 `shellcheck -S warning` 通过。
- 未改 `eval/`、`ecommerce-m2-aligned-v1`、迁移、棘轮/基线或 smoke 基线；
  `scripts/review/` 未提交、未修改。

## 未覆盖路径

- 未运行浏览器级 `--e2e`、`--db`、`--all`；未在 systemd dev 栈执行演示 seed，
  只在隔离栈 26081 验证。
- `questions.jsonl` 未接入轮次链路执行，未验证 ANSWER/CLARIFY/HANDOFF 的实际回答
  质量；未调用真实模型。
- 未实现 customer handoff 聚合、报告 CLI/管理台报告页（WP36）。
- 源变化测试使用临时包副本，未改动仓库内已提交的 demo 数据；未用真实商家数据。
- 未验证多实例并发执行同一脚本；脚本按 job_id 绑定版本，但仍以单实例执行为前提。
