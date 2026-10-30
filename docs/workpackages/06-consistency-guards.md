# 06 一致性收尾与机械护栏（P-D）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 详细规格：`docs/archive/backend-refactor-plan.md` §3 P-D

## 目标

消除重复的响应写出路径，并新增结构守卫防止回流。

## 改动范围

- 裸 `writeJSON` 统一为 `httpapi.WriteJSON`（清单见源文档 §3 P-D）。
- 新增 `scripts/check/check-structure.py`（待建） 并接入 `check.sh`（35 → 36 项，按 T1 条款记 amendment）。

## 不碰

- 不改响应体结构与状态码。
- 不改既有守卫的判定逻辑。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
python3 scripts/check/check-structure.py
```


## 行为变更

零。

## 未覆盖路径

- 新守卫需做一次负向验证（故意引入违规 → 断言 FAIL），否则视为未生效。
