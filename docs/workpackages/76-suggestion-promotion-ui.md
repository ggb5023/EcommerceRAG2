# 76 采纳 UI：员工「采纳为知识草稿」

- 状态：见 INDEX.md
- 依据：WP75 后端；用户选择「员工采纳→草稿→审核发布」
- 前置：WP75 已关

## 目标

在建议面板上提供「采纳为知识草稿」：仅可复制建议可用，二次确认后提交；
成功提示草稿已入队、审核发布前不参与回答；失败可重试；随工单切换重置。

## 步骤

1. `SuggestionPanel.vue`：新增 `adopt` emit、`adoptBusy`/`adoptNotice` props、
   `data-testid="suggestion-adopt"` 按钮（`can_copy` 才可用）、ConfirmDialog
   二次确认（事实区：来源/可见性）、成功提示区。
2. `SupportDesk.vue`：`adoptCurrentSuggestion` 调
   `POST /v1/support/suggestions/{id}/adopt`（CSRF）；`adoptNotice`/`adoptBusy`
   状态；`resetSuggestion` 同时清空采纳提示；失败写入面板错误区。
3. 测试：SSR 断言可复制建议出现采纳按钮且无 disabled、成功提示渲染；不可复制
   建议按钮禁用的既有断言保留。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
```

## 限制

- dev 为 mock 画像，真实工单无法产生可复制建议，采纳正向链路未在 dev 目检；
  由后端单测与 Python 受众测试覆盖；端到端留待真实模型演示栈。

## 不要做

- 复制时自动采纳；不可复制时允许点击；
- 提示「已发布」（草稿仍需审核发布）；
- 不改后端接口、不加依赖。

## 汇报

五节格式；附组件改动行号、SSR/前端测试输出。
