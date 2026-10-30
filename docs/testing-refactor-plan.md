# 测试体系重构方案（交付 DSH 执行）

- 版本：**v2**（2026-10-27 修订）
- 仓库：`/data/app/EcommerceRAG2`
- 分支：`main` @ `5baa01c`（m8 已按 `--no-ff` 合入 main，起点标签 `refactor-start`；`main == origin/main`，工作区干净）
- 执行方式：按批次推进，每批次单独提交、单独跑门禁，**停下汇报等确认**

### 修订记录

| 版本 | 变更 |
| --- | --- |
| v1 | 初版：现状基线 + 四批次 |
| v2 | ① 去掉悬空引用 W1/W2/M9，改为可自行判定的前置条件；② 真库层改为按用例分组指定 env 文件（原"字母序取第一个"会永远选错）；③ 定义 `UNCONFIGURED` 中 Go 部分的计数方法；④ 补 `--cov` 参数与 `--all` 耗时预算；⑤ 前端用例数以运行时为准（37）；⑥ 迁移从零补建库权限与清理方式 |
| v3 | 批次 1 验收补两条硬条款：新旧 `check.sh` 对照（旧脚本无参数解析，`--all` 会被静默忽略，基线按无参数全量记录）与检查项差集（新增项列出、消失项为空，按实际执行项比对） |
| v4 | 明确差集口径：对照清单为纯条目文件，按 `id` 比对；每项固定打印 `[<layer>] <id> <命令摘要>` |
| v5 | `--all` 改为严格语义（有 `UNCONFIGURED` 即 exit 1 + `STRICT` 行），新增 `--allow-unconfigured` 显式放宽 |
| v6 | 记录 e2e 层当前状态（12 项缺前置、2 项真实缺陷，批次 3 前恒红）；批次 3 补 e2e 前置声明、独立栈、`--cov` 实装、`db-env-health` |

---

## 0. 前置条件（开工前必须先确认，不满足则停止并汇报）

```bash
git status --porcelain            # 必须为空
git rev-parse main                # 记录
git diff main -- scripts/check.sh # 必须无输出（main 与本分支的 check.sh 一致）
```

若 `scripts/check.sh` 在其它分支上存在未合并改动（用 `git log --all --oneline -- scripts/check.sh` 检查最近 5 条），**停止并汇报**，不要并行修改该文件。

> 说明：本方案批次 1 会重写 `scripts/check.sh` 的参数解析。该文件是共享门禁，与其它工作流并行修改会产生冲突。

---

## 1. 现状基线（实测于 2026-10-27，勿用估算值）

| 层 | 数量 | 默认门禁是否执行 | 说明 |
| --- | --- | --- | --- |
| Go 单测 | 33 文件 / 98 个 `Test` 函数 / 10 包 | 是 | `go test -p 2 ./...` |
| Go 集成 | 6 文件 | **否** | 需 `V2_INTEGRATION_ENV` |
| Python 单测 | 收集 378 / 通过 350 / 跳过 28 | 是 | 28 个跳过中 20+ 因缺 `V2_INTEGRATION_ENV` |
| 前端单测 | 10 文件 / **运行时 37 用例** | 是 | `Tests 37 passed`（grep `it(` 只有 35，以运行时为准） |
| 前端构建 | `vue-tsc --noEmit` + `vite build` | 是 | |
| 浏览器 E2E | 14 个 `frontend/tests/*.browser.mjs` | **否** | 手动运行 |
| 评测自测 | 23 文件 / 288 用例 | 是 | `eval/run.sh offline` |
| 覆盖率 | 零配置 | — | go / pytest / vitest 均未接入 |
| 固定等待 | 浏览器测试 10 处 `waitForTimeout` | — | 分布在 3 个文件 |

**核心问题**：测试数量充足，但成本最高的三层（真库集成、浏览器 E2E、迁移从零）不在任何门禁内；覆盖率零配置，无法判断新增代码是否被测到；"测试通过"的可信度依赖执行者是否记得手动跑。

---

## 2. 目标（可量化验收）

