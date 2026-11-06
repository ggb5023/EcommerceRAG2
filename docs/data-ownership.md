# 数据归属

[项目入口](../README.md) · [架构](architecture.md) · [后端重构细则](archive/backend-refactor-plan.md)

> 所有当前被写表均已声明；未声明预算 0，只许降。新增写入前必须先登记。

一张表只有一个写入方，或少数被明确批准的写入方。这张表是唯一来源：代码里的每一条
`INSERT` / `UPDATE` / `DELETE` 都必须落在下面声明的集合里，否则 `scripts/check/check-ownership.py` 失败。

## 口径

- **写入方的单位**：Go 是包名（`go/internal/<包>/`，例如 `localauth`、`admin`、`ingestclient`）；
  Python 是模块名（`python/app/<模块>/`，例如 `ingest`、`providers`）。
- **统计范围**：`go/` 与 `python/app/` 下的源码，排除 `*_test.go`、`python/tests/`、`go/internal/pb/`。
  迁移文件、测试夹具与只读查询不计入。
- **数据库角色管不到包**：`admin`、`localauth`、`ingestclient`、`customer` 共用 `rag_v2_gateway`
  一个角色，因此"单写者"只能由源码扫描兜住，不能靠 `GRANT` 表达。
- 本文只写归属与理由，不写实现细节；结构变化仍以 `sql/migrations/` 为准。

## 表归属

