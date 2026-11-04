# 38 可观测性：关联 ID + 最小指标 + 日志规范

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[observability-plan](../archive/observability-plan.md) M-W1/M-W2/M-W4、
> [constraints](../constraints.md) §4、[environment-facts](../environment-facts.md)

## 目标

把归档计划 M-W1/M-W2/M-W4 落成可执行的最小实现：请求级关联 ID 贯穿
Go→Python，Go/Python 各自的 loopback 文本指标，以及日志字段与机械检查。
不引入 Prometheus/OpenTelemetry，不改产品行为、迁移或评测集。

## M-W1 关联 ID

- 复用 `RequestContext.request_id`；Go 调 Python 时通过 gRPC metadata
  `x-request-id` 透传，Python 同时读取 scope.request_id 与 metadata。
- Go 在 understand/search/generate/checkpoint 调用点输出 JSON 日志，带
  `component`、`request_id`、`rpc`；不打印 query、正文或凭据。
- Python RPC 入口输出 JSON 日志，带同一 `request_id`；不打印请求正文。
- 验收：`grep -c 'x-request-id' python/app/rpc/*.py go/internal/gateway/*.go`
  >= 2；一次建议/问答后同一 request_id 同时出现在 `/tmp/ecr-dev-logs/gateway.log`
  与 `ai.log`。

## M-W2 最小指标

标准库实现，无新依赖。

Go gateway 在独立 loopback `METRICS_ADDR`（默认 `127.0.0.1:9090`）暴露
`GET /internal/metrics`：

- `http_requests_total{path,status}` counter；`path` 使用 ServeMux 路由模板
  （`r.Pattern`），无法取得模板时归入 `/unmatched`，绝不使用原始 URL；
- `http_request_duration_seconds` histogram（固定桶）；
- `auth_lockouts_total` / `auth_rate_limited_total` counter，在 localauth 的
  锁定/限流计数点递增；
- `ai_jobs_pending` / `ai_job_age_seconds` gauge，gateway 只读查 `ingest_job`
  （pending/running 数量与最早任务年龄）；
- 启动期拒绝非 loopback `METRICS_ADDR`；指标不挂 8080。

Python ai 在 loopback 的 `AI_METRICS_ADDR`（默认 `127.0.0.1:9091`）暴露
`GET /internal/metrics`，输出 `usage_log_write_failures_total` counter；
启动期拒绝非 loopback 地址。两个端点都输出文本 `# TYPE` 行。

禁止标签包含 tenant 名称、query、prompt、密钥、DSN 或顾客正文。

## M-W4 日志规范

`docs/logging.md` 规定 JSON 行：必填 `ts`、`level`、`component`、`msg`，
有 request_id 则带 `request_id`；禁止 token/key/正文全文。

`scripts/check/check-logs.py`：

- Go 侧只允许 `go/internal/gateway` 现有 5 处裸 `log.Printf`，新增即失败；
- Python 侧禁止新增 `print(` 进入 `python/app/`；现有 CLI/进程失败两处按文件
  白名单保留；
- 接入 `scripts/check.sh` fast 层，门禁从 40 项变为 41 项（按 T1 记录）；
- 负向验证：临时新增一处裸 `log.Printf` 应 FAIL，撤销后 PASS。

## 验收命令

```bash
cd go && go test ./... && go vet ./...
cd python && uv run --frozen pytest -q
curl -s 127.0.0.1:9090/internal/metrics | head
curl -s 127.0.0.1:9091/internal/metrics | head
python3 scripts/check/check-logs.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：Go 重建后 `strings` 验证标记并重启 gateway/ai，启动时间晚于最后提交；
从非 loopback 地址访问 9090 失败；同一 request_id 的 Go/Python 日志；负向验证
两段输出。

## 不做

- 不引入 Prometheus/OpenTelemetry 或新依赖。
- 不改产品行为、迁移、评测集、棘轮/基线；不重采 smoke。
- 不做管理台指标页；不把内部指标暴露到公共端口。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（实现与验证后填写。）

## 未覆盖路径

（实现与验证后填写。）
