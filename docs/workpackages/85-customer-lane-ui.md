# 85 顾客售前/售后入口 UI

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求顾客先选服务、售后填订单号
- 前置：WP84 已关

## 目标

1. 顾客入口先选服务类型；售后必须输入合法订单号并随会话创建提交；
2. 会话头部显示服务类型与绑定订单，工单状态（已提交/已领取）可见；
3. 售后显示「只读查询，退款/改址/取消由人工处理」提示；顾客引用抽屉保留；
4. 已有未关闭会话时直接进入，不重复要求选择。

## 步骤

1. `customer-api.ts`：`ServiceType`/`Ticket`/`ServiceSnapshot` 类型、`laneLabel`、
   `ticketStatusText`、`orderIdPattern`；400/409 文案；
2. `CustomerChat.vue`：服务选择面板（售前/售后）、订单号输入与前端校验、
   创建会话带 `service_type`/`order_id`；头部 lane chip；工单状态文案；
   售后只读提示；「开始新咨询」重新打开选择面板；
3. 新增浏览器用例 `frontend/tests/customer-lanes.browser.mjs`
   （选择面板先于输入框、售后订单必填、订单大写、工单文案）；
   更新 `customer-service.browser.mjs` 的新访客流程；
4. 单测覆盖 `laneLabel`/`orderIdPattern`/`ticketStatusText`。

## 验收命令

```bash
cd frontend && npm test -- --run && npm run lint && npm run build
# 浏览器用例的站点地址变量写法见 docs/design-system.md §5
cd frontend && node tests/customer-lanes.browser.mjs
bash scripts/check.sh
```

## 限制 / 未运行

- `customer-service.browser.mjs` 需要 Keycloak 受控环境，本轮未运行；只改了入口选择步骤。
- 顾客端仍保留引用抽屉（决策 0041 §2.6）。

## 不要做

- 不在顾客端显示员工/商家后台信息；不改变引用展示；
- 不改服务端接口、不改 smoke 基线。