| 表 | 允许的写入方（包/服务） | 读取方 | 理由 | 钉住它的检查 |
| --- | --- | --- | --- | --- |
| `app_user` | `localauth` | `admin`、`auth`、`customer`、`gateway`、`localauth`；Python `ingest` 只读 | 账号属于本地登录模块：CLI 建号与员工创建共用同一个写入方法，admin 在**自己的事务里**调用它，账号行只有一处 SQL | `scripts/check/check-ownership.py`；`TestStaffCreationRespectsGrantBoundary` |
| `turn_decision` | `admin` | `admin`（B6 之后由管理端读） | 每次 execution 一行决策记录（`tenant_id, execution_id` 唯一），写入方只有 `store_turn.go`；与 execution/turn 终态同事务，`ON CONFLICT DO UPDATE`；chosen/rejected 空时为 `[]` 而非 NULL | `scripts/check/check-ownership.py`；`TestAppendTurnDecisionRoundTripAndOverwrite` |
| `local_credential` | `localauth` | `localauth` | 凭据与账号同生共死，必须和 `app_user` 同事务写入；`local_*` 表只由本地登录模块拥有 | `scripts/check/check-ownership.py`；`TestStaffCreationRespectsGrantBoundary` |
| `admin_audit` | `admin`、`localauth`、`ingestclient` | `admin` | 审计是横切关注点：控制命令受理/回执、账号初始化、管理操作都要留痕，三处都在同一个 gateway 进程内，收口会引入无意义的跨包依赖 | `scripts/check/check-ownership.py` |
| `inspection_report` | `admin` | `admin` | L3 巡检报告由管理控制面唯一写读；巡检只读业务表、只写本表，不改变任何发布状态 | `scripts/check/check-ownership.py` |
| `admin_permission_request` | `admin` | `admin` | 权限申请与审批属于管理控制面，只有一个入口 | `scripts/check/check-ownership.py` |
| `local_session` | `localauth` | `localauth` | 会话随账号吊销/改密失效，必须与账号同一事务 | `scripts/check/check-ownership.py` |
| `customer_session` | `customer` | `customer`、`auth` | 顾客匿名会话与员工会话的鉴权模型不同，各自拥有 | `scripts/check/check-ownership.py` |
| `document` | `ingest` | `retrieval`、`admin`、`auth`、`customer` | 资料由 Python 摄取侧写入；Go 只读，用于审核与授权 | `scripts/check/check-ownership.py` |
| `document_version` | `ingest` | `retrieval`、`admin`、`auth` | 版本状态迁移（审核/发布/回滚/撤回）在摄取侧执行 | `scripts/check/check-ownership.py` |
| `catalog_sku` | `ingest` | `retrieval`、`admin`、`gateway` | 商品表的类型化 SKU 行必须与 chunk、版本在同一摄取事务写入；Go/检索只读，不能绕过审核与发布 | `scripts/check/check-ownership.py` |
| `catalog_size_chart` | `ingest` | `retrieval`、`admin`、`gateway` | 尺码表行与商品行同属一个摄取版本；写入方只有 Python ingest，Go/检索只读 | `scripts/check/check-ownership.py` |
| `source_file` | `ingest` | `ingest`、`admin` | 上传清单的文件行由摄取侧建立，只增不改 | `scripts/check/check-ownership.py` |
| `source_manifest` | `ingest` | `ingest`、`admin` | 同上，清单哈希在摄取侧落库 | `scripts/check/check-ownership.py` |
| `usage_log` | `providers` | `providers`、`admin` | 0021：只记 token 用量，由 Python 的 ai 与 ingest 角色写入，Go 网关只读；0050 增加 nullable `request_id`/`execution_id`/`turn_id`，工作台 turn 填三列，顾客只填 `request_id`，ingest/eval 为 NULL，历史不回填 | `scripts/check/check-ownership.py` |
| `provider` | 预留：`admin`（P3 实现，当前无写入） | — | 租户供应商表结构（0034）已就位，接口与页面属 P3；写入方预先钉在管理控制面 | `scripts/check/check-ownership.py` |
| `provider_model` | 预留：`admin`（P3 实现，当前无写入） | — | 同上，随 `provider` 一起由管理控制面写入 | `scripts/check/check-ownership.py` |
| `turn` | `gateway` | `admin`、`customer`、`gateway` | 轮次状态与答案由网关用例层写；终态只由 Go 决定 | `scripts/check/check-ownership.py` |
| `conversation` | `gateway` | `gateway`、`admin` | 会话创建与标题更新由网关写 | `scripts/check/check-ownership.py` |
| `turn_idempotency` | `gateway` | `gateway` | 幂等键在创建/续接轮次的事务内写 | `scripts/check/check-ownership.py` |
| `conversation_execution` | `gateway` | `gateway`、`admin` | 每次执行的状态、答案与取消标记由网关写 | `scripts/check/check-ownership.py` |
| `message` | `gateway` | `gateway`、`customer` | 用户/助手消息随轮次事务由网关写 | `scripts/check/check-ownership.py` |
| `sse_event` | `gateway` | `gateway`、`frontend` | SSE 事件序列由网关写；前端只消费 | `scripts/check/check-ownership.py` |
| `evidence_snapshot` | `gateway` | `gateway`、`admin` | 每次执行的证据快照由网关检索阶段写 | `scripts/check/check-ownership.py` |
| `citation` | `gateway` | `gateway`、`admin` | 答案引用由网关生成阶段写 | `scripts/check/check-ownership.py` |
| `customer_conversation` | `customer` | `customer`、`admin` | 顾客会话归 customer 包唯一写者 | `scripts/check/check-ownership.py` |
| `customer_execution` | `customer` | `customer`、`admin` | 顾客执行状态归 customer 包唯一写者 | `scripts/check/check-ownership.py` |
| `customer_message` | `customer` | `customer`、`admin` | 顾客消息归 customer 包唯一写者 | `scripts/check/check-ownership.py` |
| `feedback` | `gateway` | `admin`、`gateway` | 轮次反馈由网关在自己的事务内写；隐藏 suggestion 保留策略按 turn_id 清理 | `scripts/check/check-ownership.py` |
| `routing_trace` | `gateway` | `gateway` | 路由追踪随轮次由网关写；隐藏 suggestion 保留策略按 request_id 清理 | `scripts/check/check-ownership.py` |
| `tool_call_trace` | `gateway` | `gateway` | 工具调用追踪随轮次由网关写；隐藏 suggestion 保留策略按 request_id 清理 | `scripts/check/check-ownership.py` |
| `retrieval_trace` | `retrieval`、`gateway` | `gateway`、`retrieval` | Python 检索写追踪；隐藏 suggestion 保留策略需要网关按 request_id 删除孤儿 | `scripts/check/check-ownership.py` |

