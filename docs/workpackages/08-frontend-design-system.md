# 08 前端设计系统（M9 阶段 1）

> 状态：见 INDEX.md　适用提交：3d09008　验收：见 §验收命令
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
- 本轮只迁移 Outcomes/Gaps/Import/Review/Login/Password；overview、accounts、requests、
  audit、shops、ingestion 及店铺设置等页面仍使用原样式。
- 浏览器全量矩阵受 dev 栈 `/admin/v1/merchant/ingestion` 503 阻塞；import-wizard/Keycloak
  用例还依赖隔离栈与 `ECR_BROWSER_ENV`，本轮未跑。

## 实际交付 vs 计划

- `023b8e8`：`frontend/src/styles/tokens.css`（亮/暗两套 + 4px 间距 + 字号/圆角/阴影）
  与 6 个组件 Button/Input/Card/Badge/Table/Empty；所有组件 `inheritAttrs: false` +
  `v-bind="$attrs"`，`data-testid` 透传到根节点；组件测试 6 条（文案、变体、禁用、空态、
  透传）。
- 页面迁移：
  - `16c22ad` Gaps：Button/Table/Empty/Badge；`GapsView.vue` data-testid 4 → 4。
  - `dbca663` Outcomes：Card（7 个指标卡）；该页原 data-testid 0 → 0。
  - `76c9cb8` Import：Button/Input；`KnowledgeImport.vue`/`ImportWizard.vue` 原 0 → 0。
  - `6676272` Review：Button/Badge/Table（历史版本、审核列表、批量结果）；原 0 → 0。
  - `3d09008` Login/Password：Input/Button；原 0 → 0。
- 全仓 `git grep -n data-testid frontend/src | wc -l`：`877a871` 为 8，当前为 25
  （新增组件测试断言与透传注释；既有 4 个 `GapsView` testid 全部保留）。
- 验证：`npm test` 46 passed；`npm run build` 通过；`check.sh` 40；`--smoke` 45
  五条逐字节一致；目标页面 1440/390 定向浏览器检查通过（`document.scrollWidth == viewport.width`）。
- 未改 API/路由/状态管理/文案；未新增依赖；未动 390px 断点值。
- 08 保持 `active`；是否转 `done` 等主 agent 复核。
