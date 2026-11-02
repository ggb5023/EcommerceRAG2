# 18 视觉重构 阶段 A：配色 + 暗色 + 管理台骨架

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[frontend-references](../research/frontend-references.md)、
> [frontend-refactor-plan §6/§7/§8](../archive/frontend-refactor-plan.md)

## 目标

在不改业务逻辑、状态、API、文案、断点、路由和 data-testid 的前提下：

1. 把管理台从绿色系切到 Tailwind slate + indigo，并补齐暗色；
2. 加 `prefers-color-scheme` 跟随 + `.dark` / `.light` 手动覆盖与 localStorage；
3. 把 `AdminApp.vue` 的展示层拆成 `AdminShell`（侧边导航 + 顶栏）、页面组件和共享
   dialogs；脚本逻辑仍只实例化一次。

## 口径

- 颜色只来自 `styles/tokens.css`；组件不再新增硬编码颜色。
- 亮色：bg `#f8fafc`、surface `#ffffff`、border `#e2e8f0`、text `#0f172a`、
  muted `#64748b`、primary `#4f46e5`、success `#059669`、warning `#d97706`、
  danger `#dc2626`；primary hover/active `#4338ca` / `#3730a3`。
- 暗色：bg `#0b1220`、surface `#0f172a`、border `#1e293b`、text `#e2e8f0`、
  muted `#94a3b8`、primary `#818cf8`、success `#34d399`、warning `#fbbf24`、
  danger `#f87171`。
- 主题选择存 `localStorage['ecr-theme']`；无值跟随系统；`html.dark` / `html.light`
  覆盖；管理台顶栏 `data-testid="theme-toggle"`。
- 页面迁移只移动模板；`AdminApp.vue` 的 script/state/functions 保持原有实现，
  通过 provide/inject 共享给 shell 与页面。业务逻辑零改动。

## 路由页

`overview`、`outcomes`、`gaps`、`accounts`、`requests`、`audit`、`shops`、
`sources`、`ingestion`、`reviews`、`not-found` 各一个组件；`AdminPageRouter`
按 `page` 渲染。

## 验收命令

```bash
cd frontend && npm test && npm run build          # gzip ≤ 90KB
bash scripts/check.sh
bash scripts/check.sh --smoke
# 浏览器矩阵与截图（vite --force 临时端口，跑完停）
WORKSPACE_URL=http://127.0.0.1:5174 bash scripts/dev/admin-visual-shots.sh
```

截图写入 `.local/redesign/`：light/dark 各 outcomes、gaps、login 三张；
矩阵 JSON 写入同一目录。

## 不做

- 不做顾客端移动版（WP20）；不做表格工具栏/表单抽屉（WP19）；
- 不改文案、断点、路由、状态、API、Go/Python/迁移、棘轮/基线/smoke；
- 不引入 UI 框架或新依赖；不做 WP19/WP20；不 push。

## 未覆盖路径

- 暗色只做页面与组件目检（outcomes/gaps/login 截图），未做完整视觉回归比对。
- 浏览器矩阵覆盖路由与视口布局，不覆盖所有业务交互。
- `AdminApp.vue` 的脚本逻辑仍在原文件（只拆展示层），未做逻辑模块化。
