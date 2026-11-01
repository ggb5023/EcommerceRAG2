# 05 Python 长函数拆分（P-C）

> 状态：见 INDEX.md　适用提交：907977d　验收：见 §验收命令
> 背景：`docs/archive/backend-refactor-plan.md`（归档只留历史背景，本文件是当前规格）

## 目标

把 Python 侧前五长函数拆到单函数 ≤80 行，**行为零变更**。

## 清单（本批开工 AST 实测，2026-11-01）

| 函数 | 行数 | 位置 | 目标 |
| --- | --- | --- | --- |
| `provider_generate_node` | 322 | `python/app/graph/workflow.py:601` | ≤80 |
| `submit` | 209 | `python/app/ingest/database.py:164` | ≤80 |
| `_publish_reviewable` | 190 | `python/app/ingest/worker.py:511` | ≤80 |
| `_inspect_package` | 154 | `python/app/ingest/package_validation.py:40` | ≤80 |
| `parse` | 151 | `python/app/ingest/pipeline.py:93` | ≤80 |

测量口径（复核用）：

```bash
cd python && uv run --frozen python - <<'PY'
import ast, pathlib
rows=[]
for p in pathlib.Path('app').rglob('*.py'):
    for n in ast.walk(ast.parse(p.read_text(encoding='utf-8'))):
        if isinstance(n,(ast.FunctionDef, ast.AsyncFunctionDef)):
            rows.append(((n.end_lineno or n.lineno)-n.lineno+1, f"{p}:{n.lineno}", n.name))
rows.sort(reverse=True)
for s,loc,name in rows[:12]: print(f"{s:4d} 行  {loc:52s} {name}")
PY
```

## 拆分原则

- 只提取有明确职责的辅助函数/数据类，调用图与外部行为不变；主函数保留原有控制流。
- 不改判定逻辑、提示词文本、模型调用参数、图结构（0028 边界）、返回形状、错误码。
- 新函数命名表达职责，每个 ≤80 行；不新增产品行为。
- 测试随函数迁移；用例数不减（本批开工基线 **379 passed / 30 skipped**）。
- 不拆 `workflow.py` 的图结构；提示词资产化另立工作包。

## 提交顺序

1. `refactor(graph): split provider_generate_node`
2. `refactor(ingest): split submit`
3. `refactor(ingest): split publish`
4. `refactor(ingest): split package inspection`
5. `refactor(ingest): split parse`

每个文件拆分后跑 `cd python && uv run --frozen pytest -q`；全部完成后重启 `ecommerce-rag-dev`，
核对 ai 进程启动时间晚于最后提交，再跑五条 smoke。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
cd python && uv run --frozen ruff check app
bash scripts/check.sh
bash scripts/check.sh --smoke
cd frontend && npm test && npm run build
```

## 不碰

- 不碰 `workflow.py` 的图结构；不改提示词文本。
- 不改门禁、棘轮、基线、迁移；不重采 smoke 基线。
- 不做 08，不开新工作包。

## 未覆盖路径

- 拆分后 coverage 分支可能碎片化；若下降需补测试，不许下调门槛。
- 本次只覆盖清单前五名；第 6 名以后的长函数留给后续工作包。

## 实际交付 vs 计划

| 函数 | 拆分前 | 拆分后 | 位置 |
| --- | --- | --- | --- |
| `provider_generate_node` | 322 | 22 | `python/app/graph/workflow.py` |
| `submit` | 209 | 58 | `python/app/ingest/database.py` |
| `_publish_reviewable` | 190 | 16 | `python/app/ingest/worker.py` |
| `_inspect_package` | 154 | 21 | `python/app/ingest/package_validation.py` |
| `MarkdownParser.parse` | 151 | 48 | `python/app/ingest/pipeline.py` |

- 全部通过提取纯辅助函数/数据类完成：`provider_generate_node` 的 schema/instruction/messages/postprocess
  分块；`submit` 的 existing/source/manifest/job/document/event 分块；`_publish_reviewable` 的
  prepare/version/chunks/item/finalize 分块；`_inspect_package` 的 uploaded/manifest/document 分块；
  `MarkdownParser.parse` 提取 `_MarkdownParseState` 与逐行处理 helper。
- 行为零变更：`pytest -q` 拆分前后均为 **379 passed / 30 skipped**；ruff 全绿；`check.sh` 40；
  `--smoke` 45 且五条与 `.local/w2/` 基线逐字节一致；前端 40 tests + build 通过。
- ai 进程重启时间 `2026-11-01 22:06:50`，晚于最后提交 `907977d`（22:06:24）。
- 未新增测试文件：现有 379 个用例覆盖了拆分前后的路径；未改提示词文本、模型参数、图结构、返回形状、错误码。
- 05 保持 `active`；是否转 `done` 等主 agent 复核。
