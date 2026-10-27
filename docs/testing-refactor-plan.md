# 测试体系重构方案（交付 DSH 执行）

- 版本：**v2**（2026-10-27 修订）
- 仓库：`/data/app/EcommerceRAG2`
- 分支：`m8-tenant-providers` @ `2055561`（领先 main 9 个提交，工作区干净；main == origin/main == `4be2611`）
- 执行方式：按批次推进，每批次单独提交、单独跑门禁，**停下汇报等确认**

### 修订记录

| 版本 | 变更 |
| --- | --- |
| v1 | 初版：现状基线 + 四批次 |
| v2 | ① 去掉悬空引用 W1/W2/M9，改为可自行判定的前置条件；② 真库层改为按用例分组指定 env 文件（原"字母序取第一个"会永远选错）；③ 定义 `UNCONFIGURED` 中 Go 部分的计数方法；④ 补 `--cov` 参数与 `--all` 耗时预算；⑤ 前端用例数以运行时为准（37）；⑥ 迁移从零补建库权限与清理方式 |

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

汇报必须包含：四条命令的实测耗时、`UNCONFIGURED` 前后数量、浏览器用例逐一 PASS 列表、`npm ci` 单步耗时。

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

### 5.3 每日全量

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
