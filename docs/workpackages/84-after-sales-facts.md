# 84 售后只读事实 AI + 复制规则

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求售后 AI 只读查询、未解决转工单
- 前置：WP83 已关

## 目标

1. 顾客 scope 签名 `service_type`/`customer_order_id`，Python 校验；
2. 售后允许 `get_order_status`、`get_logistics`、`get_stock`、`get_price`；
   订单号只用会话绑定值，模型给出其它订单号→澄清；
3. 证据与事实都为空才转人工；事实快照持久化并在终态前复验；
4. `CanCopyReply` 接受「引用或非 mock 事实」；员工建议仍要求引用。

## 步骤

1. `IssueCustomerScope` 读取并签名两条轨道字段；`customerScopeMismatch` 比较；
   `ValidateCustomerScope` 校验互斥；Python `Scope`/`_v2_scope` 透传，
   `workflow.validate_scope` 缺省 pre_sales、售后必须有订单号；
2. `go/internal/customer/ai.go`：按轨道校验工具白名单；`afterSalesFactPlan`
   绑定订单、要求显式唯一 SKU；`lookupCustomerFacts` 按订单/商品分组调用
   `facts.Lookup`；搜索允许「有事实、无证据」；`Generate` 携带 facts；
   `finishReason` 在提交前重跑 `facts.Lookup` 复验绑定与授权；
3. 持久化：`customer_execution.facts_json`、`conversation_execution.facts_json`
   （员工轮次经 `setFacts` 落入终态事务；建议轮次 faktCount 恒 0）；
4. `domain.CanCopyReply(state, behavior, citations, facts, mock)`；
5. Python 售后 control/generation 提示词（只读、订单已绑定、禁止声称退款/改址/取消），
   registry 登记并升 `control` 版本；
6. 契约表新增「顾客服务轨道 scope」，两侧测试；棘轮上调由主 agent 单独提交。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./internal/customer ./internal/auth ./internal/domain
cd python && uv run --frozen pytest -q
bash scripts/check.sh
```

## 限制 / 未运行

- dev/隔离栈为 mock 画像，模型输出被 `ValidateProposal` 拒绝，售后事实型真实回答需
  `v2_langchain_bailian`；真实模型链路未在本轮运行（诚实汇报）。
- 商品 SKU 只接受当前消息中显式出现的唯一标识，不复用历史（比员工链路更严格）。

## 不要做

- 不写订单/退款/改址/取消；不让模型自由选择订单号；
- 不放宽员工建议的引用要求；不改 smoke 基线。
