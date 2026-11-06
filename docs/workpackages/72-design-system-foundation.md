# 72 设计系统地基（tokens / 共享组件 / 设计规范）

- 状态：见 INDEX.md
- 依据：用户决定「自研组件 + 统一设计规范」；员工/顾客页面仍硬编码绿色
- 前置：WP71 已关

## 目标

建立唯一视觉来源：语义 tokens、共享组件（PageHeader / StatePanel /
ConfirmDialog）、页面规范文档；把客服与建议面板从硬编码绿色迁到 tokens；
把 App.vue 与 AdminApp.vue 的全局样式抽到独立 CSS。

## 步骤

1. `frontend/src/styles/tokens.css`：新增 `--color-bg-subtle`、`--color-surface-raised`、
   `--color-border-strong`、`--color-primary-soft`、`--color-success-soft`、`--color-warning-soft`、`--color-danger-soft`、
   `--color-info`、`--color-focus`、`--shadow-focus`、`--font-mono`、`--control-height`、
   `--rail-width`、`--page-max`；明暗两套值。
2. `frontend/src/channel.css`：删除绿色（#17312b/#16765b/#f2f7f5 等），全部改用
   tokens；保留 `.support-columns` 布局与断点。
3. `frontend/src/SuggestionPanel.vue`：样式改用 tokens。
4. 新增 `frontend/src/components/PageHeader.vue`、`StatePanel.vue`、`ConfirmDialog.vue`
   （后者含事实区 + 理由 ≥10 字 + 勾选 + busy）。
5. `App.vue` 的 `<style>` 抽到 `frontend/src/styles/workbench.css`；
   `admin/AdminApp.vue` 的 `<style>` 抽到 `frontend/src/admin/admin.css`；
   `main.ts` 导入两份全局样式。
6. 新增 `docs/design-system.md`（tokens 表、组件规则、页面模板、区域统一、
   视觉审查循环、无障碍）。
7. 新增 `frontend/src/design-tokens.test.ts`：扫描已迁移文件中的裸十六进制颜色。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不要做

- 不引入 UI 库或新依赖；不改路由/接口/业务逻辑；
- 不在本包重排管理台信息架构（WP78）；不重写页面布局（WP79）；
- 不修改 smoke 基线与棘轮。

## 汇报

五节格式；附 tokens 增项、channel.css/SuggestionPanel 改动行号、新组件清单、
前端测试/lint/build 输出、检查脚本输出。
