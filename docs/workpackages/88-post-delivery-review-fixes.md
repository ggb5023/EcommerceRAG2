# 88 交付后审查修复（售前/售后/工单/事实/回答审查）

- 状态：见 INDEX.md
- 依据：WP80–WP87 交付后只读审查；用户要求审查并修复潜在错误
- 前置：WP87 已关

## 目标

只修复审查确认的错误并补回归测试，不扩功能、不改棘轮/基线、不改已执行迁移。

## 数据与 SQL

1. **JSON null 快照**：`customer.addMessage` 的 `citations any` 在传入 typed-nil 时写入
   `null`；管理台审查与巡检的 `jsonb_array_length` 与 `jsonb_array_elements` 会报错。
   - `addMessage` 改为 `[]*ragv1.Evidence`，新增 `marshalCitationSnapshot`（nil→`[]`）；
     `finishSuccess` 的 `evidence_json` 同样归一。
   - `store_answers.go`、`store_inspection.go`、`countExecutionFacts` 的 JSON 读取加
     `CASE WHEN jsonb_typeof(...)='array' ... ELSE 0/'[]' END`。
   - 不新增数据迁移：旧行由读取端防御，新行由写入端保证。
2. **迁移 0059（审查新发现）**：0058 的 lane CHECK 在 `after_sales + NULL` 上得到 NULL，
   PostgreSQL 视为通过。新增 0059 重建约束，显式 `order_id IS NOT NULL`；不修改 0058。

## 复制规则

3. **mock 事实不可复制**：顾客与员工的事实计数都只看 `tool_name` 非空且
   `is_mock=false`；员工建议仍强制 factCount=0。
4. **redacted 回答不可复制**：引用失效且内容被替换时强制
   `can_copy=false/blocked_reason=not_copyable`；`domain.CanCopyReply` 参数语义改为
   非 mock 事实数。
5. **事实复验只针对 ANSWER**：CLARIFY/HANDOFF 不因提交时事实提供方不可用而被转人工。

## 售后轨道 / 提示词

6. **空文档授权只允许售后**：Go `IssueCustomerScope`、Python `validate_scope`、
   `PostgresRetriever._scope_params` 仅对 customer + after_sales + 有订单号放行；
   空数组在检索 SQL 中返回零行，绝不当通配。售前/员工行为不变。
7. **售后提示词保留 facts 规则**：`_customer_messages(..., has_facts)` 在售后提示词后追加
   `GENERATION_FACTS_INSTRUCTION`（is_mock、分/元、observed_at、仅事实时 citation_ids=[]）；
   控制提示词告知模型：若顾客明确给出不同订单号，必须以 `order_id` 实体上报供 Go 拒绝；
   售后首条消息不因「刚才/前面」被强制澄清。
8. **Python facts 防御**：`_validate_facts` 接收 scope，售后订单事实必须匹配签名的
   `customer_order_id`，售前顾客 scope 禁止事实。
9. **SKU 路由**：`skuTokenPattern` 允许数字开头的 SKU（如 `3M-TAPE-100`）。

## 工单 / 员工界面

10. `resume-ai` 同时把工单置为 `CLOSED`（此前遗留 ASSIGNED/OPEN）。
11. 员工会话详情响应包含活动工单，WP87 的工单状态行才会渲染。

## 管理台审查页

12. 顾客行为统一去掉 `REPLY_BEHAVIOR_` 前缀；员工行为按状态推断（staff 行为未落库，
    限制记录）。
13. 员工引用有效性要求快照版本就是当前 active 版本且文档 `status='active'`；
    顾客引用同样要求文档 active。
14. 游标改为 `(created_at, kind, id)` 复合游标，避免同一微秒的跨来源行被跳过；
    只有实际截断时才返回 `next_before`。
15. `AnswersPage` 增加列表/详情请求序号，详情错误不再清空列表。

## 前端/脚本/测试

16. `CustomerChat`：401/403/404 关闭选择面板并回到重新进入；创建会话复用同一
    Idempotency-Key；卸载后不再新建 SSE watcher。
17. `PageHeader` 支持 `level`，回答页用 h2；`admin.browser.mjs` 恢复单 h1 断言。
18. `workspace.browser.mjs` 的 cancel 用例删除多余的 `restored(turn)`（取消没有回答气泡）；
    v2-lifecycle/restart/authority/keycloak 用例移除已废弃的证据面板选择器。
19. `isolated-stack.sh` 启动失败时用 pid 文件兜底清理；Vite 直接以 node 启动，
    stop 能按进程组结束；`customer-service.browser.mjs` 用 waitFor 处理选择面板时序。
20. `generate_commerce_dataset.py --write` 只允许覆盖本生成器产出的包，需 `--force`
    才能替换其他目录；commerce-sim 的工单 JSON 改为 snake_case。

## 验收命令

```bash
cd go && go build ./... && go vet ./...
# 以 integration 环境文件设置 V2_INTEGRATION_ENV（写法见 docs/agent-entry.md §4）
cd go && go test ./internal/customer ./internal/admin ./internal/gateway ./internal/auth
cd python && uv run --frozen pytest -q
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
# dev 切 development 身份跑 smoke，再切回 oidc_browser
bash scripts/check.sh --smoke
bash scripts/check.sh --db
# 受影响浏览器用例：admin、customer-mobile、customer-lanes、workspace
```

## 限制 / 未运行

- selfhost 仍禁用 `V2_FACTS_BASE_URL`（既有部署策略），售后事实在 selfhost 按不可用转人工。
- 售后商品 SKU 仍只认当前消息（WP84 已记录）。
- staff turn 的 behavior 没有落库列；审查页按 state 推断，CLARIFY 仍显示为完成/回答。
- WP80 的 adoption 路径仍不加行锁（ingest 角色无 tenant/app_user/shop 的 FOR SHARE 权限）；
  revoke 与提交之间的窗口按 WP80 的已接受取舍保留，网关授权 + 审核发布是兜底。
- `v2_langgraph_fixture` 的同步图仍不消费 facts；生产 provider 图已修复，fixture 只作为测试替身。
- 真实在线模型链路未跑；mock 仍拒绝顾客输出。
- 数据集 returns/tickets 的时间与 SKU 归属不一致，列入 WP89。
- customer-control/source/service/selfhost-restore/keycloak 等受控 e2e 仍需专用环境。

## 不要做

- 不修改已执行迁移（只新增 0059）；不新增 SECURITY DEFINER；不改棘轮/预算/基线；
- 不把引用放回员工/商家前台；不放宽员工建议引用要求；不把空文档当通配。