| # | 目标 | 验收方式 |
| --- | --- | --- |
| G1 | 门禁分三层；快速层 ≤ 45s，全量 `--all` ≤ 6 分钟 | `time bash scripts/check.sh [--all]` |
| G2 | 环境缺失导致的跳过**单独显形** | 门禁末三行含 `UNCONFIGURED n` |
| G3 | 真库层与浏览器层各一条命令可跑 | `check.sh --db` / `--e2e` 均 exit 0 |
| G4 | 覆盖率可测量并有基线 | `.local/coverage/` 有产物 + `docs/testing.md` 记录基线 |
| G5 | 固定等待不再新增 | 门禁断言计数 ≤ 10 |
| G6 | 迁移可从零建库 | 临时库全量 apply 通过 |

### 2.1 各层耗时预算

| 层 | 预算 | 超出时的处理 |
| --- | --- | --- |
| 快速层（默认） | ≤ 45s | fail，打印最慢的三个步骤 |
| 真库层（`--db`） | ≤ 2 min | 打印耗时，不 fail |
| 浏览器层（`--e2e`） | ≤ 3 min | 打印耗时，不 fail |
| 全量（`--all`） | ≤ 6 min | fail，打印各层耗时分解 |

**先测量再优化**：开工第一步执行 `time npm ci --prefix frontend`，把结果记入汇报。若该步骤 < 8s，则 2.2 节之外的 `npm ci` 降频逻辑简化成"仅在 `--all` 时执行"。

---

## 3. 批次 1：门禁分层（P0，先做）

### 3.1 命令契约

```bash
bash scripts/check.sh            # 快速层：静态 + 单测 + 构建；无 DB、无浏览器
bash scripts/check.sh --db       # 快速层 + 真库层
bash scripts/check.sh --e2e      # 快速层 + 浏览器层
bash scripts/check.sh --all      # 快速层 + 真库层 + 浏览器层
bash scripts/check.sh --cov      # 附加覆盖率采集与基线比对（批次 2 实现，批次 1 先保留参数位）
```

要求：

- 参数可组合；未知参数打印用法并 `exit 2`
- 任一层失败立即 exit 非 0，并打印失败层名字
- 每层开始与结束各打印一行 `BEGIN <layer>` / `END <layer> <耗时>`
- 所有临时产物写入 `.local/`（已 gitignore）

### 3.2 各层内容

**快速层（默认）**

```
scripts/check/privacy_guard.py
scripts/check/check-docs.py
scripts/check/check-openapi.py
scripts/check/check-doc-baseline.py
scripts/check/check-eval-corpus.py
shellcheck -S warning scripts/*.sh scripts/*/*.sh eval/*.sh docker/db/*.sh docker/db/init/*.sh
buf lint && buf build -o /dev/null && buf breaking --against .git#branch=main
go vet ./... && go test -p 2 ./...
pytest（不设 V2_INTEGRATION_ENV）
ruff check（含 eval/ 与 scripts/dev）
前端 npm test 与 npm run build
scripts/check/facts-fake-matrix.sh
eval/run.sh offline
```

**真库层（`--db`）—— 按用例分组指定 env，不按字母序取第一个**

机器上存在两个不同的 env 文件，用途不同，混用会写错库：

| env 文件 | 归属用例 |
| --- | --- |
| `/etc/ecommerce-rag/import-integration.env` | `TestImportBatch*`、`./internal/customer`、`./internal/ingestclient` |
| `/etc/ecommerce-rag/test-integration.env` | `TestDatabaseAdministrativeAccess*`、`./internal/localauth` |

执行方式：

```bash
V2_INTEGRATION_ENV=/etc/ecommerce-rag/import-integration.env \
  go test ./internal/admin -run 'TestImportBatch' && \
  go test ./internal/customer ./internal/ingestclient

V2_INTEGRATION_ENV=/etc/ecommerce-rag/test-integration.env \
  go test ./internal/admin -run 'TestDatabaseAdministrativeAccess' && \
  go test ./internal/localauth

# Python 真库测试：两者皆可，优先 test-integration.env
V2_INTEGRATION_ENV=/etc/ecommerce-rag/test-integration.env pytest
```

两个文件都不存在 → 打印 `WARN db layer skipped: no integration env`，计入 `UNCONFIGURED`，**不 fail**。

**浏览器层（`--e2e`）**

```
启动独立 mock 栈（26081 / 26063）+ 独立 Vite（5174）+ 独立 accept 库
按字母序执行 frontend/tests/*.browser.mjs 全部用例
结束：停栈、断开库连接、打印报告目录路径
约束：不得触碰 dev 栈（8080 / 5173）与 ecr_v2_dev_local
任一用例失败 → 该层 fail，打印报告目录；其余用例继续跑完再退出
```

