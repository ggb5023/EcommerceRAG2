# 87 前台只留回答 + 收尾

- 状态：见 INDEX.md
- 依据：决策 0041 §2.6；用户要求员工/商家 AI 辅助只显示回答
- 前置：WP85、WP86 已关

## 目标

1. 员工 AI 查询台移除证据/引用面板与移动端「证据与引用」入口；
2. 员工建议面板移除内联引用卡片，保留复制/采纳与服务端 `can_copy`；
3. 员工队列显示服务类型、订单号、工单状态；售后会话新增「解决并关闭工单」；
4. 顾客端引用抽屉保留；
5. 文档与全量验证收尾。

## 步骤

1. `App.vue`：删除 EvidencePanel、证据栏与工具栏入口，工作区改为两列；
2. `SuggestionPanel.vue`：删除引用列表与 CitationCard；
3. `SupportDesk.vue`：队列项显示 lane/订单/工单；会话头显示 lane 与工单；
   `resolve` 动作加入领取状态清理；
4. 更新 `workspace.browser.mjs`（断言不再渲染引用）与 `api.test.ts`；
5. `architecture.md`、`ui-only-acceptance.md` 同步；INDEX/CURRENT 收尾。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
bash scripts/check.sh --smoke
bash scripts/check.sh --db
bash scripts/check.sh --e2e   # 隔离栈 --with-vite --commerce-sim
cd go && go build ./... && go vet ./...
```

## 限制 / 未运行

- `--e2e` 的 customer-control/source/service/selfhost-restore 用例需要独立的
  `CUSTOMER_RUNTIME_DIR` 受控栈；本机未配置时如实记为未覆盖，其余浏览器用例在隔离栈上跑。
- 真实在线模型链路（售后事实型回答）依赖 `v2_langchain_bailian`，本轮未启用。

## 不要做

- 不移除顾客端引用；不放宽 can_copy；不改 smoke 基线/棘轮。
