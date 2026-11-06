# 74 员工工作台：建议面板并入工单

- 状态：见 INDEX.md
- 依据：用户决定「删除 AI 辅助独立页签；领取工单后在会话右侧显示建议面板」
- 前置：WP73 已关

## 目标

`/staff` 只保留人工队列；建议面板跟随已领取工单，未领取不显示、切换工单清空；
移除员工页的「知识管理」入口，改为「AI 查询台」。

## 步骤

1. `StaffWorkspace.vue`：删除标签页与 App.vue 引用，只渲染 `SupportDesk`；
   保留 admin 落地守卫与 `?stay=1`；`/staff?tab=assist&conversation=x` 重定向到
   `/assistant?conversation=x`，`tab=queue` 去掉查询参数。
2. `SupportDesk.vue`：
   - 删除「知识管理」链接，导航改为「AI 查询台」`/assistant` 与「商品目录」
     `/assistant/catalog`；
   - `canSuggest = owned && state==='HUMAN'`（抽到 `staff-suggestion.ts` 便于单测）；
   - 面板放三列右栏，`:key` 用工单 id；`open / release / close / resume-ai /
     失去 ownership` 清空建议与错误；
   - 「对最后一条顾客消息生成建议」放在面板内。
3. `channel.css`：三列布局（≥1100px 右栏 300–360px；≤1100px 面板落到会话下方；
   ≤700px 堆叠）。
4. `routes.ts`：`/support`、`/support/:id` 直接跳 `/staff`（不带 tab）。
5. 文档：`docs/support.md` 的 `/staff` 与 `/assistant` 描述。
6. 测试：`staff-suggestion.test.ts` 锁定可见性规则。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
# 浏览器用例（隔离栈或 dev）：领取后右栏出现建议；切换工单清空；未领取不显示；
# 员工页无「知识管理」链接
```

## 不要做

- 未领取/非 HUMAN 状态不得显示面板；
- 不改建议生成/复制接口语义；不改后端 ownership 校验（另开工作包）；
- 不加依赖、不改 smoke 基线。

## 汇报

五节格式；附组件改动行号、测试输出、浏览器检查结果。
