# 20 顾客端移动版（Open WebUI + Vercel 参照）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[frontend-references](../research/frontend-references.md)、
> [frontend-refactor-plan §7/§8.4](../archive/frontend-refactor-plan.md)、
> [0005 可复制回复条件](../decisions/0005-copyable-reply-policy.md)

## 目标

顾客端 `CustomerChat.vue` 移动优先：全屏消息、底部输入条、键盘顶起、
`[1]` 引用角标、底部引用抽屉与可复制状态；不改顾客端 API、路由、状态或文案。

## 口径

- 视口：`viewport-fit=cover`；高度用 `dvh`/`visualViewport`，不用固定 px 高度。
- 安全区：输入条 `padding-bottom: calc(..., env(safe-area-inset-bottom))`。
- 输入字号 ≥16px；触控目标 ≥44×44px，引用角标 ≥32px；间距 ≥8px。
- 消息区占满剩余高度，气泡最大宽度 85%；输入条始终贴底，不遮挡。
- 流式刷新时贴底；用户上滑暂停自动滚动，显示“回到最新”。
- 引用：`[n]` 角标 → 底部抽屉；抽屉复用 `SuggestionPanel` 的引用卡片
  （提取为 `CitationCard`，不另造一份）。
- 复制：沿用 0005 的可用/禁用态与原因。顾客快照当前没有 `can_copy` 字段，
  前端在字段缺失时按“AI 回答 + 至少一条引用”显示可复制，其余显示原因；
  不改 API，限制写入工作包“未覆盖路径”。
- 断点/设备：iPhone 14（390×844，DPR3）、Pixel 5（393×851）；390px 横向零溢出。
  管理台移动适配不在本包范围。

## 改动页面

- `frontend/src/CustomerChat.vue`：移动布局、键盘顶起、引用角标与抽屉。
- `frontend/src/SuggestionPanel.vue`：改为复用 `CitationCard`。
- `frontend/src/components/CitationCard.vue`：新增共享引用卡片。
- `frontend/index.html`：viewport 加 `viewport-fit=cover`。
- `frontend/tests/customer-mobile.browser.mjs`：设备描述符用例。

## 验收命令

```bash
cd frontend && npm test && npm run build        # gzip ≤90KB
# 临时 vite（5174 --force）跑完停；用例默认连接 5174
node tests/customer-mobile.browser.mjs
bash scripts/check.sh                            # 40
bash scripts/check.sh --smoke                    # 45
git grep -c data-testid frontend/src             # 与基线对照，只增不减
```

设备用例至少断言：底部输入可见、模拟键盘缩小视口后输入不遮挡、无横向溢出、
引用抽屉可开合、复制禁用态与原因正确。输出 JSON 写入 `.local/wp20/`。

## 不做

- 不改顾客端 API、路由、状态机、现有文案；不引依赖；
- 不做管理台移动适配；不碰 Go/Python/迁移/棘轮/基线；不重采 smoke；
- 不用 `git add .`；不提交/修改/删除 本机未跟踪目录；不 push。

## 实际交付

- `CustomerChat.vue` 改为移动优先：`visualViewport` 高度 + `dvh` 回退；固定聊天壳、
  全屏消息区、底部输入条；输入 16px、触控目标 44px、单次 `safe-area-inset-bottom`。
- 顶部店铺名 + 状态；店铺说明折叠；错误提示在消息流内；发送中禁用输入/按钮。
- 流式刷新保持贴底；用户上滑后暂停自动滚动并显示“回到最新”。
- 引用：每条 AI 回答显示 `[1]` 角标（`aria-label="查看来源 1"`，保留旧测试选择器），
  点击打开底部抽屉；抽屉 `role="complementary" aria-label="回答来源"`。
- `CitationCard` 从 `SuggestionPanel` 提取，建议面板与顾客抽屉共用同一组件。
- 复制：`can_copy` 字段存在时直接显示；顾客快照没有该字段时，按“AI 回答 + 至少
  一条引用”显示可用，否则显示“没有可引用的资料依据”等原因。复制成功/失败提示
  均在消息内显示；AI 回答支持 600ms 长按复制。
- `index.html` viewport 增加 `viewport-fit=cover`。
- 新增 `frontend/tests/customer-mobile.browser.mjs`：iPhone 14（390×844，DPR3）与
  Pixel 5（393×851，DPR2.75）；断言输入可见、无横向溢出、复制可用/禁用及原因、长按复制反馈、
  抽屉开合、模拟键盘缩小视口后输入与输入条不被遮挡；输出 6 张截图与 JSON 到
  `.local/wp20/`。
- data-testid：基线 `3d852b8` 26 处 → 当前 27 处，新增 `citation-card` 1 处，无减少。
- 前端：`npm test` **49 passed**；`npm run build` PASS，JS gzip **79.54KB**（≤90KB）。
  8 个代码/测试提交逐个在独立 worktree 跑 `npm test` + `npm run build`，全部通过
  （JS gzip 78.24–79.54KB）。
- 设备用例：`{"status":"PASS", device_scale_factor:3/2.75, checks:10}`；
  截图 `.local/wp20/{iphone-14,pixel-5}-{open,drawer,keyboard}.png`。
- 门禁：`check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**；`--smoke`
  **45 PASS**，五条 smoke 与 `.local/w2/` 基线逐字节一致。
- 提交：文档 `e09c385`，代码/测试 `0171f1c`…`48262a7`。

## 未覆盖路径

- **键盘顶起未真机验证**：Playwright 无真实软键盘，用例通过缩小视口并触发
  `visualViewport.resize` 模拟；iOS Safari 真机与地址栏收缩未测。
- **抽屉复用 M6 卡片已确认**：`SuggestionPanel` 与顾客端都使用
  `frontend/src/components/CitationCard.vue`；未另造卡片。
- 顾客快照没有 `can_copy`/`blocked_reason`，复制可用性由前端按可观测条件推导；
  0005 的 Go 判定只覆盖员工建议接口，本轮不改 API，二者语义未在服务端统一。
- 设备用例使用 Playwright 路由 mock（固定三消息），不连真实顾客会话；未断言真实
  AI 回答、SSE 流式、人工接管链路。
- 未测 iPhone SE（375×667）、横屏切换、长回答滚动与“回到最新”的真实滚动行为。
- 未做真实剪贴板写入断言，只验证按钮可用/禁用与原因；未跑完整
  `customer-service.browser.mjs`（需 Keycloak/TLS 环境）。
- 管理台移动适配、Go/Python/迁移/棘轮/基线均未触碰；smoke 未重采。
