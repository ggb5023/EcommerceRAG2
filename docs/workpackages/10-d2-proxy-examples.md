# 10 D2 前置代理示例（nginx / Caddy）

> 状态：见 INDEX.md　适用提交：7c5e710　验收：见 §验收命令
> 依据：`docs/decisions/0026-proxy-fronted-deployment.md` R4-1..R4-8

## 目标

让 D2（前置代理终止 TLS、网关只监听 loopback 明文）开箱可抄：
交付 nginx/Caddy 示例、安装文档一节，以及一个只用标准库的本地代理验证程序。

## 改动范围

- `docker/selfhost/examples/nginx.conf`、`docker/selfhost/examples/Caddyfile`：
  单商家单域名示例，带 R4-2/3/4 注释与网关侧配套说明（`WEB_ROOT` 为空、
  `HTTP_ADDR` 绑 loopback、`V2_CUSTOMER_PROXY_HOST`、与 D1 互斥）。
- `docs/install.md`：新增「前置代理部署（D2）」一节（TLS 终端、SPA、协议头注入一次、
  SSE 不缓冲四件事，指向 examples）。
- `docs/agent-entry.md` §7「部署形态 / 前置代理」行补 `docs/install.md` D2 节。
- `go/cmd/d2-proxy/main.go`：标准库 `httputil.ReverseProxy` 最小前置代理；
  注入一次 `X-Forwarded-Proto`（不覆盖已有值）、`FlushInterval=-1`、
  反代 `/v1` `/admin/v1` `/v1/customer`（不重写路径）、SPA fallback 到 前端构建产物（`npm run build` 输出）。
- `scripts/dev/d2-proxy-smoke.sh`：起随机端口代理，验证 API 200、SSE 有帧、SPA 200 html；
  接入 `scripts/check.sh --e2e`（`run_optional`，无 Chromium 依赖）。

## 不做

- 不改产品 Go/Python/前端代码；不改迁移/棘轮/基线。
- 不把 examples 接进 Compose；不新增依赖（代理只用标准库）。
- 不做 WP11/WP12；不处理 08 的残留美化项。

## 验收命令

```bash
cd go && go build ./cmd/d2-proxy && go vet ./...
bash scripts/dev/d2-proxy-smoke.sh
bash scripts/check.sh
bash scripts/check.sh --smoke
bash scripts/check.sh --e2e
cd frontend && npm test && npm run build
python3 scripts/check/check-docs.py
python3 scripts/check/check-doc-paths.py
```

## 未覆盖路径

- **nginx/caddy 语法未校验**：本机没有 nginx/caddy 二进制，示例只做逐行审查；
  真实证书、域名、TLS 握手与 HTTP/2 未验证。
- 代理验证程序只模拟 D2 的三件事，不替代真实前置代理；SSE 验证使用已完成 turn 的
  持久事件回放，不是实时流持续推送。
- `check.sh --e2e` 的 D2 项依赖本机 dev 栈 8080 与 前端构建产物（`npm run build` 输出）；缺任一项输出
  UNCONFIGURED，不算通过。
- D1 内置 edge 与真实 ALB/云厂商代理未在本轮验证；D4 由既有
  `validateDeploymentForm` 单测覆盖，不在本包新增。

## 实际交付 vs 计划

- `07e83eb`：工作包 + INDEX 行；`4ae9476`：examples、install/agent-entry 文档、
  `go/cmd/d2-proxy`、`scripts/dev/d2-proxy-smoke.sh`、check.sh `--e2e` 接入。
- examples：nginx/Caddy 均带 R4-2（协议头一次）、R4-3（SSE 不缓冲）、R4-4（SPA/API 同源不重写）
  注释与网关侧配置说明；D1/D4 互斥已注明。
- `d2-proxy-smoke.sh` 三步 PASS：API 200 + `scored_total`；SSE 收到 `event:`/`status`；
  SPA 200 text/html。`check.sh --e2e` 项 `d2-proxy-smoke` PASS；fast 40、smoke 45 不变。
- 未覆盖：nginx/caddy 语法未校验（本机无二进制）、真实 TLS/域名/HTTP2 未验证、SSE 用持久事件
  回放而非实时持续流、D2 项依赖本机 dev 8080 与 前端构建产物（`npm run build` 输出）。
- WP10 保持 `active`；是否转 `done` 等主 agent 复核。
