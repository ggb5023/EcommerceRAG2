# 05 Python 长函数拆分（P-C）

> 状态：pending　适用提交：f31bc44　验收：见 §验收命令
> 背景：`docs/archive/backend-refactor-plan.md`（归档只留历史背景，本文件是当前规格）

## 目标

把 Python 侧前五长函数拆到单函数 ≤80 行，**行为零变更**。

## 清单（本批开工 AST 实测，2026-11-01）

| 函数 | 行数 | 位置 | 目标 |
| --- | --- | --- | --- |
| `provider_generate_node` | 322 | `app/graph/workflow.py:601` | ≤80 |
| `submit` | 209 | `app/ingest/database.py:164` | ≤80 |
| `_publish_reviewable` | 190 | `app/ingest/worker.py:511` | ≤80 |
| `_inspect_package` | 154 | `app/ingest/package_validation.py:40` | ≤80 |
| `parse` | 151 | `app/ingest/pipeline.py:93` | ≤80 |

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
