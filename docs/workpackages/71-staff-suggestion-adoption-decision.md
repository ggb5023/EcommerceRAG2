# 71 决策 0039：员工建议回复采纳为知识草稿

- 状态：见 INDEX.md
- 依据：用户 2026-11-06 决定「员工采纳→草稿→审核发布」
- 前置：无

## 目标

先冻结策略再实现：记录「员工可显式采纳 AI 建议为知识草稿、必须审核发布」
这一决定，并取代 `docs/knowledge.md` 与 `docs/support.md` 中「FAQ 不从客服
回复生成 / 人工回复不加入知识库」的绝对表述。

## 步骤

1. 新增 `docs/decisions/0039-staff-suggestion-knowledge-draft.md`（已写）；
2. `docs/decisions/INDEX.md` 增加 0039 行；
3. `docs/constraints.md` 决策计数 35 → 36；
4. `docs/knowledge.md`：把「FAQ 不从人工客服回复自动生成。首版没有完整草稿
   协作系统。」改为：人工回复永不自动入库；员工可显式采纳 AI 建议为知识草稿，
   草稿经审核发布后才参与回答；首版没有完整草稿协作系统；
5. `docs/support.md`：把「人工回复……也不会自动加入知识库」补一句采纳路径；
6. `docs/product.md`：如需与知识闭环表述对齐，补一句（可选，保持单一出处优先）。

## 验收命令

```bash
# 全部文档守卫（命令写法见 docs/agent-entry.md §4，此处不重复）
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不要做

- 不实现代码、不加迁移、不建候选表；
- 不宣称自动发布；不修改历史决策；
- 不重复陈述同一规则（docs-drift 预算 0）。

## 汇报

五节格式；附决策全文、各文档改动行号、守卫输出。
