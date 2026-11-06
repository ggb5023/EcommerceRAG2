# 62 0032 intelligence 域——阶段 1：共享层与域标记契约

- 状态：见 INDEX.md
- 依据：决策 0032（助手平台定位与抓取方案）；本包只做第三梯队的地基：
  共享层表 + `rag_v2_intel` 角色隔离 + scope 域标记契约；**不接爬虫**
- 前置：WP61 已关

## 目标

1. 共享层（market）与租户层（监控配置）两张新表 + 独立 DB 角色 `rag_v2_intel`：
   answer 侧角色（rag_v2_ai / rag_v2_ingest / rag_v2_gateway）**物理读不到共享层表**。
2. scope 域标记：proto 加字段、Go 签发、Python 校验，钉成跨进程契约。
3. 0032 状态转「已采纳」（只改状态行与 INDEX 引用，不重写条款）。

## 迁移 0054（只追加；先登记再写库）

1. 先读 `docs/data-ownership.md` 与其守卫（check-ownership）的登记规则，
   登记两张新表与写者：共享层写者 = 系统抓取 worker（角色 `rag_v2_intel`，
   阶段 2 接线）；租户层写者 = `admin`（商家配置自己的监控清单）。
   若守卫要求"有登记必有写点"而阶段 1 尚无写点，**停下汇报**，不绕过守卫。
2. `0054_intelligence_shared_layer.sql`：
   - `market_observation`：`id`、`market`、`category`、`platform`、`observed_at`
     **NOT NULL**（0032 §6.3 情报必须带时间）、`source_url`、`payload_json`、
     `observed_at` 索引；**无 tenant_id**（共享层，0032 §3）；
   - `intel_watch_config`（租户层）：`tenant_id` 外键、`market`、`category`、
     `platform`、`enabled`、`created_at`；只存「商家想看什么」，不复制共享数据；
   - **GRANT 只在同迁移内**（environment-facts #4）：`market_observation` 仅
     `SELECT` 给 `rag_v2_intel`（阶段 2 写入前再加写权限，或本迁移一并给
     SELECT,INSERT,UPDATE——以 ownership 登记的写者为准）；answer 侧角色
     **不得出现**在共享表的任何 GRANT 里；`intel_watch_config` 授权给
     `rag_v2_gateway`（admin 读写）与 `rag_v2_intel`（读）。
3. 角色 `rag_v2_intel` 属集群级：不在迁移里 CREATE ROLE；把角色创建加进
   `scripts/ops/selfhost-db-init.py`（CI/自部署入口）并在
   `docs/development.md` 记录本机创建命令（`CREATE ROLE rag_v2_intel LOGIN`）。
   本地 dev 集群由主 agent 执行创建，开发 agent 不必碰集群。

## scope 域标记（跨进程契约，先钉契约后写代码）

1. `docs/contracts.md` 新增一行（类型 `跨进程`）：「scope 域标记」——
   Go 侧：`go/internal/auth/authority.go` 签发时写入 `domain`（默认 `answer`，
   仅宿主可设 `intelligence`）；Python 侧：`python/app/chains/provider.py`
   `require_scope` 校验（未知值拒绝、缺省按 `answer`）；两侧测试落地后标 ✅。
2. proto：`RequestContext` 加字段 `string domain = 28;`（26 business_date、
   27 catalog_filter 之后）；`buf lint` / `buf build` / `buf breaking` 必须过；
   `buf generate` 重新生成 `go/internal/pb` 与 `python/rag/v1` 的 pb 文件并提交。
   **若 buf.build 远程插件不可达**：停下汇报（生成文件不许手改），不硬凑。
3. Go：签发处写 `domain`（scope 签名覆盖该字段）；Python：`require_scope`/
   `_v2_scope` 结构校验域值 ∈ {`answer`, `intelligence`}，未知值拒绝；answer 链路
   （turn/ingest）全部按 `answer` 传递，不改变行为。
4. 测试：Go authority 测试（默认 answer、显式 intelligence 签名与篡改被拒）/
   Python scope 校验测试（未知域拒绝、缺省 answer）；smoke 五条基线必须逐字节一致
   （域标记默认值不得改变任何既有链路行为）。

## 文档

- 0032 状态行「提议」→「已采纳」（一行改动，不动条款正文）；
  `docs/decisions/INDEX.md` 同步状态；`docs/contracts.md` 新行；
  `docs/data-ownership.md` 两表登记；`docs/development.md` 角色创建说明。

