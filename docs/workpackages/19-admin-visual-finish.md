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
- 不提交、不修改、不删除 本机未跟踪目录；临时 Vite 跑完即停。

## 实际交付

- 新组件：`DataTable`（标题 + 搜索 + 操作插槽）与 `Drawer`（遮罩、标题、body/footer
  插槽），均为 `frontend/src/components/` 下的项目组件，无新依赖。
- 工具栏与抽屉：账号（新增员工）、权限申请（提交申请）、店铺（店铺设置）使用抽屉；
  审计、摄取、版本审核使用工具栏与搜索；列表仍调用原 API 与原有函数。
- 剩余页：运行状态改用 `Card`；资料来源改用 `DataTable`/`Table`/`Input`/`Button`；
  未找到页改用 `Empty`/`Button`；`ShopSettings`、`AgentReviewSettings` 改用
  `Input`/`Button`；`CustomerTrial` 的样式改为 tokens。
- tokens：新增 `--color-on-primary`、`--color-overlay` 与 `color-scheme` 亮/暗声明；
  顺带修正管理台品牌块、环境标记、激活导航的对比度，以及表单/文件控件的暗色原生外观。
- data-testid：baseline（`3994f1e`）与当前逐文件对照，无减少；新增
  `data-testid="data-table"` 与 `data-testid="drawer"` 各 1。
- 前端：`npm test` **49 passed**（新增 DataTable/Drawer 2 条）；`npm run build`
  PASS，JS gzip **78.24KB**（≤90KB）。14 个代码/测试提交逐个在独立 worktree 中跑
  `npm test` + `npm run build`，全部 **49 passed**，JS gzip 77.19–78.30KB。
- 浏览器：`scripts/dev/admin-visual-shots.sh` 在 5174 `--force` 上跑 **10 路由 ×
  4 视口** 全部 PASS；主题切换 `"" → "dark"`。矩阵 JSON 在
  `.local/redesign/browser-matrix.json`。
- 截图：22 张，覆盖 10 个管理路由 + 登录页的 light/dark，命名为
  `<route>-<light|dark>.png`；另手工验证店铺/权限申请抽屉开合，截图为
  `shops-drawer-dark.png`、`requests-drawer-dark.png`。
- 暗色逐页目检：11 张 dark 截图（10 路由 + 登录）逐张查看；修正了品牌块/环境标记
  对比度、表单字号与原生文件选择器在暗色下的白底。亮色抽查了店铺与资料来源。
- 门禁：`check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**；`--smoke`
  **45 PASS / 0 UNCONFIGURED / 0 FAILED**，五条 smoke 与 `.local/w2/` 基线逐字节一致。
- 提交：基础设施 `3b45741`，页面与组件 `6b854a8`…`e718ee6`，每页一个提交。

## 未覆盖路径

- 暗色为逐页人工目检，**未做像素级回归比对**，没有历史截图 diff 基线。
- 亮色只抽查店铺、资料来源与登录，未逐页目检全部 10 个亮色路由。
- 浏览器矩阵只检查标题、布局与水平溢出，不覆盖所有业务交互；抽屉只手工验证了
  店铺与权限申请，未验证账号抽屉（当前 dev 账号无账号页权限，截图是权限空态）。
- 版本审核右侧详情未选中版本，截图只覆盖列表与工具栏空态；未做完整审核操作回归。
- 未运行 `admin.browser.mjs`、`import-wizard.browser.mjs` 等完整浏览器套件；
  未重采或修改任何 smoke 基线。
- 原生文件选择器/下拉框的暗色外观依赖浏览器 `color-scheme` 支持，未跨浏览器验证。
