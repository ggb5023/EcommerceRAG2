# 后端重构细则（供 dsh 执行）

版本：**v3（现行）**　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`

> **本文件取代 v1 / v2**：仓库里只保留这一份，v1/v2 的 Python 基线（731 / 663 / 550 / 378）是错误统计，已作废。
>
> **权威范围**：本文件是 `refactor-assessment.md`（工程测评与重构说明）里 **W1（数据归属）**
> 与 **W2（gateway 拆分）** 的执行细则，外加 **P-C（Python 长函数）** 与 **P-D（机械护栏）**。
> 范围与顺序以总纲为准，本文件不新增工作包。
>
> **v3 相对 v2 的改动**（全部来自实测复核）：
> 1. 基线数字改为 **AST 实测口径**，并附可复现的测量命令（v1 的 Python 数字是错误统计，已作废）
> 2. `gateway/main.go` 目标与总纲对齐为 **≤ 500 行**（v2 写的 300 与 `Main()` 247 行冲突）
> 3. P-A 增加一条提交：拆分 `Main()` 的装配、路由与生命周期
> 4. P-B 增加两条提交：`customer/ai.go:run`(168) 与 `admin/manifest.go:parseManifest`(155) 收到 ≤150
> 5. 测试基线写死具体数字（Go 98 个测试函数；Python 350 passed / 28 skipped）

---

## 0. 一句话目标

**在不改变任何对外行为的前提下**，把后端三处结构性债务切开：turn 引擎、admin 持久化边界、Python 长函数；并用机械检查防止回退。**不是重写，不动分层，不动契约。**

## 1. 硬约束（违反任一条即停止并汇报）

1. **行为零变化**：HTTP 路由、请求/响应字段、错误码、SSE 事件顺序、幂等语义、fencing 语义全部保持。不接受"顺手优化"。
2. **不变量不动**：一张表一个写入方；只新增迁移、不改已有迁移（当前 0035 之后）；`proto/rag/v1/rag.proto` 不改；应用不执行 DDL。
3. **质量口径不变**：`real_service_acceptance=false`、`human_review_completed=false` 的标注一律不动；不因重构修改评测门槛、冻结集、真值。
4. **提交粒度**：一阶段一分支；每个提交可独立编译、可独立回滚；前缀 `refactor:`（纯结构）、`test:`（补测）、`docs:`。
5. **推送与合并**：不 push、不 merge，除非操作者明确授权；合并后 24 小时内推送并删除分支。
6. **重构期间功能冻结**：除阻塞性 bug 外不接新功能，避免与 P-A/P-B/P-C 冲突。
7. **文档先行**：开工前把 `refactor-assessment.md` 从 `.local/`（gitignored）移到 `docs/refactor-assessment.md` 并提交，否则细则引用的"总纲"在 clone 后不可见。

## 2. 基线（重构前先记录，用于对比）

```bash
cd /data/app/EcommerceRAG2
git rev-parse --short HEAD main origin/main      # 期望 main == origin/main == 4be2611
git rev-list --count main..m8-tenant-providers   # 期望 9（先合并再开工）
bash scripts/check.sh                            # 期望 PASS foundation checks
ECR_REQUIRE_EVAL=1 bash scripts/check.sh         # 结论与当前 HEAD 一致即可
(cd python && ./.venv/bin/python -m pytest -q)   # 期望 350 passed, 28 skipped
grep -rhoE '^func Test[A-Za-z0-9_]+' --include='*_test.go' go | wc -l   # 期望 98
```

结构基线（**AST 实测**，2026-10-27）：

| 指标 | 当前值 | 目标值 |
| --- | --- | --- |
| `gateway/main.go` 行数 | ~1706 | **≤ 500**（与总纲一致） |
| 最大 Go 函数 `runTurn`（`gateway/main.go:1058`） | **413** | ≤ 150 |
| 次大 `Main`（`gateway/main.go:146`） | **247** | ≤ 80（拆成 build/routes/lifecycle） |
| `createTurn` / `resumeTurn` | 184 / 155 | ≤ 150（HTTP 适配层 ≤ 40） |
| `customer/ai.go:68 run` | **168** | ≤ 150 |
| `admin/manifest.go:125 parseManifest` | **155** | ≤ 150 |
| gateway 包 SQL 语句 | 71 | 0（进 `internal/turn/store.go`） |
| admin 包 SQL 语句 | 127 | 0（进 `internal/admin/store*.go`） |
| 事务起点在 handler | 7 个文件 | 0 |
| 最大 Python 函数 `provider_generate_node` | **322** | ≤ 150 |
| 次大 `submit` / `_publish_reviewable` / `_inspect_package` / `parse` | 209 / 187 / 154 / 151 | ≤ 150 |
| Go 测试/代码比 | 3967 / 16053（25%） | ≥ 30% |
| 裸 `writeJSON` | 40 处 | 0（统一走 `httpapi`） |

上表 SQL 语句数（gateway 71 / admin 127）的口径，复核时照此复现：

```bash
grep -rhoE 'SELECT |INSERT INTO |UPDATE |DELETE FROM ' go/internal/<pkg>/*.go | wc -l
```

换口径会得到不同数字；本文只认上表这一条命令，其他口径不作为基线。

**测量口径**（复核时照此执行，避免口径不一致）：

```bash
# Go：go/ast 统计 FuncDecl 行跨度，排除 pb/ 与 *_test.go
# Python：ast 统计 FunctionDef/AsyncFunctionDef 的 (end_lineno - lineno + 1)
python3 - <<'PY'
import ast, pathlib
rows=[]
for p in pathlib.Path('python/app').rglob('*.py'):
    for n in ast.walk(ast.parse(p.read_text(encoding='utf-8'))):
        if isinstance(n,(ast.FunctionDef, ast.AsyncFunctionDef)):
            rows.append(((n.end_lineno or n.lineno)-n.lineno+1, f"{p}:{n.lineno}", n.name))
rows.sort(reverse=True)
for s,loc,name in rows[:8]: print(f"{s:4d} 行  {loc:52s} {name}")
PY
```

## 3. 阶段与交付

### P-A　turn 引擎拆分（最高优先级，2–3 天）

**问题**：`runTurn`(413) / `createTurn`(184) / `resumeTurn`(155) 把状态机、幂等、fencing、SSE、恢复全包了；`Main()`(247) 又把装配与业务混在一处。这是本系统唯一"出错就重复执行或错乱对话"的地方。

**目标形态**（新建包 `go/internal/turn`）：

```
internal/turn/
  engine.go      Turn()/Resume() 入口，状态迁移；单函数 ≤ 120 行
  states.go      状态与允许的迁移（表驱动，便于单测穷举）
  store.go       turn / turn_idempotency / sse_event / tool_call_trace /
                 conversation_execution 的全部 SQL 与事务
  stream.go      SSE 写出（只负责推送与断线重连序列）
  fence.go       所有权与 fencing epoch 校验
  engine_test.go 状态迁移穷举（表驱动）
  store_integration_test.go  幂等、并发、租约（-tags integration）

gateway/
  main.go        ≤ 500 行：只留 main() 与装配调用
  wire.go        buildDeps()  ≤ 80 行
  routes.go      registerRoutes() ≤ 80 行
  lifecycle.go   runServer()  ≤ 80 行
```

> **表边界先写死**：`internal/turn` 只拥有 `turn`、`turn_idempotency`、`sse_event`、`tool_call_trace`、
> `conversation_execution` 五张表；**`customer_*` 五张表仍归 `internal/customer`**，两者不得互写。
> 拆分时若发现 `gateway` 里存在 `customer_` 前缀的写入，**先停下汇报**，不要顺手搬。

**提交顺序**（每步单独可编译、可回滚）：

1. `refactor(turn): extract the state machine from the gateway` —— 建包、搬 `runTurn` 主体；**不搬 SQL**
2. `refactor(turn): move turn persistence into the turn package` —— 搬 SQL 与事务
3. `refactor(turn): move SSE streaming into its own file`
4. `refactor(gateway): split wiring into build, routes and lifecycle` —— 拆 `Main()`（v3 新增）
5. `test(turn): cover every allowed state transition` —— 表驱动穷举
6. `test(turn): cover idempotency and fencing under concurrency` —— 集成测试

**验收**：

```bash
cd go && gofmt -l internal/turn internal/gateway && go vet ./... && go test ./...
cd .. && bash scripts/check.sh
cd frontend && node tests/v2-lifecycle.browser.mjs      # 13 步
cd frontend && node tests/v2-restart.browser.mjs        # 6 步（恢复语义）
wc -l go/internal/gateway/main.go                       # ≤ 500
```

**阶段函数验收（[决策 0025](decisions/0025-code-level-rules.md) R2）**：每个拆出的阶段函数行数 **≤80**、
只触碰一类工作（SQL / 模型调用 / 序列化 / 纯判断），触碰两类以上必须再拆；
汇报时给出每个函数的行数与触碰类别统计。

**回滚**：`git revert` 单提交即可——包与调用方在同一提交内切换。

### P-B　admin 持久化边界 + 单写者收口（2 天）

**问题**：`admin` 127 条 SQL 写在 handler 里，事务起点散在 7 个文件；`app_user` 与 `admin_audit` 同时被 `admin` 与 `localauth` 写入（违反 0004 红线）。

**目标形态**：

```
internal/admin/
  store.go            app_user / admin_audit / admin_role_grant / tenant_agent_review
  store_imports.go    import_batch / import_item / source_* / document
  store_review.go     review / version 状态迁移
  handlers 保持不变（imports.go / manifest.go / review.go / version.go），只去掉 SQL
```

**单写者收口**：`localauth` 不再直接写 `app_user`/`admin_audit`，改为调用 `admin.Store` 的最小方法（`CreateAccount`、`AppendAudit`）；`localauth` 只保留 `local_*` 三张表。

**提交顺序**：

1. `refactor(admin): add a store for account and audit writes`
2. `refactor(admin): move import persistence into the store`
3. `refactor(admin): move review and version transitions into the store`
4. `refactor(localauth): write accounts and audit through the admin store`
5. `refactor(admin): split manifest parsing into validate and build` —— `parseManifest` 155 → ≤150（v3 新增）
6. `refactor(customer): split the execution steps out of run` —— `customer/ai.go:run` 168 → ≤150（v3 新增）
7. `test(admin): pin the single writer for app_user and admin_audit` —— 源码扫描 + 集成测试双保险

**验收**：

```bash
grep -rc 'INSERT INTO\|UPDATE \|DELETE FROM' go/internal/admin/*.go go/internal/gateway/*.go   # 期望 0
grep -rn 'INSERT INTO app_user\|INSERT INTO admin_audit' go/internal --include='*.go' | grep -v _test.go   # 只应出现在 admin 包
cd go && go test ./... && cd .. && bash scripts/check.sh
cd frontend && node tests/login.browser.mjs             # 7 步（员工创建 + 首登改密）
```

### P-C　Python 长函数拆分（1.5 天）

**问题**（AST 实测，前五名）：

| 函数 | 行数 | 位置 |
| --- | --- | --- |
| `provider_generate_node` | 322 | `graph/workflow.py:600` |
| `submit` | 209 | `ingest/database.py:160` |
| `_publish_reviewable` | 187 | `ingest/worker.py:489` |
| `_inspect_package` | 154 | `ingest/package_validation.py:40` |
| `parse` | 151 | `ingest/pipeline.py:93` |

> v1 曾把 `_response_format`(11)、`parse_persisted_package`(87)、`_common`(15)、`package_payload_hash`(16) 列为长函数，属统计口径错误，已作废。

**目标形态**

```
python/app/graph/steps/provider_generate.py   ← 从 workflow.py 抽出单步能力（≤150 行/函数）
python/app/ingest/store/submit.py             ← database.submit：校验 / 事务 / 结果装配
python/app/ingest/publish.py                  ← worker._publish_reviewable：状态迁移 / 写库 / 事件
python/app/ingest/validation/inspect.py       ← package_validation._inspect_package：按规则组拆
python/app/ingest/pipeline/parse.py           ← pipeline.parse：解析 / 归一化 / 落库
```

**提交顺序**（依赖最少、纯函数最多优先）：

1. `refactor(graph): extract provider_generate_node into a step module`
2. `refactor(ingest): split submit into validation, transaction and assembly`
3. `refactor(ingest): split publish into transitions, persistence and events`
4. `refactor(ingest): split package inspection by rule groups`
5. `refactor(ingest): split parse into parse, normalize and persist`

**验收**（用例数不减是硬指标：实测基线 **350 passed / 28 skipped**）：

```bash
cd python && uv run pytest -q        # 不得少于 350 passed / 28 skipped
uv run ruff check app
cd .. && bash scripts/check.sh
python3 - <<'PY'
import ast, pathlib
bad=[]
for p in pathlib.Path('python/app').rglob('*.py'):
    for n in ast.walk(ast.parse(p.read_text(encoding='utf-8'))):
        if isinstance(n,(ast.FunctionDef, ast.AsyncFunctionDef)):
            size=(n.end_lineno or n.lineno)-n.lineno+1
            if size>150: bad.append((size,f"{p}:{n.lineno}",n.name))
print(sorted(bad, reverse=True))
raise SystemExit(1 if bad else 0)
PY
```

### P-D　一致性收尾 + 机械护栏（0.5 天）

1. 40 处裸 `writeJSON` 统一为 `httpapi.WriteJSON`
2. 新增 `scripts/check/check-structure.py` 并接入 `check.sh`：

| 规则 | 阈值 |
| --- | --- |
| `go/internal/**`（排除 `pb/`、`*_test.go`）单函数行数 | ≤ 150 |
| `go/internal/gateway/main.go` 行数 | ≤ 500 |
| `python/app/**` 单函数行数 | ≤ 150 |
| `eval/*.py` runner 行数 | ≤ 300 |
| `go/internal/{admin,gateway}` 中 SQL 语句 | 0 |

> 该脚本加入时**必须全绿**。若届时仍有超标项，说明前面阶段漏了函数——补提交而不是加白名单；
> 白名单只允许临时存在一个里程碑，并在下一次提交中清空。

3. `docs/architecture.md` 差距表补一行：后端分层（turn / admin store / providers 拆分已完成）

## 4. 全局验收（重构完成后一次跑完）

```bash
cd /data/app/EcommerceRAG2
bash scripts/check.sh                                   # 必须 PASS foundation checks
ECR_REQUIRE_EVAL=1 bash scripts/check.sh                # 结论与重构前一致
(cd python && uv run pytest -q)                          # ≥ 350 passed / 28 skipped
(cd frontend && npm test && npm run build)
(cd frontend && node tests/v2-lifecycle.browser.mjs)     # 13/13
(cd frontend && node tests/v2-restart.browser.mjs)       # 6/6
(cd frontend && node tests/login.browser.mjs)            # 7/7（local 档）
python3 scripts/check/check-structure.py                 # 通过
```

**结构性验收**：第 2 节表格每个指标达标；`check-structure.py` 通过；`git branch | wc -l` ≤ 3。

## 5. 汇报格式（每阶段结束）

```text
完成内容：
改动文件：
结构性指标（前后对比，含 AST 实测数字）：
验证结果（跑了哪些命令、输出摘要）：
未运行项和限制：
回滚方式：
下一步：
```

## 6. 严禁事项

- 不得为让结构达标而删测试、降断言、放宽门槛
- 不得在重构分支上顺手改行为、改 UI、改文案、改错误码
- 不得把 Go 拆成多服务、不得合并 Go 与 Python、不得把 eval 搬进应用
- 不得使用 `git add .`、不得 `--no-verify`、不得 force push
- 遇到"重构中发现 bug"：**单独开 `fix:` 提交**，不与 `refactor:` 混在一起

## 7. 起手动作

```bash
# 0) 前置：确认总纲与分册已在仓库内且可被 git 跟踪
#    注意 .gitignore 的 /docs/** 会忽略这些文件，必须先有白名单：
#    !/docs/*-plan.md 与 !/docs/refactor-assessment.md
git check-ignore -v docs/refactor-assessment.md docs/backend-refactor-plan.md   # 期望输出为空
ls docs/refactor-assessment.md docs/backend-refactor-plan.md

# 1) 确认起点干净（m8 已合入 main 并推送）
git checkout main && git pull --ff-only && git log --oneline -3

# 2) 每个阶段一条分支，合完即删
git checkout -b refactor-pA-turn

# 3) 记录第 2 节基线（含 AST 实测数字与 pytest 计数）

# 4) 按对应阶段的提交顺序执行，阶段之间回 main 重新开分支
```

阶段之间必须回到 `main` 重新开分支，不要在一个分支上连做 P-A/P-B/P-C。
