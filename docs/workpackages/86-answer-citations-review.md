# 86 管理台「回答与引用」审查页

- 状态：见 INDEX.md
- 依据：决策 0041 §2.6；用户要求前台只留回答、引用移到后台审查
- 前置：WP81 已关

## 目标

1. 管理台可审查员工轮次与顾客 AI 消息的全文、引用、事实快照与 mock 标记；
2. 仅 `merchant_admin` / `knowledge_reviewer`，按店铺范围；不返回顾客身份、会话 ID、IP 或 cookie；
3. 列表截断问题/回答，详情才返回全文；打开详情写 `admin_audit`（不含正文）；
4. SQL 只在 `store_answers.go`；前端加入知识组「回答与引用」。

## 步骤

1. `GET /admin/v1/merchant/answers`（kind/mock/limit/before）与
   `GET /admin/v1/merchant/answers/{kind}/{id}`；
2. `store_answers.go`：UNION 两来源、游标、引用有效性、事实快照解析、审计写入；
3. `AnswersPage.vue`：筛选、表格、加载更多、详情抽屉（引用有效/失效、事实观测时间）；
4. 路由/导航/标题/视觉用例登记；openapi 两条路径；
5. 集成测试覆盖两来源、过滤、游标、店铺隔离、审计与内容不含顾客身份。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./internal/admin
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
```

## 限制 / 未运行

- 客户引用的有效性按「答案引用时保存的版本仍是 active 且 external_allowed/customer_eligible」判断，
  不重放当时的 ACL 快照（ACL 变化以文档状态体现）。
- 浏览器截图用例在 WP87 统一跑。

## 不要做

- 不在响应中返回 customer_id/session/IP/cookie；不在列表返回全文；
- SQL 不进 handler；不改 smoke 基线/棘轮。
