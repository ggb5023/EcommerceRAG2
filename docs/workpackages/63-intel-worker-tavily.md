# 63 0032 阶段 2：抓取 worker + Tavily 接线 + 情报接口

- 状态：见 INDEX.md
- 依据：决策 0032 §4–§6；WP62 已建共享层/角色/域标记
- 前置：WP62 已关

## 三个设计落点（用户要求先定；本包内钉死，不另起决策）

| 落点 | 决定 |
| --- | --- |
| 独立进程 | 本地服务启动脚本（v2_services）新增第 3 个服务键 `intel`：`python -m app.intel.worker`，专属 env（`V2_INTEL_DSN`=rag_v2_intel 角色）、独立生命周期与就绪探针。gateway/ai 进程内**零抓取代码**。 |
| 限流 | worker 进程内 token bucket：`V2_INTEL_RPM`（默认 5，校验 1..60）；Tavily 适配器既有 429/rate_limited 分类继续复用（retryable → 任务回退），不新增错误类别。 |
| 默认关 | `V2_INTEL_ENABLED`（0/1，默认 0）。为 0 时 v2_services **不启动** intel 服务、worker 不占端口不占连接；dev.env 保持 0。smoke 五条基线必须逐字节一致。 |

## 迁移 0055（只追加；先登记再写库）

1. `docs/data-ownership.md` 登记 `intel_job` 与写者（`intel`，rag_v2_intel 角色，本包接线）。
   `market_observation` 的 WARN 同步消除（worker 写入）。
2. `0055_intel_job.sql`：复用 ingest 的租约语义、**不写 ingest_job**（single-writer）：
   `id, tenant_id NULL（共享任务）, watch_config_id NULL, market, category, platform,
   query, status CHECK(pending/running/done/failed), lease_expires_at, retry_count,
   next_attempt_at, error, observed_count, created_at, updated_at, fencing_epoch`；
   索引 `(status, next_attempt_at)`。GRANT 全部只给 `rag_v2_intel`（同迁移）。
3. `market_observation` 补去重约束：`UNIQUE (market, category, platform, source_url)`。
   **0054 已在库上执行——约束只能进 0055 新迁移**（ALTER TABLE ADD CONSTRAINT），
   不许改 0054。

## worker（python/app/intel/，三层纪律：编排→能力→摄取）

- `worker.py`：主循环（`V2_INTEL_POLL_SECONDS` 默认 60）：
  1. 扫 `intel_watch_config`（enabled）→ 无 24h 内 job 的配置生成 job
     （`RECRAWL_INTERVAL_HOURS=24` 常量唯一出处）；
  2. claim 到期 job（租约 + fencing 语义与 ingest worker 同构）；
  3. `TavilyProvider.search(query, ...)`（适配器零改动，只调用）；
  4. 写 `market_observation`（`observed_at=now()` 必填；UNIQUE 冲突 upsert
     payload+observed_at）；`observed_count` 回写 job；status done/failed+error 脱敏；
  5. `usage_log` 每调用一行：`tenant_id=NULL`、`slot='search'`、`model='tavily'`、
     `input_tokens/output_tokens=NULL`（**搜索只记次数，0021 口径**）、
     http_status/error_code 如实；失败也记行。
- 限流器在 worker 内（token bucket，`V2_INTEL_RPM`）；配置校验失败→进程拒绝启动
  （fail-closed）。
- `V2_INTEL_PROXY_URL`：接受空或 https URL，本包只校验与透传，**不做直连抓取**
  （Tavily 是 API 调用；直连抓取留后续并强制经代理，0032 §6.5）。

## 情报接口（proto + Go admin）

1. `proto/rag/v1/intel.proto`（待建）：
   `service IntelService { rpc ListObservations(ListObservationsRequest) returns
   (ListObservationsResponse) }`——请求 market/category/platform/since/limit（≤100），
   响应行含 market/category/platform/source_url/title/score/published_date/observed_at。
   `buf lint/build/breaking` + `buf generate` 提交 pb（buf.build 不可达就停下汇报）。
2. worker 挂载 gRPC server（复用既有 mTLS transport，地址 `V2_INTEL_GRPC_ADDR`
   默认 127.0.0.1:50054）。
3. Go：`go/internal/ports` 加 `IntelClient` 接口 + 实现；gateway admin：
   `GET /admin/v1/intel/observations`（merchant_admin/knowledge_reviewer，透传过滤）；
   `intel_watch_config` CRUD 走 gateway 直连 store（表已授权 rag_v2_gateway）：
   `GET/POST/PATCH /admin/v1/intel/watch-configs`。openapi.yaml 加 4 个 path。
4. 前端 `/admin/intel`：观测列表（market/category 筛选 + 截至 observed_at 时间）
   与监控配置编辑；无货币（0021）。

