# 79 跨区视觉统一 + 截图审查工具 + 收尾文档

- 状态：见 INDEX.md
- 依据：用户要求 admin/staff/customer 统一 UI；前端质量差、参照成熟后台
- 前置：WP78 已关

## 目标

1. 工作台/登录/密码/预览页的硬编码绿色与灰色全部改用 tokens；
2. 主题（明/暗/跟随系统）在所有区域生效（不再只有 App.vue 应用）；
3. 提供可复跑的截图审查工具，agent 可据 PNG + manifest 自检；
4. 收尾路由与界面文档。

## 步骤

1. `frontend/src/styles/workbench.css`、`PasswordPage.vue`、`admin/KnowledgePreview.vue`、
   `frontend/src/styles/base.css` 的裸十六进制全部替换为语义 tokens（0 残留）；
2. `main.ts` 启动时 `applyThemeChoice(loadThemeChoice())`，暗色覆盖 admin/staff/
   assistant/customer/login 全部区域；
3. 修复 WP78 缺陷：`AdminApp.vue` 的 provide 漏掉 `navigationGroups`，导致管理台
   左侧分组导航不渲染；已在 provide 中加入并复跑截图确认；
4. 新增 `frontend/tests/visual-review.mjs`：
   - 双上下文登录（admin=`access`，staff=`staff`）；
   - 路由：admin 6 页 + `/staff` + `/assistant` + `/assistant/catalog` +
     `/shop/shop-demo` + `/login`；明/暗 × 1440/390；
   - 检查：横向溢出、页面标题、员工页无「知识管理」链接、目录页「非实时」提示；
   - 输出 `.local/visual-review/*.png` 与 `manifest.json`；
   - `package.json` 增加 `test:visual`；用法唯一出处 `docs/design-system.md`。
5. 文档：architecture 路由图、install 的 `/staff`/`/assistant` 描述、design-system
   审查循环补登录环境变量；design-tokens 测试纳入 base/workbench/PasswordPage/
   KnowledgePreview。
6. 人工目检：admin-accounts（权限导航分组）、assistant-catalog（暗色可读）、
   staff-queue（无绿色、无知识管理链接）。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
# 登录凭据按 docs/design-system.md 设置后：
npm run test:visual        # manifest PASS + 人工查看 PNG
bash scripts/check.sh
```

## 限制 / 未运行

- 本轮为 UI/文档改动，`--smoke` 需要 development 身份（dev 当前 OIDC），未跑；
  轮次链路代码未改。
- 员工工单的「已领取 + 建议面板」三列布局需人工登录领取后目检（截图工具当前
  未自动领取工单），列入未覆盖路径。

## 不做

- 不加 UI 依赖；不改业务接口；不改 smoke 基线/棘轮；
- 不提交截图与凭据（`.local/` 已忽略）。
