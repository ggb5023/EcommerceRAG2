# 设计系统与页面规范

> 权威等级：context。界面颜色、间距、组件行为以本文 + `frontend/src/styles/tokens.css`
> 为准；页面不得硬编码颜色。本文不重复业务规则（见 contracts / decisions）。

## 1. 设计令牌

唯一出处 `frontend/src/styles/tokens.css`。语义分组：

| 组 | 令牌 | 用途 |
| --- | --- | --- |
| 底色 | `--color-bg` / `--color-bg-subtle` / `--color-surface` / `--color-surface-raised` | 页面、次级底、卡片、悬浮层 |
| 边框 | `--color-border` / `--color-border-strong` | 分隔线 / 可交互控件边界 |
| 文本 | `--color-text` / `--color-text-muted` / `--color-on-primary` | 正文 / 次要 / 主色上的文字 |
| 主色 | `--color-primary` / `-hover` / `-active` / `--color-primary-soft` | 主按钮、选中态、链接 |
| 状态 | `--color-success` `--color-warning` `--color-danger` `--color-info` 及 `-soft` 变体 | 徽标、提示、危险操作 |
| 焦点 | `--color-focus` / `--shadow-focus` | 键盘焦点环 |
| 字体 | `--font-sans` / `--font-mono` | 正文 / 哈希与 ID |
| 布局 | `--space-1..8` / `--radius-sm..lg` / `--control-height` / `--rail-width` / `--page-max` | 间距、圆角、控件高度、导航宽度、内容宽度 |

暗色值同时提供：`prefers-color-scheme: dark` 与 `:root.dark`（主题切换用）。
新增颜色必须先加令牌，再在组件里引用；`design-tokens.test.ts` 会扫描已迁移
文件中的裸十六进制颜色。

## 2. 组件规则

- **Button / Input / Badge**：只用现有 `components/` 组件，不写原生按钮样式；
  危险动作用 `variant="danger"`，文字操作用 `variant="text"`。
- **PageHeader**：每个管理页顶部一个（eyebrow / 标题 / 副标题 / actions 插槽）；
  管理台外壳已有页面 h1 时用 `level=2`，避免同页两个 h1。
- **DataTable**：标题、搜索、筛选（actions 插槽左侧）、表格、空态、分页；
  加载与错误分别用 **StatePanel**。
- **StatePanel**：`loading` / `error` / `forbidden` / `empty` 四种语气；
  错误必须给重试入口，禁止空白页。
- **ConfirmDialog**：事实区（label/value）+ 理由（≥10 字）+ 勾选确认；高风险
  操作（发布、撤回、撤权、授予）必须使用。
- **Drawer**：只用于表单类次级任务；关闭后焦点回到触发按钮。

## 3. 页面模板

```text
PageHeader(eyebrow, title, subtitle, actions)
  → DataTable(title, subtitle, search, filters, actions)
    → Table / Empty
    → 分页脚
StatePanel：loading / error / forbidden 时替换整块内容
```

- 列表页统一：左搜索、右操作；筛选在搜索与操作之间。
- 表单页：两列栅格（窄屏单列），必填校验在前端做即时提示、服务端为准。
- 管理台导航分组：**运营 / 知识 / 权限 / 系统**；无可见项的组不渲染。

## 4. 区域统一

- 管理台、员工工作台、AI 查询台、顾客聊天、登录页共用同一套令牌与组件。
- 员工/顾客页面不得再引入独立配色（历史 `channel.css` 绿色已移除）。
- 暗色模式下所有区域必须可读：表格行、抽屉、弹窗、引用卡片一致。

## 5. 视觉审查循环（agent 用）

1. 启动目标栈（隔离栈或 dev），设置 `VISUAL_ADMIN_USER` / `VISUAL_ADMIN_PASSWORD` /
   `VISUAL_STAFF_USER` / `VISUAL_STAFF_PASSWORD` 后运行 `npm run test:visual`；
2. 读取 `.local/visual-review/manifest.json` 中的 checks 与 failures；
3. **必须**用 Read 工具直接查看 `.local/visual-review/*.png`（明/暗、1440/390），
   不能只看 JSON 就宣称通过；
4. 对照本规范检查：颜色是否来自令牌、是否有横向溢出、空/错/加载态、导航分组、
   角色可见性、未领取工单不显示建议面板、目录页“非实时”提示；
5. 修复后重跑，直到 manifest 全绿且截图目检通过。

## 6. 无障碍

- 所有交互元素可键盘到达；焦点环使用 `--color-focus`，不得移除 outline 而不提供替代。
- 模态/抽屉打开时焦点进入内部，Esc 关闭，关闭后焦点归还。
- 颜色不是唯一信息载体：状态同时用文字（徽标文案、`role=status/alert`）。
