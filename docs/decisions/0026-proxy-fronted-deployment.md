# 0026 部署形态：内置 edge 与前置代理二选一

- 状态：提议
- 日期：2026-10-28
- 关联：[0004](0004-retained-invariants.md)（11 条不变量）、[architecture](../architecture.md)、[0025](0025-code-level-rules.md)

## 背景

网关有两种"站在最前面"的方式，代码里都能跑，但叠在一起会出错：

- `WEB_ROOT` 非空时，网关自己承担 edge 角色：终止 TLS、提供 SPA 兜底、可选身份代理（`gateway/main.go:Main` → `edge.Routes`）。
- `WEB_ROOT` 为空时，网关只提供明文 HTTP，前面通常还有一层 nginx/ALB 终止 TLS。

`edge.Proxy` 的两个行为让叠加变成错误：

1. 无条件 `Header.Set("X-Forwarded-Proto", "https")`（`go/internal/edge/edge.go:33`）——即使真实链路是
   "前置代理 → 明文 → 内置 edge"，协议头也会被再写一遍，下游无法判断原始协议；
2. `FlushInterval = -1`（`edge/edge.go:26`）——反向代理层为了 SSE 关闭缓冲；
   前置代理若也做缓冲，事件会被二次吞掉。

因此需要一个明确的形态矩阵与启动期断言。

## 决定

### 部署形态矩阵

| 形态 | WEB_ROOT | TLS 终止 | 谁提供 SPA | 状态 |
| --- | --- | --- | --- | --- |
| **D1 内置 edge** | 非空 | 网关 | 网关（`edge.SPA`） | 支持 |
| **D2 前置代理** | 空 | 前置代理 | 前置代理 | 支持 |
| **D3 本地开发** | 空 | 无（loopback 明文） | Vite（5173） | 支持 |
| **D4 前置代理 + 内置 edge** | 非空且前置代理存在 | 两段 | 两边都想提供 | **不支持** |

### 实施约束（重构期间）

- **R4-1 单入口**：浏览器流量只到 Go；D1/D2 二选一，**不得叠加**（D4 不支持）。
- **R4-2 协议判定只做一次**：D1 由内置 edge 注入 `X-Forwarded-Proto`；D2 由前置代理注入并配置可信代理；
  任何一层都不得重复改写。
- **R4-3 SSE 不得缓冲**：`edge.Proxy` 的 `FlushInterval=-1` 保持不变；前置代理必须关闭响应缓冲。
- **R4-4 静态与 API 同源**：D1 下 SPA 由网关提供；D2 下由前置代理提供；`/v1`、`/admin/v1`、`/v1/customer`
  路径不得改写。
- **R4-5 明文只允许 loopback**：D2/D3 的监听地址必须是 loopback（由 R4-8 断言）。
- **R4-6 身份代理按档位启用**：只有 `oidc_browser` 需要 `V2_IDENTITY_PROXY_URL`；D2 下若前置代理接管
  `/identity`，不得再启用内置身份代理。
- **R4-7（补充）D2 形态必须 `WEB_ROOT` 为空**：检测到"`WEB_ROOT` 非空 且配置了外部代理信任
  （`V2_CUSTOMER_PROXY_HOST`）"时启动失败，提示二选一。
- **R4-8（补充）明文监听必须 loopback**：`WEB_ROOT` 为空时，监听地址非 loopback（含 `:8080`、`0.0.0.0:8080`、
  非回环 IP/域名）一律启动失败。

落地位置：`go/internal/gateway/deployment_form.go` 的 `validateDeploymentForm`，在 `Main` 解析 `HTTP_ADDR`
之后立即调用；单测覆盖 D1/D2/D3 通过、D4 拒绝、四种非 loopback 地址拒绝。

## §4 与 W2 步 3 的签名约定

阶段函数不接收 `*http.Request`。入口（`runTurn` 的调用方）一次性算出：

```go
type turnRequestContext struct {
	host     string // 请求 Host
	isSecure bool   // 依据 R4-2 的唯一判定结果
	clientIP string // 依据可信代理链解析后的客户端地址
}
```

阶段函数只接收 `turnRequestContext` 与业务数据；`check-stage-boundaries.py` 断言
`go/internal/gateway/turn_*.go` 的函数签名中不出现 `*http.Request`。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 允许 D4，并在 edge 里检测"已有 X-Forwarded-Proto 就不再覆盖" | [Go httputil.ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy) | 协议判定分散到两层，排障时要同时看两份配置；且 `FlushInterval=-1` 与前置代理的缓冲策略仍会互相遮蔽 |
| 干脆删掉内置 edge，只支持 D2 | [本仓库 docs/install.md](../install.md) | 单机自托管（商家只给一台机器、一个端口）会失去开箱即用的 HTTPS；D1 是产品的默认交付形态 |
| 用环境变量做运行时开关（如 `ECR_EDGE_MODE`） | — | 多一个必须同步的状态；形态由 `WEB_ROOT` 是否为空即可唯一确定，再加开关只会制造组合爆炸 |

## 后果

- D4 从"能跑但行为可疑"变成"启动即失败"，错误信息直接给出二选一建议。
- 非 loopback 的明文监听（历史上用于临时远程调试）不再被允许；需要远程访问时走 D1 或 D2。
- 本决策是**提议**状态：它约束实现，不改变任何对外接口；与 11 条不变量冲突时不变量优先（同 [0025](0025-code-level-rules.md)）。
