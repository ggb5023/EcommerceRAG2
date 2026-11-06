# 84 售后只读事实 AI + 复制规则

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求售后 AI 只读查询、未解决转工单
- 前置：WP83 已关

## 目标

1. 顾客 scope 签名 `service_type`/`customer_order_id`，Python 校验；
2. 售后允许 `get_order_status`、`get_logistics`、`get_stock`、`get_price`；
   订单号只用会话绑定值；
3. 证据与事实都为空才转人工；事实快照持久化并在终态前复验；
4. `CanCopyReply` 接受「引用或非 mock 事实」；员工建议仍要求引用。

## 步骤

1. Go `IssueCustomerScope` 写入两字段；`customerScopeMismatch` 比较；
   Python `validate_scope` 缺省 pre_sales、拒绝无订单号的 after_sales；
2. `go/internal/customer/ai.go`：按服务轨道选择工具；模型给出的其它订单号→澄清；
   商品工具复用 `domain.RoutedProductResource`；事实写入 `facts_json`；
   终态重跑 `facts.Lookup` 复验授权/绑定；
3. `domain.CanCopyReply(state, behavior, citationCount, factCount, mock)`；
4. Python 售后 control/generation 提示词（只读、订单已绑定、禁止声称退款/改址/取消）；
5. 契约表新增「顾客服务 scope」；棘轮上调由主 agent 单独提交。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./...
cd python && uv run --frozen pytest -q
bash scripts/check.sh --smoke
bash scripts/check.sh --db
```

## 不要做

- 不写订单/退款/改址/取消；不让模型自由选择订单号；
- 不放宽员工建议的引用要求；不改 smoke 基线。