**e2e 层当前状态（2026-10-28）**

`--e2e` 共 14 项，当前 **0 通过**，分两类，互不阻塞：

- **12 项缺前置**（批次 3 补齐后才会真跑）：依赖独立栈 26081/26063、独立 Vite 5174、
  独立 accept 库、受保护报告目录或合成账号。它们在用例入口就显式拒绝执行
  （`protected synthetic accounts required`、`explicit protected offline V2 runtime required`、
  `ERR_CONNECTION_REFUSED http://127.0.0.1:5174/...` 等），不是产品回归。
- **2 项是真实缺陷**：`e2e-admin`（`/admin/merchant/shops` 在 390px 下横向溢出 3px）、
  `e2e-workspace`（`strictEqual` 期望与实际不符）。二者单独提 `fix:` 提交修，不阻塞 T1 合并。

因此 **批次 3 完成前 `--e2e` 恒为红，属预期**；默认层与 `--db` 不受影响，
`--all` 会因此 exit 1（严格语义），发布前跑 `--all` 时必须先补齐前置或显式记录豁免。

**实测更正（2026-10-28，T1 合并后）**

- `e2e-admin` 的 390px 溢出是真实缺陷，已修：根因是店铺行的 `ShopSettings` 表单最小内容宽 272px
  撑开了 `1fr` 栅格轨道（轨道按 min-content 取 383px，正文起点 10px → 文档 393px）。
  窄屏改为 `grid-template-columns: minmax(0,1fr)` 并允许店铺行换行；8 条管理路由在 390px 下
  `scrollWidth` 全部回到 390。修完用例继续走到下一步，**仍缺一份"可审核版本"数据**才可能全绿。
