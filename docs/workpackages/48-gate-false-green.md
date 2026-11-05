# 48 P2 门禁假绿修复：UNCONFIGURED 语义与覆盖面

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P2（2026-11-04 本机私有快照，未入库）、
> [agent-entry §4](../agent-entry.md)、[constraints §2](../constraints.md)

## 目标

修正四个守卫的假绿：缺前置时必须返回 exit 2 / UNCONFIGURED；openapi 必须枚举
真实路由；migration-grants 的 `GRANT ALL TABLES` 只覆盖它出现之前的表。只改
守卫脚本与文档，不改产品代码、迁移或 smoke 基线。

## a) service-logs

- 无 `ECR_SERVICE_LOG_DIR` 目录：改为 `exit 2`（UNCONFIGURED），不再 `return 0`。
- `check.sh` 的 fast 层改用 `run_optional`，exit 2 计入 UNCONFIGURED；目录存在但
  gateway/ai 日志为空仍 FAIL(1)。

## b) eval-corpus

- 语料缺件且 `ECR_REQUIRE_EVAL != 1`：`exit 2`（UNCONFIGURED）。
- 语料缺件且 `ECR_REQUIRE_EVAL=1`：`exit 1`（FAIL）。
- 语料存在且 metadata sha256 匹配：`exit 0`。

## c) openapi

- 从 `go/internal/admin/handler.go`、`routes_customer.go`、customer service 的
  `Routes`/`StaffRoutes` 和 `routes_health.go` 提取方法+路径模板，与
  `docs/openapi.yaml` 双向比对；缺路由、多路由、模板或方法不匹配均 FAIL。
- 补齐 admin/customer/support 路由到 openapi；auth/setup 与 internal validate 用
  显式枚举的 `EXTERNAL_ROUTES`/`EXCLUDED_ROUTES` 白名单，理由逐条写明，不允许 `*`。
- 负向：临时从 yaml 删一个路径 → FAIL；恢复 → PASS。

## d) migration-grants

- 当前把 `0022` 的 `GRANT ... ON ALL TABLES` 当成同一文件/后续表的隐式授权。
  改为按文件和语句位置：grant-all 只覆盖它出现之前创建的表；同一迁移中 grant-all
  之后 CREATE TABLE 必须有显式逐表 GRANT。
- 先实测 49 条迁移；若历史表因此被判无授权，停止并报告清单，不删检查。
- 负向：临时迁移建表 → FAIL；删除 → PASS。

## 验收命令

```bash
python3 scripts/check/check-service-logs.py            # 无目录 exit 2
python3 scripts/check/check-eval-corpus.py             # 无语料 exit 2
ECR_REQUIRE_EVAL=1 python3 scripts/check/check-eval-corpus.py  # 无语料 exit 1
python3 scripts/check/check-openapi.py                 # 路由枚举 PASS
python3 scripts/check/check-migration-grants.py        # 49 迁移 PASS
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不改产品代码、迁移或 smoke 基线；不调其它棘轮/预算。
- 不为通过门禁改断言或重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 未覆盖路径

- auth/setup/internal validate 不纳入 admin/customer 路由枚举，白名单逐条列明。
- 不验证运行时路由可达性；只验证注册模板与 openapi 一致。
- 不跑 `--db` / `--e2e` / `--all`。

## 实际交付

### service-logs

- 无日志目录：`UNCONFIGURED service logs ...` → exit 2。
- 空 `gateway.log` / `ai.log`：FAIL → exit 1。
- 恢复默认日志：`PASS service logs: gateway 446 rows, ai 435 rows, ingest skipped` → exit 0。
- `check.sh` fast 层改用 `run_optional`；模拟无日志目录时：
  `PASSED 41 / UNCONFIGURED 1 / FAILED 0`（旧实现会把它误计为 PASSED 42）。

### eval-corpus

- 无语料、未设 `ECR_REQUIRE_EVAL`：`UNCONFIGURED eval corpus absent ...` → exit 2。
- 无语料、严格模式：`FAIL eval corpus absent ...` → exit 1。
- 默认语料存在：`PASS eval corpus present; sha256=db5d27dc9281` → exit 0。
- `check.sh` fast 层改用 `run_optional`。

### openapi

- `docs/openapi.yaml` 补齐 admin/customer/support 路由：67 paths / 73 operations。
- 守卫从 admin handler、routes_customer、customer service、routes_health 提取
  68 条注册路由；1 条 loopback internal validate 明确排除，6 条 auth/setup
  外部路由逐条列明。
- 当前输出：
  `PASS openapi paths=67 operations=73 refs=0 routes=68 external=6 excluded=1`。
- 负向：临时删除 `/admin/v1/merchant/usage` → `FAIL missing openapi route ...`；
  恢复后 PASS。

### migration-grants

- 改为按文件和语句位置比较：`GRANT ALL TABLES` 只覆盖在其之前创建的表；
  同一文件中 grant 之后 `CREATE TABLE` 仍需逐表 GRANT。
- 当前 49 条迁移、69 张表全部 PASS。
- 负向：临时迁移先建 `wp48_before_grant`、再 `GRANT ALL TABLES`、再建
  `wp48_after_grant` → 仅 `wp48_after_grant` FAIL；删除草案后 PASS。

### 文档与数字

- `agent-entry.md` 明确：缺前置脚本 exit 2，`check.sh` 用 `run_optional` 记
  UNCONFIGURED，`--all` 严格模式仍失败。
- `constraints.md` 同步 service-logs、eval-corpus、migration-grants 的语义。
- `check-doc-numbers.py` 的 fast 计数改为包含 `run fast` 与 `run_optional fast`，
  当前仍是 `fast=42`。
