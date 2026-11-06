# 78 管理台信息架构与权限界面

- 状态：见 INDEX.md
- 依据：用户要求参照成熟管理界面重构；merchant/access 权限分工
- 前置：WP77 已关

## 目标

管理台导航按「运营 / 知识 / 权限 / 系统」分组；账号页按角色区分只读与可管理；
权限申请页对 access 隐藏提交、只保留审批；access 可两步代理授予角色。

## 步骤

1. 新增 `frontend/src/admin/navigation.ts`（分组 + 图标 + 角色可见性）与单测；
   `AdminApp.vue` 用 `navigationGroups` 计算并导出；`AdminShell.vue` 渲染分组，
   追加「系统」入口（AI 查询台 `/assistant`、员工工作台 `/staff?stay=1`）。
2. `AccountsPage.vue`：
   - 列表对 `access_admin` 与 `merchant_admin` 都显示（merchant 只读）；
   - access 显示「授予 / 调整角色」抽屉：目标账号 ID、角色（merchant_admin /
     knowledge_reviewer / platform_observer）、店铺、理由 ≥10；提交两步代理
     （创建请求 → 立即审批），成功后刷新；
   - 撤销按钮仍仅 access。
3. `RequestsPage.vue`：提交按钮 `v-if="session && !canAccess"`；「处理」按钮仅
   `canAccess`；提交抽屉同样对 access 隐藏。
4. 测试：`navigation.test.ts`（merchant/access/无角色三组可见性）。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
# 浏览器/curl：access 登录看到分组导航与账号页全量；merchant 账号页只读、
# 权限申请页无提交按钮；access 提交按钮隐藏
```

## 限制

- 账号菜单（下拉聚合主题/退出）本轮未实现，头部沿用现有按钮，留待 WP79 视觉统一。
- DataTable 的加载/错误/分页统一模型未在本包重写，现有状态面板沿用。

## 不要做

- 前端不成为授权边界；merchant 不出现撤销/授予/审批按钮；
- 不改后端接口、不加依赖、不改 smoke 基线。

## 汇报

五节格式；附导航分组、账号/权限页改动、测试输出。
