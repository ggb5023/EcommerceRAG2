# 58 导入模板双 schema 常量统一（挂账 a）

- 状态：见 INDEX.md
- 依据：WP32 遗留（`32-m8-precheck-template.md` 记录"模板生成方式为 Go schema 常量，
  Python catalog.py 仍是校验 schema；两侧测试锁定"）；决策 0025 规则 1（每条规则只有一个出处）
- 前置：无

## 背景与问题

`go/internal/admin/import_templates.go` 的 `importTemplateSchema.Headers` 与
`python/app/ingest/catalog.py` 的 `PRODUCT_BASE_FIELDS | PRODUCT_ATTRIBUTES`、
`SIZE_CHART_BASE_FIELDS | SIZE_CHART_MEASUREMENTS` 是**同一事实的两份拷贝**，
仅靠 Go 侧一行注释 "mirrors catalog.py" 维系。任何一侧改列，另一侧不报错：
下载的模板可能缺列（顾客按模板填了但校验拒绝）或多列（校验漏掉）。

本包把「导入模板列集合」钉成跨进程契约：contracts.md 单一行陈述 + 新快速层守卫
静态比对两侧 + 两侧各一条测试锁定。**不改变任何运行时行为**（模板渲染、CSV
校验、别名、控制列全部不动）。

## 契约事实（唯一陈述处：docs/contracts.md 新增行）

- `product_table_v1` 模板列，有序 18 列：
  `sku_code, spu_code, title, category, brand, color, size_label, size_chart_code,
  capacity_ml, length_cm, width_cm, height_cm, weight_g, material, pattern,
  list_price_cents, currency, price_observed_at`
- `size_chart_v1` 模板列，有序 10 列：
  `chart_code, size_label, size_system, chest_cm, waist_cm, hip_cm,
  foot_length_cm, height_cm, weight_kg, fit_note`
- Python canonical 集合 == 模板列的**集合**（列序由 Go 侧模板锁定；
  Python 的 `validate_typed_document` 本就按列名解析，接受任意列序）。

## 步骤

1. **contracts.md 加一行**（类型 `跨进程`）：
   - Go 侧：`go/internal/admin/import_templates.go:importTemplateSchema`
     （渲染下载 CSV 的 Headers）
   - Python/前端侧：`python/app/ingest/catalog.py`
     （`PRODUCT_BASE_FIELDS|PRODUCT_ATTRIBUTES`、`SIZE_CHART_BASE_FIELDS|SIZE_CHART_MEASUREMENTS` 校验）
   - 两侧测试：本包步骤 3、4 的用例名
   - 状态：✅（两侧测试落地并跑通后再标）
2. **新守卫 `scripts/check/check-import-schema.py`**（纯静态、无 I/O 依赖）：
   - 正则提取 `import_templates.go` 两个 Headers 切片（保持顺序）；
   - 正则提取 `catalog.py` 四个 frozenset 的字面量；
   - 断言：product 集合相等、size_chart 集合相等；
   - 不等 → `FAIL import-schema: <差异列清单>`，exit 1；全等 → `PASS import-schema parity`
   - 挂入 `scripts/check.sh` 快速层（新一行 `run fast import-schema "python3 scripts/check/check-import-schema.py"`，
     放 docs-links 之后）；快速层 43→44 项，同步改 `docs/agent-entry.md` 两处
     （§3 标题 `43 项`、§4 `快速层 43 项`）——check-doc-numbers 会拦数字不符。
3. **Go 侧测试** `go/internal/admin/import_templates_test.go`：
   锁两个 Headers 的**顺序与内容**与契约行完全一致（表驱动，逐列断言）。
4. **Python 侧测试** `python/tests/test_catalog_schema.py`：
   锁 `PRODUCT_BASE_FIELDS | PRODUCT_ATTRIBUTES` == product 模板列集合、
   `SIZE_CHART_BASE_FIELDS | SIZE_CHART_MEASUREMENTS` == size_chart 模板列集合。

## 验收命令

```bash
bash scripts/check.sh            # 快速层 44 项，FAILED 0（含新守卫与 doc-numbers）
cd go && go test ./internal/admin/ -run TestImportTemplate -v
cd python && uv run --frozen pytest -q tests/test_catalog_schema.py
python3 scripts/check/check-import-schema.py   # PASS import-schema parity
```

**负向验证（不许改断言迁就，验证后必须还原）**：临时给 Go 的 product Headers
去掉 `currency` → 守卫必须 FAIL 且列出差异列；临时给 Python 的
`PRODUCT_BASE_FIELDS` 去掉 `currency` → 同样 FAIL。还原后全绿再交付。

## 不要做

- 不把模板改成提交的静态 CSV 文件（运行时渲染保持现状）。
- 不动 `PRODUCT_ALIASES`、`CONTROL_COLUMNS`、`SIZE_SYSTEMS`、`validate_typed_document`、
  handler 行为（BOM、示例行、错误处理）。
- 不改 `scripts/check/check-contracts.py` 的 `CONTRACTS_CROSS` 棘轮——新增 ✅ 行会让
  守卫打印 NOTE，棘轮由主 agent 在收尾提交单独上调。
- 不动 `eval/`、不新增迁移、不新增接口。
- 不提交到 `origin`（主 agent 负责 push）；提交身份
  `EcommerceRAG Maintainers <maintainers@example.invalid>`；不用 `git add .`。

## 汇报

按 CLAUDE.md 五节格式，含：守卫与两侧测试的真实命令输出、负向验证的 FAIL 行、
快速层 44 项全绿截图行、INDEX/agent-entry 数字改动的行号。
