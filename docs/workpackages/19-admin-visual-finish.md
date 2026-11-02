# 19 视觉残留收尾：剩余管理页 + 工具栏/抽屉 + 暗色逐页目检

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[18-admin-visual-redesign](18-admin-visual-redesign.md)、
> [frontend-refactor-plan §6/§7](../archive/frontend-refactor-plan.md)

## 目标

在不改业务逻辑、文案、断点、路由与 data-testid 语义的前提下：

1. 把 WP18 未覆盖的管理页与共享组件统一到 tokens + 既有
   `Button`/`Input`/`Card`/`Badge`/`Table`/`Empty`，颜色只来自 CSS 变量；
2. 为列表页（账号、权限申请、审计、店铺、摄取、版本审核）补统一表格工具栏
   （标题 + 搜索 + 操作按钮）与表单抽屉；
3. 扩展截图脚本覆盖全部管理路由的 light/dark 逐页截图与 4 视口矩阵。

## 口径

- 复用 `frontend/src/styles/tokens.css`，不新增硬编码颜色；暗色沿用 WP18
  的 VS Code Dark+ 中性色与蓝色强调。
- 新组件放在 `frontend/src/components/`：`DataTable`（工具栏 + 插槽）与
  `Drawer`（表单抽屉）；不引入第三方 UI 库或新依赖。
- 搜索只做前端过滤，不新增 API；抽屉内的操作仍调用原有函数与接口。
- 每个页面单独提交；每次提交后跑 `npm run build`；保留原有 data-testid，
  只允许新增。

## 页面清单

- 剩余页：`overview`、`sources`、`not-found`、`ShopSettings`、
  `AgentReviewSettings`、`CustomerTrial`。
- 列表页抽屉：`accounts`（新增员工）、`requests`（提交申请）、
  `audit`（仅工具栏）、`shops`（店铺设置）、`ingestion`（仅工具栏）、
  `reviews`（仅工具栏与筛选）。

## 验收命令

```bash
cd frontend && npm test && npm run build          # gzip ≤ 90KB
bash scripts/check.sh
bash scripts/check.sh --smoke
# 浏览器矩阵与逐页截图（vite --force 临时端口，跑完停）
WORKSPACE_URL=http://127.0.0.1:5174 bash scripts/dev/admin-visual-shots.sh
```

截图写入 `.local/redesign/<route>-<light|dark>.png`；矩阵 JSON 写入同一目录，
覆盖 10+ 路由 × 4 视口（1440/1280/1024/390）。

## 不做

- 不做 WP20 顾客端移动版；不做 WP21 后端债务；
- 不改文案、断点、路由、状态、API、Go/Python/迁移、棘轮/基线/smoke；
- 不重采 smoke 基线；不引依赖；不 `git add .`；不 push；
- 不提交、不修改、不删除 `scripts/review/`；临时 Vite 跑完即停。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
