# 日志规范

本文只规定运行时日志字段与机械检查；指标口径见 [metrics](metrics.md)，关联 ID
的实现见 [WP38](workpackages/38-observability.md)。

## JSON 行

每条日志是一行 JSON，必填：

| 字段 | 说明 |
| --- | --- |
| `ts` | UTC RFC3339 时间戳 |
| `level` | `debug` / `info` / `warning` / `error` |
| `component` | 组件名，如 `gateway`、`customer`、`ai_rpc` |
| `msg` | 固定事件名或可检索的短消息 |
| `request_id` | 有请求级关联 ID 时必填；没有时省略 |

示例（值仅示意）：

```json
{"ts":"2026-11-04T00:00:00Z","level":"info","component":"gateway","msg":"rpc_request","rpc":"understand","request_id":"v2-..."}
```

## 禁止内容

日志、指标标签和错误响应不得包含 token/key、DSN、请求正文、prompt、顾客原文或
货币金额。请求级关联只使用 `RequestContext.request_id` 和 `x-request-id`，不把
query 拼进日志。

## 实现与检查

- Go 使用 `internal/observability.LogJSON`；网关既有 5 处裸 `log.Printf` 是
  迁移前基线，新增裸调用会被 `scripts/check/check-logs.py` 拒绝。
- Python 使用 `python/app/logging_config.py` 的 `JsonFormatter`；`python/app/` 下的 `print(` 除
  `python/app/ingest/cli.py` 的 CLI 输出外一律拒绝。
- 模块入口在启动时配置格式；测试通过 `caplog` 读取记录，不依赖控制台格式。

```bash
python3 scripts/check/check-logs.py
```

检查已接入 `scripts/check.sh` 默认 fast 层；门禁总数从 40 项变为 41 项。
`--smoke` 保持原有 45 项轮次链路基线，不重复运行该静态项；这不改变任何数值
棘轮或 smoke 基线。
