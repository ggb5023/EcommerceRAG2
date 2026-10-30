# 08 前端设计系统（M9 阶段 1）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 详细规格：`docs/research/frontend-references.md`（选型结论）+ `docs/archive/frontend-refactor-plan.md` §6/§7（tokens 与移动适配细节）

## 目标

只做设计系统：tokens + 6 个基础组件 + `data-testid`，业务逻辑零改动。

## 改动范围

- `frontend/src/styles/tokens.css`（待建）（新）：颜色 / 间距 / 字号 / 圆角 / 阴影。
- `frontend/src/components/` 下 6 个基础组件（Button / Input / Card / Badge / Table / Empty）。
- 现有页面替换为组件调用，保留全部 `data-testid`。

## 不碰

- 不改任何 API 调用、路由、状态管理。
- 不改文案（改文案需同提交改断言）。
- 不动顾客端移动布局的断点值（390px 矩阵必须保持）。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```
浏览器用例全过 + 390px 矩阵不回退（逐页提交流程见源文档 §9）
```


## 行为变更

零（视觉重构）。

## 未覆盖路径

- 组件替换涉及页面较多，逐页提交；每页单独跑一次浏览器用例，不要一次换完。