## 验收命令

```bash
bash scripts/check.sh            # 44 项 FAILED 0（buf-lint/build/breaking、contracts、ownership、migration-grants 全绿）
cd go && go test ./internal/auth/ -run Domain -v
cd python && uv run --frozen pytest -q tests/test_provider_scope.py
bash scripts/check.sh --smoke    # 47 项，五条基线逐字节一致（proto 改动后必须重建+重启，见 environment-facts #1）
# 隔离验证（本机）：以 rag_v2_ai 连接查 market_observation 必须 permission denied；
# 以 rag_v2_intel 连接可 SELECT（角色创建后由主 agent 复核，开发 agent 不碰集群角色）
```

## 不要做

- 不接 Tavily/爬虫、不加抓取 worker、不建情报页面/接口（阶段 2/3）；
- 不让 answer 域读取共享表（连 GRANT 都不许出现）；
- 不动 0001/product.md/ROADMAP 的暂缓清单（0032 已修订过，无需再改）；
- 不手改 pb 生成文件；不改 CONTRACTS_* 棘轮（新增 ✅ 行 NOTE 由主 agent 单独提交）；
- 不改历史迁移、不新增依赖。

## 实施与验证（2026-11-06）

角色由主 agent 创建；开发 agent 未执行 CREATE/ALTER ROLE。本机两个库均可见
`rag_v2_intel`（LOGIN、非超级用户），密码在仓外。迁移 0054 已应用两库：
```text
PASS migration 0054
PASS 54 migration checksums
```
同迁移 GRANT（answer 侧角色不出现在共享表）：
```sql
GRANT SELECT,INSERT,UPDATE ON market_observation TO rag_v2_intel;
GRANT USAGE,SELECT ON SEQUENCE market_observation_id_seq TO rag_v2_intel;
GRANT SELECT,INSERT,UPDATE,DELETE ON intel_watch_config TO rag_v2_gateway;
GRANT USAGE,SELECT ON SEQUENCE intel_watch_config_id_seq TO rag_v2_gateway;
GRANT SELECT ON intel_watch_config TO rag_v2_intel;
```
真实角色连接：
```text
rag_v2_ai/ingest/gateway market_observation SELECT=DENIED 42501
rag_v2_intel market_observation SELECT=OK rows=0
rag_v2_gateway/intel intel_watch_config SELECT=OK rows=0
```
`role_table_grants` 中共享表运行时角色只有 `rag_v2_intel`；租户表为 gateway
全写与 intel 只读。ownership 守卫输出：
```text
WARN intel_watch_config: declares admin but no write site found (document may be stale)
WARN market_observation: declares intel but no write site found (document may be stale)
PASS data ownership: 57 tables declared, 166 write sites checked
```
守卫不要求“登记必有写点”，只告警；阶段 1 无 worker/admin 写点，保留 WARN，
未写假 SQL、未用“预留”掩盖，阶段 2 接线后消失。
契约先行：新增「scope 域标记」；proto `string domain = 28;`，`buf generate`
生成并提交，Python `field 28 type 9`；`buf lint/build/breaking` 全过。
`check-contracts` 输出 `NOTE contracts: 跨进程 rose to 17; raise CONTRACTS_CROSS`，未改棘轮。
测试：
```text
go test ./internal/auth/ -run Domain -v -> 4 PASS
python ... pytest -q tests/test_provider_scope.py -> 5 passed
```
默认签发 answer；`IssueIntelligenceScope` 是宿主专用入口，未接任何路由；
`require_scope` 接受 answer/intelligence，未知拒绝，缺省 answer。
门禁：
```text
bash scripts/check.sh -> PASSED 44
bash scripts/check.sh --smoke -> PASSED 47（五条逐字节一致）
```
重建/重启：
```text
strings .local/v2-gateway | grep -c IssueIntelligenceScope -> 1
gateway/ai_process 启动 10:35:07，HEAD 458b9dd 10:34:29
```
0032 状态行改「已采纳」，decisions/INDEX 同步，条款正文未改。
未覆盖：无抓取 worker、Tavily、情报页面/数据行；未跑 `--db`/`--e2e`/浏览器/远端 Actions；
最终 `buf breaking` 以已含字段的 main 为基准，提交前旧基准运行已通过。
