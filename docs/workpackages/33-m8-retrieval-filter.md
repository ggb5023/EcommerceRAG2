# 33 M8 阶段 2（三）：SKU/规格检索过滤

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0034](../decisions/0034-product-table-import.md) §2.6、
> [0028](../decisions/0028-orchestration-boundary.md)、
> [0032](../decisions/0032-intelligence-domain-and-crawling.md)

## 目标

把用户显式提供的 SKU、颜色、尺码、规格放进 Go 签发的 `catalog_filter`，由 Python
用参数化 SQL 收窄已发布商品行；无过滤时保持原路径。过滤只读，不扩权、不从原始
query 拼 SQL、不让模型猜数值。

## scope 契约

- `RequestContext.catalog_filter` 消息：`sku_codes[]`、`colors[]`、`size_labels[]`、
  `size_chart_codes[]`、`attributes{key:value}`，全部可选；没有自由文本条件。
- Go 只从 `UnderstandResponse.Entities` 和 `domain.ResourceTokens` 提取；实体值必须
  在当前用户 query 中出现，不能从历史或助手文本补值。
- Go 只读 SQL 校验 active/reviewed/有效期内的商品行存在；无匹配时走澄清/资源不存在，
  不签发空结果过滤。
- 过滤条件在签发前写入 scope 并重新签名；Go 负责授权和提取，Python 只校验并消费。

## 提取边界

- SKU：实体名为 `sku`，或标识符形态的值出现在 `ResourceTokens(query)`。
- 颜色/尺码/尺码表：实体名为 `color`、`size_label`、`size_chart_code`（及约定中文名），
  值必须逐字出现在 query。
- 规格：实体名为已登记属性键（如 `capacity_ml`）或 `attribute`，值必须是
  `key=value`；键只允许 0034 的已登记键。
- 多值同字段取 OR，不同字段取 AND；不会把 query 中未声明的数字或属性当过滤条件。

## Python 过滤

- `_scope_params` 校验 `catalog_filter` 结构、长度和属性键；未知键拒绝。
- 过滤 SQL 通过 `(tenant_id,version_id,chunk_index)` 关联 `catalog_sku` 与 `chunk`，
  `sku_code`/`color`/`size_label`/`size_chart_code` 归一化后参数化比较；
  attributes 只按已登记键比较。
- 有过滤但零命中返回空证据并失败关闭（HANDOFF/澄清）；无过滤维持现有检索。
- 数值属性只由 SQL 过滤/读取，不交给向量或模型生成；不改变 RRF 权重。

## 验收命令

```bash
buf lint && buf build -o /dev/null && buf breaking --against .git#branch=main
cd go && go test ./... && go vet ./...
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 重建 + `strings` 标记 + 重启；Python 重启并核对启动时间；用 dev 版本 93
（7 个 SKU）做带 SKU 过滤的实库检索，断言只命中对应 chunk；不存在 SKU 失败关闭；
`check-contracts.py` 输出；五条 smoke 逐字节一致。

## 不做

- 不改 facts provider；不做 WP32 遗留；不自动发布/下架；不做模糊/自然语言规格猜测。
- 不改检索 RRF 或 embedding；不改已执行迁移；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- proto：`RequestContext.catalog_filter` + `CatalogFilter`（SKU/颜色/尺码/尺码表/属性；
  无自由文本）；生成 Go/Python 绑定并通过 buf lint/build/breaking。
- Go：`domain.CatalogFilterCandidates` 只从显式实体与 `ResourceTokens` 提取；gateway
  只读 SQL 校验 active/reviewed 商品行，`Authority.ApplyCatalogFilter` 重新签名 scope；
  无匹配转澄清，SQL 失败失败关闭。
- Python：`python/app/rpc/rag.py` 透传 filter；`python/app/retrieval/postgres.py` 校验结构/已登记属性键，
  用 `EXISTS` + `(tenant_id,version_id,chunk_index)` 参数化过滤；无过滤路径不变。
- 契约表新增「scope catalog_filter」跨进程行；Go domain/SQL 测试与 Python 检索测试覆盖。
- 实库：临时发布 WP31 版本 93 后，用 scope `sku_codes=["SKU-CUP-480"]` 检索：
  `matched=1`，命中 `db-chunk-145`；不存在 SKU `matched=0`（失败关闭）。随后撤回版本
  93，恢复 smoke 基线。
- 验证：Go/Python 全测通过；`check.sh --smoke` **PASSED 45**；stage-boundaries
  `runTurn 117/117`；contracts `cross 11/11`（ratchet 10，未调高）。
- 等待主 agent 复核后转 `done`。

## 未覆盖路径

- 未做完整 Go 路由 → scope 签名 → Python 检索的浏览器级 e2e；实库证据直接使用
  catalog_filter scope 验证检索，Go 提取由 domain 单测覆盖。
- 未在 active 商品表上验证颜色/尺码/属性多条件组合；SQL 条件由单测覆盖。
- WP33 只覆盖商品表行过滤；未改 facts provider、RRF/embedding 或自动发布/下架。
- 未运行 `--db`、`--e2e`、`--all`；未用真实平台 CSV/XLSX。
- dev 版本 93 在验证后为 `revoked`，不是 active/pending；这是为恢复 smoke 基线的清理动作。
