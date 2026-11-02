# 15 提示词资产化与 prompt-matrix

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：`docs/decisions/0025-code-level-rules.md` R1、WP05 的拆分做法

## 目标

把 `python/app/graph/workflow.py` 内联的 control / generation 提示词搬到
`python/app/graph/prompts/` 资产模块，由 `registry.py` 维护“变体 → (module, version)”
清单；workflow 只 import registry，不再内联提示词文本。**行为零变更**：运行时字符串
逐字节不变，判定逻辑、模型参数、图结构不变。

同时新增 `prompt-matrix` 测试，固定一组 query 与期望变体，防止以后改提示词时
悄悄改变变体选择。

## 口径

- 文本逐字节不变；拆分前后用字符串字面量 multiset 对比，除被移动的提示词外零差异。
- 每个变体一个模块：
  `control.py`（provider_understand 系统提示词、customer 补充段、context gate 提示词）、
  `generation_base.py`（base 指令、base suffix、六段只读能力元数据）、
  `generation_facts.py` / `generation_plan.py` / `generation_snapshot.py` /
  `generation_limits.py` / `generation_delivery.py` / `generation_applicability.py` /
  `generation_reason.py` / `generation_customer.py`。
- `registry.py` 维护 `VARIANTS: variant -> (module, version)`（版本字符串，如 `"v1"`）
  与文本资产；workflow 通过 registry 读取，不直接 import 资产模块。
- 提示词修改流程（写进本文件，后续遵守）：
  1. 先在 registry 提升该变体的 version；
  2. 同步 `python/tests/test_prompt_matrix.py` 的期望；新增/删除变体时同时更新
     `VARIANTS` 与 matrix；
  3. 重新跑 multiset 对比与 smoke，确认行为变化是预期的；不得只改文本不升版本。
- prompt-matrix 固定样本覆盖 facts/plan/snapshot/limits/delivery/applicability/
  reason/customer 至少各一例，以及一个无变体的普通知识问句。每个变体的 registry
  文本非空且版本号存在。

## 改动范围

- 新增 `python/app/graph/prompts/` 包及 `registry.py`。
- `python/app/graph/workflow.py`：删除内联提示词常量，改用 registry；逻辑不变。
- `python/tests/test_prompt_matrix.py`：新增变体选择与 registry 完整性测试。
- `docs/workpackages/15-prompt-assets.md`、INDEX、CURRENT；如
  `docs/refactor-input.md` 有提示词债务条目则同步。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
bash scripts/check.sh
bash scripts/check.sh --smoke
cd frontend && npm test && npm run build
```

## 不做

- 不改任何提示词文本、判定逻辑、模型参数、图结构；不调棘轮/基线；
  不重采 smoke；不碰评测集；不做剩余长函数拆分（另批）；不做 WP16。
- 不用 `git add .`；提交身份 `EcommerceRAG Maintainers <maintainers@example.invalid>`；
  不 push、不 merge、不删分支。

## 实际交付

- `python/app/graph/prompts/`：control、generation_base、facts、plan、snapshot、
  limits、delivery、applicability、reason、customer 共 10 个资产模块；
  `registry.py` 维护 10 个 variant → `(module, "v1")` 清单与 `ASSETS`。
- `workflow.py` 只 import `registry`；provider_understand 系统提示词、customer
  补充段、context gate 提示词和全部 `_GENERATION_*` 文本均已移出。
- 字面量 multiset 证据：baseline workflow 854 个字符串字面量，新 workflow 834；
  **removed=20、added=0，且 removed 恰好等于 20 个提示词值**；20/20 个旧值与
  registry ASSETS 逐字节相等（sha256 已逐一核对）。
- prompt-matrix：9 个固定样本（base/facts/plan/snapshot/limits/delivery/
  applicability/reason/customer）+ registry 完整性测试，共 10 条；`pytest -q` =
  **402 passed / 30 skipped**。
- 运行时：`app.ai_process` 启动 12:30:46，晚于 `d95abf1` 的 12:30:39。
- 门禁：`check.sh` 40 PASS；`check.sh --smoke` 45 PASS，五条 smoke 与
  `.local/w2/` 逐字节一致；`go build ./... && go vet ./...` OK；前端
  `npm test` 46 passed、`npm run build` PASS。
- `docs/refactor-input.md` 未发现提示词相关债务条目，未改。

## 未覆盖路径

- 本包只验证字符串与变体选择，不验证真实模型在新资产加载后的回答质量；
  真实模型质量评测仍按既有 `--live` 流程另行授权。
- prompt-matrix 是固定样本的回归钉，不是完整的提示词语义测试。
- 不覆盖尚未资产化的非 workflow 提示词（如有）。