| `chunk` | `ingest` | `retrieval`、`admin`、`customer`、`ingest` | 解析后的正文片段由 ingest 与版本/商品行在同一摄取事务写入；检索、管理审核和顾客授权只读 | `scripts/check/check-ownership.py` |
| `control_command` | `ingestclient` | `admin`、`ingestclient` | 控制命令的受理与回执由 ingestclient 写；admin 读取命令结果 | `scripts/check/check-ownership.py` |
| `customer_command` | `customer` | `customer` | 顾客命令幂等结果由 customer 包唯一写读 | `scripts/check/check-ownership.py` |
| `customer_event` | `customer` | `customer`、`admin` | 顾客会话事件随状态变化由 customer 写；handoff 报告由 admin 只读 | `scripts/check/check-ownership.py` |
| `customer_identity` | `customer` | `customer` | 匿名顾客身份归 customer 包唯一写者 | `scripts/check/check-ownership.py` |
| `customer_reply` | `gateway` | `gateway` | 员工建议/回答的终态回复由 gateway 写读，customer 通道不写此表 | `scripts/check/check-ownership.py` |
| `customer_retrieval_trace` | `retrieval` | `retrieval` | 顾客检索追踪由 retrieval 写入并读取，供诊断/保留策略使用 | `scripts/check/check-ownership.py` |
| `import_batch` | `admin` | `admin` | 导入批次由管理控制面唯一写入和读取 | `scripts/check/check-ownership.py` |
| `import_item` | `admin` | `admin` | 导入批次条目与批次同事务，由 admin 唯一写读 | `scripts/check/check-ownership.py` |
| `ingest_artifact_object` | `ingest` | `ingest` | 摄取对象日志由 ingest worker 写读，用于重试/清理 | `scripts/check/check-ownership.py` |
| `ingest_command_result` | `ingest` | `ingest` | 摄取命令结果由 ingest 服务写入和读取 | `scripts/check/check-ownership.py` |
| `ingest_event` | `ingest` | `ingest` | 摄取阶段事件由 ingest worker/服务唯一写读 | `scripts/check/check-ownership.py` |
| `ingest_job` | `ingest` | `admin`、`gateway`、`ingest` | 任务队列由 ingest 唯一写；admin 查询、gateway 只读队列 gauge | `scripts/check/check-ownership.py` |
| `ingest_job_item` | `ingest` | `admin`、`ingest` | 任务条目由 ingest 写；admin 读审核/任务视图 | `scripts/check/check-ownership.py` |
| `ingest_payload` | `ingest` | `ingest` | 摄取载荷由 ingest 写入并读取，不对外暴露 | `scripts/check/check-ownership.py` |
| `ingest_publication` | `ingest` | `ingest` | 发布记录由 ingest 命令写入并读取 | `scripts/check/check-ownership.py` |
| `ingest_worker_heartbeat` | `ingest` | `admin`、`ingest` | worker 心跳由 ingest 写；admin 只读运行概览 | `scripts/check/check-ownership.py` |
| `local_auth_event` | `localauth` | `localauth` | 本地登录审计由 localauth 唯一写读 | `scripts/check/check-ownership.py` |
| `review_suggestion` | `admin` | `admin` | 审核建议由管理控制面写入并读取，随版本保留策略清理 | `scripts/check/check-ownership.py` |
| `shop` | `admin` | `admin`、`auth`、`customer`、`ingest`、`localauth` | 店铺设置由 admin 写，身份/顾客/摄取侧只读 | `scripts/check/check-ownership.py` |
| `source` | `ingest` | `admin`、`auth`、`customer`、`ingest` | 资料来源由 ingest 创建，管理/授权/顾客侧只读 | `scripts/check/check-ownership.py` |
| `tenant_agent_review` | `admin` | `admin` | 租户审核开关由 admin 唯一写读 | `scripts/check/check-ownership.py` |
| `v2_asking_checkpoint` | `graph` | `graph` | ASKING 检查点由 Python graph 写入并恢复，Go 不直接读写该表 | `scripts/check/check-ownership.py` |

## 维护规则

- 新增写入方：先改本文件的集合，再改代码，提交里两者同时出现；脚本会核对。
- 新增表：先在本文登记，再写迁移。未登记却被写入的表，脚本只告警不失败，但新增登记属于同一次提交。
- 收口方向只能是"被调用方向"：`admin` 已经依赖 `localauth`，因此 `localauth` 不能反向调用 `admin`。
- 跨表原子性由调用方事务保证：写入方法接收调用方的 `pgx.Tx`，不自己开事务。
