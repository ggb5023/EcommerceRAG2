# 32 M8 阶段 2（二）：导入预检、模板与前端

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0034](../decisions/0034-product-table-import.md) §2.5、§2.1、
> [0017](../decisions/0017-m6-l1-import-precheck.md)、[contracts](../contracts.md)

## 目标

实现商品表/尺码表的逐行预检、两种 CSV 模板下载和导入向导展示。预检只给建议，
不写业务版本、不发布；block 错误存在时确认必须被拒绝。

## 预检接口

- 实现现有 `ValidatePackage` RPC（此前只有 proto，无实现/调用）。
- `ValidatedPackageDocument` 增加：
  - `table_schema`、`row_count`、`error_count`；
  - `row_errors[]`，每项
    `{row,column,code,severity,value,message,suggested_action}`；
  - `truncated` 标记。
- Go 在创建批次时调用该 RPC；结果写入 `import_item.precheck_detail` JSONB。
  错误总数上限 200，单条字段上限 200 字，超出截断并置 `truncated=true`。
- 确认接口重新读取批次预检；存在 block（文档级或行级）返回 409，不排队摄取。

## 预检规则

| 规则 | 严重度 | 说明 |
| --- | --- | --- |
| 缺必填列 | block | 商品表 `sku_code`、`title`；尺码表 `chart_code`、`size_label`、`size_system` |
| 未知列 | warn | 保留在 attributes，不参与过滤 |
| 空/重复表头、列数不符 | block | 保留行号；XLSX 同样 |
| SKU 重复 | block | 同版本内大小写不敏感 |
| 非法数字/单位 | block | 已登记数值属性必须为非负数值 |
| 非法 `size_system` | block | 只允许 0034 的五个枚举 |
| 未知尺码表/尺码 | warn | 预检提示；发布时仍缺则拒绝 |
| 价格缺 `price_observed_at` | block | 价格快照必须带观测日 |
| XLSX 公式/宏/合并/隐藏/超限 | block | 不执行、不静默跳过 |

校验复用 [catalog.py](../../python/app/ingest/catalog.py) 的 header/row 规范化与属性校验，
不另写二套规则。

## 模板

- `GET /admin/v1/merchant/import/templates/product_table_v1`
- `GET /admin/v1/merchant/import/templates/size_chart_v1`
- 只读 CSV，UTF-8 BOM；模板由 schema 生成（Go 代码常量，不提交手写模板文件）；
  不生成 XLSX 模板。首行是标准列名，第二行是示例占位行。
- 仅 merchant 角色可下载；响应带 `Content-Disposition`，不含货币金额文案。

## 前端

- `ImportWizard.vue` 展示每个文档的 `row_errors`：行号、列、错误码、severity、建议；
  `truncated` 显示“仅显示前 200 条”提示。
- 提供两个模板下载按钮；使用 `data-testid` 便于测试。
- 文案保持“预检仅供参考，发布前确认”，不出现金额。
- 既有上传、确认、撤回流程不变；block 时确认按钮禁用并提示原因。

## 验收命令

```bash
buf lint && buf build && buf breaking --against .git#branch=main
cd go && go test ./... && go vet ./...
cd python && uv run --frozen pytest -q
cd frontend && npm test && npm run build
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 重建 + `strings` 标记 + 重启；Python 重启并核对启动时间；用含重复 SKU、
非法尺码、缺观测日价格的合成 CSV 走预检，贴 `row_errors` 与
`import_item.precheck_detail`；两种模板各下载一次并贴首行；contracts 两侧测试。

## 不做

- 不做检索过滤（WP33）；不改 facts provider；不自动发布/下架。
- 不调棘轮/基线；不重采 smoke；不改已执行迁移；不提交手写模板文件。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- Proto：`ValidatedPackageRowError` + `ValidatedPackageDocument.table_schema/row_count/error_count/row_errors/truncated`；
  `ValidatePackage` RPC 已实现，Go 创建批次时调用并写入 `import_item.precheck_detail`。
- 预检规则：缺必填列、未知列 warn、空/重复表头、列数不符、SKU 大小写不敏感重复、
  非法数字/单位、非法 `size_system`、未知尺码表/尺码 warn、价格缺观测日、XLSX
  公式/宏/合并/隐藏/超限；block 存在时确认返回 **409 precheck_blocked**。
- 上限：row_errors 最多 200 条；每个字符串字段截断到 200 字符；超出置
  `truncated=true`，`error_count` 保留截断前总数。
- 模板：`GET /admin/v1/merchant/import/templates/{name}`，由 Go schema 常量生成
  `product_table_v1.csv` 与 `size_chart_v1.csv`，UTF-8 BOM，无手写模板文件。
- 前端：ImportWizard 显示行号/列/错误码/建议和截断提示，增加两个模板按钮；
  block 时禁用确认；新增 `template-product-table`、`template-size-chart`、
  `row-errors`、`row-error`、`row-errors-truncated`、`precheck-blocked`、
  `confirm-import` 选择器。
- 验证：Go/Python/前端全测通过；`check.sh` 40 与 `--smoke` 45 通过；buf
  lint/build/breaking 通过；实库预检 batch 28 返回 4 条 row_errors、block 3，
  确认返回 409；两种模板首行已下载核对。
- 契约表新增「ValidatePackage 逐行预检」跨进程行，Go/Python 两侧测试已列。
- 等待主 agent 复核后转 `done`。

## 未覆盖路径

- WP33 的检索过滤未开始；未做浏览器层 e2e/截图，只做前端单元测试与构建。
- 未用真实商家 CSV/XLSX 验证模板别名覆盖；预检样本是合成数据。
- 模板生成方式为 Go schema 常量，Python catalog.py 仍是校验 schema；两侧测试锁定
  当前列名，但新增列需要同时改两处。
- 尚未在 dev 发布商品表；WP31/WP32 验证批次保持 staged/未发布。
- 未运行 `--db`、`--e2e`、`--all`；未改 facts provider、检索 SQL 或已执行迁移。
