# 可观测性（重构方案 · 观测层）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充总纲未覆盖的**可观测性**维度：审计、日志、指标、链路、失败分类。

---

## 0. 现状（2026-10-27 实测）

| 层 | 现状 |
| --- | --- |
| 审计表 | `admin_audit`、`local_auth_event`（含 `detail jsonb`）、`usage_log`（token 用量） |
| 事件/轨迹表 | `customer_event`、`sse_event`、`tool_call_trace` |
| 应用日志 | `go/internal` 内仅 **5 处** `log.*` 调用；Python 侧用标准 logging |
| 指标 | **无**（无 `/metrics`、无 OpenTelemetry） |
| 链路追踪 | **无**（Go→Python 的 gRPC 调用无 trace id 透传） |
| 健康检查 | gateway `/probe`（HTTPS+CA+Host 校验）、ai `selfhost-health.py` |
| 断线恢复 | 有 `v2-restart.browser.mjs`（6 步，含进程重启后续播） |

结论：**审计做得好，运行时可观测性几乎为零**——出问题时只能 `journalctl` + 猜。

---

## 1. 问题清单

### M1 没有请求级关联 ID

一次顾客提问会穿过 gateway → ai → provider → 回写 SSE。现在无法把"用户看到的失败"和"Python 侧的那条异常"对上。

### M2 没有关键指标

至少缺：请求量/错误率/延迟分位、`ai` 队列深度与任务年龄、`usage_log` 写入失败数、锁定/限流触发次数。

### M3 失败分类不统一

`split gate reasons`（0014）已经开了好头，但运行时的错误码仍分散：
HTTP 错误码、Python 异常类、审计 `result` 三者之间没有映射表。

### M4 日志没有统一结构

Go 侧 5 处 `log.Printf/Fatal`、Python 侧 `logging` 各写各的；没有 JSON 行格式，也没有"哪些字段必填"的约定。

---

## 2. 工作包

### M-W1　请求关联 ID（半天，优先级最高）

```
浏览器/客户端 → gateway 生成 request_id（已有 requestID 概念，复用它）
             → 通过 gRPC metadata 透传给 Python（x-request-id）
             → 写入：审计行 metadata、SSE 帧（可选）、日志行、错误响应体（非敏感）
```

验收：

```bash
# 一次顾客提问后，同一个 request_id 同时出现在：
#   admin_audit / customer_event 的 metadata、Go 日志、Python 日志
grep -c 'x-request-id' python/app/rpc/*.py go/internal/gateway/*.go   # 期望 ≥ 2
```

### M-W2　最小指标集（1 天）

不做 Prometheus 栈，先做 `/internal/metrics`（仅 loopback）+ 结构化日志计数：

| 指标 | 类型 | 来源 |
| --- | --- | --- |
| `http_requests_total{path,status}` | counter | gateway 中间件 |
| `http_request_duration_seconds` | histogram | 同上 |
| `ai_jobs_pending` / `ai_job_age_seconds` | gauge | ai 进程查库 |
| `usage_log_write_failures_total` | counter | providers/usage.py |
| `auth_lockouts_total` / `auth_rate_limited_total` | counter | localauth |

验收：`curl -s 127.0.0.1:8080/internal/metrics | head` 有上述键；且该端口不出 loopback。

### M-W3　错误码映射表（2 小时）

产出 `docs/error-taxonomy.md`：一行一个错误码，含 HTTP 状态、内部原因、是否可重试、用户可见文案、审计 `result` 取值。
**状态：骨架已建（21 行），随新增错误码持续补行**；`ingestion_queue_failed` 标注"原因未细分，待修"（见 `docs/refactor-input.md` 第 9 条）。

覆盖现有：`invalid_credentials`、`account_locked`、`rate_limited`、`account_revoked`、`password_change_required`、
`csrf_invalid`、`role_not_grantable`、`setup_already_completed`、`setup_requires_bootstrap` 等。

验收：`grep -c '^| ' docs/error-taxonomy.md` ≥ 15；且代码里每个 `writeJSON(... "error": "...")` 的字符串都能在表里找到。

### M-W4　日志规范（2 小时）

`docs/logging.md` + 机械检查：

- 行格式：JSON，必填 `ts`、`level`、`component`、`request_id`（有则带）、`msg`
- 禁止：打印 token/key/正文全文
- `scripts/check/check-logs.py`：Go 侧禁止新增裸 `log.Printf`（白名单 5 处），Python 侧禁止 `print(` 进 `app/`

---

## 3. 红线

1. `/internal/metrics` 只监听 loopback，不进离群暴露
2. 日志与指标不得包含顾客正文、密钥、token
3. 审计表只增不改，不允许为了"好看"改历史行

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 关联 ID | §M-W1 的 grep | ≥ 2 |
| 指标端点 | `curl 127.0.0.1:8080/internal/metrics` | 含 5 类键 |
| 错误码表 | `docs/error-taxonomy.md` 行数 | ≥ 15，代码全覆盖 |
| 日志规范 | `python3 scripts/check/check-logs.py` | PASS |

## 5. 与其它文档的边界

- 审计表结构变更属于数据层（`data-and-migrations-plan.md`）
- 指标端点属于运维层（`ops-and-release-plan.md` 的冒烟脚本会读它）