## 契约（先钉后写）

`docs/contracts.md` 新行「intel 观测读取」跨进程：Go 侧 ports.IntelClient +
admin handler / Python 侧 intel worker gRPC server；两侧测试
（Go：client mock 契约测试；Python：server 返回结构测试）落地后 ✅。

## 测试

- Python：job 生成/租约/fencing/退避表驱动；限流器（RPM 边界）；Tavily mock
  transport 成功/429/401；observation upsert 去重；usage_log 行数=调用数且 tokens
  为 NULL；enabled=0 不启动。
- Go：ports client、admin 观测过滤/限流参数、watch CRUD store 测试、403 路径。
- v2_services 测试：enabled=0 无 intel 服务、=1 有且 env 隔离（V2_AI_DSN 不外泄）。

## 验收命令

```bash
bash scripts/check.sh            # 44 项 FAILED 0（buf 三连/openapi/ownership/migration-grants 全绿）
cd go && go test ./internal/ports/ ./internal/admin/ -run "Intel|Watch" -v
cd python && uv run --frozen pytest -q tests/test_intel_worker.py tests/test_intel_rpc.py
bash scripts/check.sh --smoke    # 47 项基线逐字节一致（enabled=0，dev 无 intel 进程）
# 真实 Tavily（providers.env 有 TAVILY_API_KEY）：隔离栈 --identity development 起
# intel 服务，手动塞一条 watch_config → 等一个 poll 周期 → ListObservations 见行、
# usage_log 见 slot=search 且 tokens IS NULL、market_observation 唯一约束去重生效；
# 全程只发 1–2 次真实搜索（RPM=5 上限内）
```

## 不要做

- 不改 Tavily 适配器（零改动，只调用）；不新增错误类别；
- 不写 ingest_job（租约语义复用，表不复用）；不改 0054；
- 不做直连网页抓取/代理出口实际接线；不做情报向量检索；
- 不改 CONTRACTS_* 棘轮（NOTE 由主 agent 单独提交）；不加依赖；不动冻结评测集。

## 实施与验证（2026-11-06）

三个落点：独立进程 `scripts/dev/v2_services.py:40/271`（intel 专属 env，
enabled 时追加 `python -m app.intel.worker`，`:195` 健康探针）与
`python/app/intel/worker.py`；限流 `limiter.py:8 TokenBucket` +
`worker.py:142` RPM 1..60；默认关 `v2_services.py:123`，dev.env 为 0，
dev 无 intel 进程。

迁移/GRANT：
```text
PASS migration 0055 / PASS 55 migration checksums
PASS migration 0056 / PASS 56 migration checksums
```
0056 补 `SELECT ON schema_migration`（0055 已执行不可改；intel 启动需校验全量
checksum）。`intel_job` 仅 rag_v2_intel 的 SELECT/INSERT/UPDATE + sequence；
`usage_log` INSERT + sequence；`schema_migration` SELECT。`usage_log` 的 SQL 写点
仍是 providers 的 UsageRecorder（intel 调用），不新增第二个 writer。

真实 Tavily（隔离栈 enabled=1 RPM=5 poll=5s）：
```text
READY V2 gateway,ai,intel
watch=3 ecommerce/insulated bottle/competitor pricing
job=1 done observed=5 duration=2.09s
market_observation rows=5 distinct_url=5
usage_log=('search','tavily',NULL,NULL,NULL,NULL,NULL)
```
ListObservations 经 Go admin 返回 200 + 5 行；首行 title
`Pricing Strategy for Water Bottles...`，source_url
`https://www.harriswaterbottle.com/pricing-strategy-for-water-bottles...`。
去重 upsert：`before=5 after=5 title=dedupe-check`。成功路径适配器不暴露
HTTP status，usage_log 为 NULL；429/401 由单测覆盖。

测试：
```text
go ... -run "Intel|Watch" -v -> 5 PASS（integration CRUD PASS）
python ... tests/test_intel_worker.py tests/test_intel_rpc.py -> 9 passed
tests/test_v2_local_services.py -> 15 passed（enabled 0/1 与 env 隔离）
```
门禁：
```text
bash scripts/check.sh         -> PASSED 44 / UNCONFIGURED 0 / FAILED 0
bash scripts/check.sh --smoke -> PASSED 47 / UNCONFIGURED 0 / FAILED 0（逐字节一致）
```
`check-contracts` 输出 `NOTE contracts: 跨进程 rose to 18; raise CONTRACTS_CROSS`，
未改棘轮。

未覆盖：只发 1 次真实 Tavily 搜索；429/401 仅单测；无直连抓取/代理/向量检索；
测试库保留 5 条观测（一条 title=dedupe-check）与 1 条 usage_log；隔离栈已停；
未跑 `--db`/`--e2e`/远端 Actions。
