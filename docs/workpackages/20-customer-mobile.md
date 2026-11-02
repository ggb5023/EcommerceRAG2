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
- 不用 `git add .`；不提交/修改/删除 `scripts/review/`；不 push。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