- `e2e-workspace` 不是断言过时也不是产品缺陷：它断言 `session.profile == v2_langchain_mock` 且
  `is_mock == true`，而记录本条时的 dev 栈跑的是在线档（当前档位见[开发指南](development.md#当前环境状态)）。断言保留，
  由批次 3 的"按用例声明前置"把它映射为 UNCONFIGURED（缺离线 mock 栈），不改断言值。
- 因此更正分类：**1 项真实缺陷（已修）+ 13 项缺前置**。

### 3.3 跳过项显形（G2）

门禁结束固定打印三行，顺序不可变：

```
PASSED        <n>
UNCONFIGURED  <m>   （需 --db 或 --e2e）
FAILED        <k>
```

`UNCONFIGURED` 的定义与计数方式：

- **pytest**：解析 `pytest -q` 输出的 `skipped` 计数
- **Go**：集成测试在缺 env 时调用 `t.Skip`，用

  ```bash
  go test -json ./... | jq -r 'select(.Action=="skip") | .Package' | sort -u | wc -l
  ```

  若 `jq` 不可用，退化为解析 `go test -v` 输出中的 `--- SKIP` 行数

**不得并入 PASSED**，也不得硬编码为常数。

### 3.4 `npm ci` 策略

```bash
# 若 2.1 节实测评测显示 npm ci < 8s，则本策略简化为：
[ "$ALL" = 1 ] && npm ci ...
```

否则采用：

- 显式 `--all`
- `frontend/package-lock.json` 的 sha256 与 `.local/npm-lock.sha256` 不同
- `frontend/node_modules` 不存在

任一满足才执行 `npm ci`。

### 3.5 批次 1 验收

```bash
time bash scripts/check.sh        # ≤ 45s，exit 0，末三行含 UNCONFIGURED
bash scripts/check.sh --db        # exit 0，UNCONFIGURED 数量明显下降
bash scripts/check.sh --e2e       # exit 0，14 个浏览器用例逐条 PASS
time bash scripts/check.sh --all  # ≤ 6 min，exit 0
```

**门禁不倒退（硬条款：缺任一条视为批次 1 未完成）**

1. **新旧对照**：改造前的 `scripts/check.sh` 另存为 `.local/check-before.sh`（不进 Git）。
   注意旧脚本**没有参数解析**，`--all` 会被静默忽略并执行全量，因此旧脚本的基线记录为
   "无参数全量跑一次，exit 0，实际执行的检查项清单如下（逐条抄录）"；
   新脚本必须**显式解析** `--db` / `--e2e` / `--all`，且**未知参数必须报错退出**（非 0），
   不得静默忽略——否则分层可能悄悄失效而不被发现。
2. **检查项差集**：把两次 `--all` 实际执行的检查项集合（每条命令的可执行名与参数，
   按执行顺序落到 `.local/gate/<新旧>.log`）做差集：
   - **新增项**逐条列出（命令 + 一行说明它补的是什么）；
   - **消失项必须为空**，即新集合不得少于原集合；出现消失项即视为未完成。
   - 既有项的语义变化（同一命令换了参数、换了范围）算"消失 + 新增"，必须逐条说明。
   - **对比时以"实际执行的检查项"为准，不以命令行参数为准。**

汇报必须包含：四条命令的实测耗时、`UNCONFIGURED` 前后数量、浏览器用例逐一 PASS 列表、
`npm ci` 单步耗时、**新旧 `--all` 对照结果与检查项差集表**。

**差集怎么算（必须可机械执行）**

- 对照清单 `.local/gate/before-checks-canonical.txt` 是**纯条目文件**：一行一项、无表头、无空行；
  序号只写进汇报，不写进文件。
- 新脚本每项打印一行 `[<layer>] <id> <命令摘要>`；差集按 **id** 比对
  （`awk '{print $2}'` 后 `sort`），不按命令行字符串——循环与变量替换会让字符串比较产生假阳性。
- `comm -23` 是消失项（必须为空），`comm -13` 是新增项（逐条写命令 + 一行说明）。
- `id` 一旦发布不得改名；改名按"消失 + 新增"处理，必须逐条说明理由。
- **基线变更必须留 amendment 记录**（`.local/gate/before-checks-amendments.txt` 一行一条：
  日期 + 增减项 + 依据）；无记录的增减视为违反本条款。

**`--all` 的严格语义**

- `--all` 是"发布前全量"：只要有任一 `UNCONFIGURED`，汇总照打，但 **exit 1**，
  并在汇总后追加一行 `STRICT: <n> item(s) could not run; use --allow-unconfigured to accept this`。
- `--allow-unconfigured` 仅在配合 `--all` 时生效，表示本次接受"缺环境"（exit 0）；
  单独用它或配其它层没有额外效果。
- 无参数、`--db`、`--e2e` 语义不变：`UNCONFIGURED` 只计数、不失败，只有 `FAILED > 0` 才 exit 1。
- 理由是两种跑法要回答两个问题：日常开发问"我的改动坏了没有"（缺环境不该红），
  发布前问"这套东西真的都验过了吗"（缺环境必须红）。

---

## 4. 批次 2：覆盖率（P0）

### 4.1 采集命令

```bash
mkdir -p .local/coverage
# Go
go test -coverprofile=.local/coverage/go.out ./...
go tool cover -func=.local/coverage/go.out

# Python
pytest --cov=app --cov-report=term-missing --cov-report=json:.local/coverage/py.json

# 前端
npx vitest run --coverage
```

产物统一放 `.local/coverage/`，**加入 `.gitignore`**，不进仓库。

### 4.2 基线而非绝对阈值

`docs/testing.md` 新增「覆盖率基线」一节，记录本次实测值：

- Go：逐包 statement 覆盖率（表格）
- Python：`app` 各模块覆盖率
- 前端：`src` 整体覆盖率

门禁规则：

```
新覆盖率 ≥ 基线 − 0.5% → 通过
低于基线 − 0.5%         → fail，并打印低于基线的文件清单
基线更新需人工确认后提交
```

不设 80% 之类的绝对阈值，避免制造大面积红色噪声。

### 4.3 批次 2 验收

汇报需含：Go 覆盖率最低的 5 个包、Python 覆盖率最低的 5 个模块、前端整体覆盖率、基线文件内容。

---

## 5. 批次 3：补齐沉默点（P1）

### 5.1 迁移从零建库（G6）

新增 `go/internal/admin/migrations_from_scratch_test.go`，需 `--db` 环境。

**权限已确认**：`ecr_migrator` 具备 `CREATEDB`（`rolcreatedb = t`），用它创建临时库，无需超级用户。

```
1) CREATE DATABASE ecr_v2_scratch_<pid> OWNER ecr_migrator
2) CREATE EXTENSION vector; CREATE EXTENSION pg_trgm;
3) 以 ecr_migrator 按文件名字母序 apply sql/migrations/*.sql
4) 断言 schema_migration 行数 == 迁移文件数，且 checksum 全部 PASS
5) 清理：DROP DATABASE IF EXISTS ecr_v2_scratch_<pid> WITH (FORCE)
   —— 无论成功失败都必须执行，用 defer 保证
```

用途：自动发现"修改历史迁移后必须重建库"这类问题（0034 改列就是靠人工记忆才发现的）。

### 5.2 固定等待禁令（G5）

`check.sh` 增加断言：

```bash
count=$(grep -rho 'waitForTimeout' frontend/tests/*.browser.mjs | wc -l)
[ "$count" -le 10 ] || fail "waitForTimeout 增加：$count（上限 10）"
```

替换固定等待时同步下调上限；替换完成后上限固定为 0。

### 5.3 e2e 前置声明与独立栈

- 每个 `frontend/tests/*.browser.mjs` 在文件头声明所需前置（env 键、端口、目录），
  `check.sh --e2e` 读取声明：缺失 → `UNCONFIGURED` 并打印**缺哪一项**，不进入用例。
- 起独立栈：mock 26081 / 26063 + 独立 Vite 5174 + 独立 accept 库，不得触碰 dev 栈与 `ecr_v2_dev_local`。
- **注记（2026-10-29）**：在此之前，隔离栈的 Vite 未覆盖 `VITE_API_PROXY_TARGET`，默认指向 dev 网关（8080）；
  因此该日期前的 e2e 结果均打在 dev 库上，**不能作为隔离栈的证据**。修复后需重新采集全部 e2e 结论。

- **e2e 前置台账**（缺任何一项 → 该用例 `UNCONFIGURED`，不进入用例）：

| e2e 用例 | 需要的前置 | 缺失时的行为 | 当前状态 |
| --- | --- | --- | --- |
| `e2e-import-wizard` | 独立栈（gateway+ingest）、Vite、栈内 `browser.env`（DSN + `LOGIN_PASSWORD`）、`tests/fixtures/import-batch-20` | `UNCONFIGURED (… to unlock)` | **PASS**（5/5 检查） |
| `e2e-review-suggestion` | 同上 + 该库内可生成建议的待审版本（用例自己 seed `l2-seed-*`） | `UNCONFIGURED` | FAILED-缺 seed（`l2-seed-*` 未渲染，来源可能命中 `synthetic-acl`） |
| `e2e-login` | 同上 + `LOGIN_PASSWORD` + 库内存在待改密的本地账号 | `UNCONFIGURED` | FAILED-缺 seed（库里没有 must_change_password 账号） |
| `e2e-v2-lifecycle` | 同上 + 栈就绪判定（`/healthz` 与 `/v1/session` 均 200） | `UNCONFIGURED` | FAILED（首次写入后轮询 `/admin/v1/merchant/ingestion/{id}` 返回 503，待定性） |
| 其余 9 条 | 同上 + 各自的 seed 数据（例：`workspace` 需要 `syn-policy-a` 证据） | `UNCONFIGURED`（有栈但缺 seed 时是 FAILED，属数据缺口） | FAILED-缺 seed |

- **`internal/admin` 的 import batch 集成用例前置 ingest gRPC**：`V2_INTEGRATION_INGEST_ADDR`
  （缺省回落到集成 env 里的 `V2_INGEST_GRPC_ADDR`）。未配置或 connect 失败 → 用例 `SKIP` 并打印
  `ingest endpoint unavailable: <addr>`，不进入用例；该端点必须服务集成库（`test-integration.env` 的 DSN），
  指向 dev 栈的 ingest（50053）会因未知租户被拒（403 `ingest_not_authorized`）。
- 目标：`e2e-v2-lifecycle`、`e2e-login` 至少真跑通；其余用例按声明逐步补齐。

### 5.4 覆盖率实装

`--cov` 从"仅保留参数位"改为真实采集：Go `-coverprofile`、Python `--cov=app`、前端 `vitest --coverage`，
产物落 `.local/coverage/`，按批次 2 的基线规则比对。

### 5.5 db-env-health（环境静默失效的探针）

`--db` 层新增一条只读检查：确认开发库/测试库的 `schema_migration` 行数与 `sql/migrations/*.sql`
数量一致、关键表存在。0 张表的库会让网关在启动时静默失败（`development identity unavailable`），
这类"环境静默失效"必须在门禁里显形，而不是靠人发现。

### 5.6 每日全量

机器侧 `/etc/cron.d/ecr-test-nightly`（不进仓库）：

```
每日 04:10 执行 bash scripts/check.sh --all
日志写 /var/log/ecr-test-nightly.log（0600）
失败不自动修复，只留日志；次日汇报时人工查看
```

---

## 6. 批次 4：故障注入与并发（P2，可延后）

| 场景 | 断言 |
| --- | --- |
| Provider 超时 | 在配置超时内返回 503 而非挂死；`usage_log` 写一行 NULL token + `error_code` |
| DB 断连 | 健康检查转 unhealthy；业务接口返回 503 而非 panic |
| `usage_log` 写入失败 | 业务调用不受影响（代码已实现，缺测试） |
| 并发双写 | 100 并发调用 → `usage_log` 恰好 100 行，无丢失无重复 |
| 并发 batch-approve | 同版本两个请求同时批准 → 一个成功，另一个返回 stale / conflict |

---

## 7. 文档交付：`docs/testing.md`

必须包含六节，缺一视为未完成：

```markdown
1. 分层总览：六层的命令、耗时预算、外部依赖
2. 环境准备：两个 integration env 文件各自归属的用例组、mock 栈端口、独立库命名规则
3. 改动 → 该跑什么（表：改 go/internal/admin → --db；改 frontend/src/admin → 默认层 + --e2e）
4. 覆盖率基线：数值表 + 更新流程
5. 已知不稳定用例：触发条件与规避方式（无则写"暂无"）
6. 新增测试约定：命名、目录、禁止固定等待、集成测试必须可重复运行
```

---

## 8. 执行顺序与提交粒度

| 批次 | 提交前缀 | 停下条件 |
| --- | --- | --- |
| 1 门禁分层 | `test(gate):` | 四条命令 exit 0 + 耗时与 UNCONFIGURED 数据 |
| 2 覆盖率 | `test(coverage):` | 基线文件 + 最低 5 项清单 |
| 3 沉默点 | `test(gate):` / `test(migrations):` | 迁移从零测试 PASS + 等待计数断言生效 |
| 4 故障注入 | `test(faults):` | 五个场景各有对应用例 |
| 文档 | `docs(testing):` | 六节齐全 |

每批次单独提交，不 squash；**不 push、不 merge**，等确认。

---

## 9. 红线

1. 不改任何现有测试的断言逻辑；不删除现有测试（确需删除须单独提出并说明理由）
2. 不降低门禁强度：快速层现有的检查项一个都不能移除
3. 浏览器层必须使用独立栈与独立库，不得触碰 dev 的 8080 / 5173 与 `ecr_v2_dev_local`
4. 真库层不得使用"按字母序取第一个 env"的做法，必须按第 3.2 节的用例分组映射
5. 覆盖率产物、测试报告、临时库一律不进 Git
6. 本方案不改业务代码；若发现必须改业务代码才能测试，先停下汇报

---

## 10. 执行顺序的原则

**先让"该跑的测试真的跑起来"，再谈覆盖率，最后才谈故障注入。**

覆盖率再漂亮，如果浏览器测试仍靠人记得才跑，体系依旧只是纸面达标。顺序颠倒会做大量无用功。

---

## 11. 可直接转发的执行提示词

```text
读取本文件（测试体系重构方案 v2），按批次 1 开始执行，不要一次性做完所有批次。

开工前先做两件事并写进汇报：
1) 执行第 0 节的前置检查（git status 干净、check.sh 无并行改动）；
2) 实测 time npm ci --prefix frontend，作为 3.4 节策略是否简化的依据。

批次 1 只做门禁分层：
- scripts/check.sh 支持 --db / --e2e / --all / --cov（--cov 批次 1 先留参数位），未知参数 exit 2；
- 快速层 ≤ 45s，--all ≤ 6 min；
- 真库层按 3.2 节的用例分组映射两个 env 文件，禁止"按字母序取第一个"；
- 浏览器层用独立栈（26081/26063）+ 独立 Vite（5174）+ 独立 accept 库，不碰 dev；
- 门禁末三行固定打印 PASSED / UNCONFIGURED / FAILED，Go 侧跳过数用 go test -json 统计，
  不得硬编码。

批次 1 完成后按 3.5 节验收并汇报，等我确认再进批次 2。
红线按第 9 节执行：不改断言、不删测试、不降门禁强度、不 push 不 merge。
```
