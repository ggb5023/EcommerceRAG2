# 0043 窄订单族检测

- 状态：已采纳
- 日期：2026-11-08
- 工作包：[96](../workpackages/96-adoption-lock-order-family.md)
- 关联：[0041](0041-customer-service-lanes-and-tickets.md)、[contracts](../contracts.md)

## 问题

售后会话绑定一个订单，AI 只允许查该订单。Go 已有两道检查：

- `ExplicitOrderReferences`：只认“订单号 / 订单编号 / order id”后面的字面量；
- `afterSalesFactPlan` 的 entity 检查：模型报出不同 `order_id` → 澄清。

评测用例 `after-102` 暴露第三条路径：顾客写“帮我查一下 ORD-2609-0001 的物流到哪了？”，
绑定订单是 `ORD-2609-0005`。这句话没有显式线索，控制模型也可能不报 `order_id`
entity，于是两道路径都不触发，查询落到绑定订单上——顾客问的是另一个订单，却拿到
绑定订单的事实。

## 决定

`go/internal/domain/fact_resource.go` 新增
`OrderFamilyReferences(query, bound, ignore []string) []string`，只在
`afterSalesFactPlan` 确认请求了订单工具（`get_order_status` / `get_logistics`）后调用；
任何命中 → `order_clarification`，不查询。判定规则全部为纯函数：

- 候选 token 取自 query 的标识符形状片段，必须同时含字母、数字，并至少含一个
  `-` 或 `_`；纯数字、无分隔、无字母的 token 不参与；
- 按 `-`/`_` 切段；段数必须与 bound 相同；
- 首段与 bound 首段大小写不敏感相等；
- 每段的字符类（纯字母 / 纯数字 / 混合）与 bound 对应段相同；
- 排除 bound 本身与模型上报的 entity 值（`ignore`）；
- bound 纯数字或无分隔时函数返回 nil：没有可靠的“族”，不猜。

`ExplicitOrderReferences`、`RoutedOrderResource` 与 entity 检查保持不变，本规则只是
在它们之后补一条兜底。

## 不采用全量扫描的理由

裸标识扫描会把商品 SKU、政策编号、数量或日期里的数字误判成订单，导致正常售后咨询
被反复要求确认。窄族检测只覆盖“与绑定订单同前缀、同段数、同字符类”的写法，
典型是同一店铺订单号只差流水段；错误代价是澄清（安全），不是查询（越权）。

## 残余风险

- 假阴性：顾客写完全不同前缀的订单（例如把 `ORD-` 写成 `AB-`）仍会落到绑定订单；
  v1 依赖显式线索与 entity 检查兜底，后续如需更严可加确认步骤；
- 假阳性：与订单同族形状的 SKU/编号会触发澄清；这是安全方向的误报；
- 规则不做历史消息扫描，只检查当前请求，与 `routedCustomerSKU` 一致；
- 纯数字订单体系（bound 为纯数字）不启用该检测，仍只靠显式线索与 entity。

## 验证

- `go/internal/domain/fact_resource_test.go:TestOrderFamilyReferencesOnlyMatchTheSameIdentifierFamily`：
  同族不同订单、大小写、下划线、不同前缀、纯数字、绑定订单重复、entity 排除、
  段数/字符类不符、无分隔与纯数字 bound。
- `go/internal/customer/service_test.go:TestAfterSalesFactPlanBindsOrderAndSKU`：
  `after-102` 形态（无 entity）、同族 SKU 形状、不同前缀、纯数字、绑定订单重复、
  仅商品工具时不误报。
