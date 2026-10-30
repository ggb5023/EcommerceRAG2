# 数据归属

[项目入口](../README.md) · [架构](architecture.md) · [后端重构细则](archive/backend-refactor-plan.md)

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
| `local_credential` | `localauth` | `localauth` | 凭据与账号同生共死，必须和 `app_user` 同事务写入；`local_*` 表只由本地登录模块拥有 | `scripts/check/check-ownership.py`；`TestStaffCreationRespectsGrantBoundary` |
| `admin_audit` | `admin`、`localauth`、`ingestclient` | `admin` | 审计是横切关注点：控制命令受理/回执、账号初始化、管理操作都要留痕，三处都在同一个 gateway 进程内，收口会引入无意义的跨包依赖 | `scripts/check/check-ownership.py` |
| `admin_permission_request` | `admin` | `admin` | 权限申请与审批属于管理控制面，只有一个入口 | `scripts/check/check-ownership.py` |
| `local_session` | `localauth` | `localauth` | 会话随账号吊销/改密失效，必须与账号同一事务 | `scripts/check/check-ownership.py` |
| `customer_session` | `customer` | `customer`、`auth` | 顾客匿名会话与员工会话的鉴权模型不同，各自拥有 | `scripts/check/check-ownership.py` |
| `document` | `ingest` | `retrieval`、`admin`、`auth`、`customer` | 资料由 Python 摄取侧写入；Go 只读，用于审核与授权 | `scripts/check/check-ownership.py` |
| `document_version` | `ingest` | `retrieval`、`admin`、`auth` | 版本状态迁移（审核/发布/回滚/撤回）在摄取侧执行 | `scripts/check/check-ownership.py` |
| `source_file` | `ingest` | `ingest`、`admin` | 上传清单的文件行由摄取侧建立，只增不改 | `scripts/check/check-ownership.py` |
| `source_manifest` | `ingest` | `ingest`、`admin` | 同上，清单哈希在摄取侧落库 | `scripts/check/check-ownership.py` |
| `usage_log` | `providers` | `providers`、`admin` | 0021：只记 token 用量，由 Python 的 ai 与 ingest 角色写入，Go 网关只读 | `scripts/check/check-ownership.py` |
| `provider` | 预留：`admin`（P3 实现，当前无写入） | — | 租户供应商表结构（0034）已就位，接口与页面属 P3；写入方预先钉在管理控制面 | `scripts/check/check-ownership.py` |
| `provider_model` | 预留：`admin`（P3 实现，当前无写入） | — | 同上，随 `provider` 一起由管理控制面写入 | `scripts/check/check-ownership.py` |

## 维护规则

- 新增写入方：先改本文件的集合，再改代码，提交里两者同时出现；脚本会核对。
- 新增表：先在本文登记，再写迁移。未登记却被写入的表，脚本只告警不失败，但新增登记属于同一次提交。
- 收口方向只能是"被调用方向"：`admin` 已经依赖 `localauth`，因此 `localauth` 不能反向调用 `admin`。
- 跨表原子性由调用方事务保证：写入方法接收调用方的 `pgx.Tx`，不自己开事务。
