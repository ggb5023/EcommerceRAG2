# 工程测评与重构说明（给 dsh 阅读）

版本：2026-10-27b
适用仓库：`/data/app/EcommerceRAG2`
当前基线：`main == origin/main == 4be2611`；进行中分支 `m8-tenant-providers`（HEAD `2055561`，领先 main 9 个提交，工作区干净）

## 0.0 权威归属（四份文档谁说了算）

| 文档 | 角色 | 冲突时 |
| --- | --- | --- |
| 本文件 | 总纲：问题清单、工作包划分、执行顺序、红线 | **以本文件的顺序与红线为准** |
| `docs/backend-refactor-plan.md` | W1/W2 的执行细则（Go/Python 拆分怎么做） | 细则以后端文档为准，范围以本文件为准 |
| `docs/testing-refactor-plan.md` | 门禁分层、覆盖率、故障注入 | 门禁相关以测试文档为准 |
| `docs/frontend-refactor-plan.md` | 前端设计系统与两端分离 | 前端相关以前端文档为准 |
| `docs/data-and-migrations-plan.md` | 数据归属、迁移纪律、授权检查、恢复演练 | 表归属与迁移以本文档为准 |
| `docs/ops-and-release-plan.md` | 部署编排、版本化发布、升级回滚、部署冒烟 | 发布与回滚以本文档为准 |
| `docs/security-and-secrets-plan.md` | 授权判定收口、密钥清单与轮换、个人信息保留 | 安全边界以本文档为准 |
| `docs/observability-plan.md` | 请求关联 ID、最小指标集、错误码表、日志规范 | 可观测性以本文档为准 |
| `docs/eval-system-plan.md` | 评测可信度：三态门禁、provenance、报告 schema | 评测门禁语义以本文档为准 |
| `docs/governance-plan.md` | 分支治理、里程碑 tag、决策编号、依赖扫描、文档防漂移 | 流程规则以本文档为准 |

同一件事只允许出现在一个文档里；本文件不再重复 W1/W2 的具体拆分步骤，
新增的六份文档各自编号（D/O/S/M/E/G 前缀），不得与本文件的 W0–W6 混用。

**开工前先确认九份文档都在**（缺任何一份就停下汇报，不要自行猜测内容）：

```bash
cd /data/app/EcommerceRAG2
for f in docs/refactor-assessment.md docs/backend-refactor-plan.md docs/testing-refactor-plan.md \
         "docs/frontend-refactor-plan.md" docs/data-and-migrations-plan.md docs/ops-and-release-plan.md \
         docs/security-and-secrets-plan.md docs/observability-plan.md docs/eval-system-plan.md \
         docs/governance-plan.md; do
  [ -f "$f" ] && echo "OK   $f" || echo "MISS $f"
done
```


---

## 0. 这份文档怎么用

1. **先读第 1–3 节**（测评结论与问题清单），确认你同意判定，再动手。
2. 工作包 W1–W6 **互相独立**，按第 6 节顺序执行；每完成一个就停下来汇报，不要连做。
3. 每个工作包都有"验收命令"，**必须以命令输出作为证据**，不接受"我认为改好了"。
4. 第 8 节是红线，违反任何一条就回退重做。
5. 本文档只描述"怎么重构"，不改变任何产品范围；新增功能需求一律回到 ROADMAP。

---

## 1. 测评结论

| 维度 | 评分 | 依据 |
| --- | --- | --- |
| 分层与依赖方向 | A− | 无反向依赖：`localauth` 不引 `gateway`；Python 不引 `eval`；`eval → app` 单向 |
| 不变量遵守 | **B** | 单写者已被突破 2 处（`app_user`、`admin_audit`），无决策记录 |
| 可维护性 | **B−** | `gateway/main.go` ≈1.7k 行装配与业务混合；`admin` 包 6.3k 行但已按职责拆分 |
| 测试厚度 | A− | 67 个测试文件、14 条浏览器用例、`check.sh` 全链路 |
| 治理 | B+ | 21 份决策记录（0006 已被 0021 取代并正确标注）；**12 条分支**（7 条已合并未删、2 条停滞、3 条活跃） |

**一句话结论**：底子好，问题集中在"红线被悄悄放宽"与"单体文件持续增长"，最佳解法是**收口 + 立规矩**，不是重写。

---

## 2. 现状基线（执行这些命令应得到相同量级）

```bash
cd /data/app/EcommerceRAG2
git rev-parse --short HEAD main origin/main
git rev-list --count main..HEAD           # 当前 9（m8 未合并）
git branch | wc -l                       # 期望 ≤ 3（当前 11）
ls sql/migrations/*.sql | wc -l          # 当前 35
ls docs/decisions/*.md | wc -l           # 当前 21
wc -l go/internal/gateway/main.go        # 当前 1706，目标 ≤ 500（需先拆 Main()，它本身 247 行：L146-L392）
find eval -name '*.py' -exec cat {} + | wc -l    # 当前 ≈12.7k
find python/app -name '*.py' -exec cat {} + | wc -l  # 当前 ≈10.4k
time bash scripts/check.sh               # 2026-10-27 实测 31.1s，作为"快速层"预算基准
```

已知体量集中点：

| 文件/包 | 行数 | 性质 |
| --- | --- | --- |
| `go/internal/admin`（整包） | ≈6330 | 已按职责拆分（imports/manifest/review/version/staff/usage/shop_settings），**这是正面样板** |
| `go/internal/gateway/main.go` | ≈1706 | 装配 + 路由 + turn 执行 + SSE 混在一起 |
| `python/app/providers/aliyun_bailian.py` | 1118 | 单 provider 实现过大 |
| `python/app/graph/workflow.py` | 945 | 编排与单步能力混杂 |
| `python/app/ingest/pipeline.py` | 935 | 同上 |
| `eval`（整体） | 12769 | 超过应用本体；4 个 runner 各自加载/校验/写报告 |

---

## 3. 问题清单（按优先级）

### R0 0021 落地后的文档漂移（必修，最轻）

**证据**：0021 已把"货币预算账本"改为"token 用量记录"，代码侧已移除 budget 配置（现役脚本是
`scripts/dev/provider-usage.py`，`budget.py` / `pricing.json` 均已删除），但下列文档仍在描述预算/账本：
`docs/install.md`、`docs/development.md`、`docs/architecture.md`、`docs/backup.md`、`docs/refactor-input.md`、
`AGENTS.md`、`ROADMAP.md`（具体份数以 R0 验收命令的实测输出为准）。
（`docs/decisions/0006`、`0019`、`0021` 中的提及属历史记录，**不改**；`docs/*-plan.md` 与
`docs/refactor-assessment.md` 因在本轮讨论这些废弃键，纳入漂移检查白名单。）

**判定**：口径不一致，会让商家按旧文档去配 `V2_ONLINE_BUDGET_*`。

**修法**：一个 `docs:` 提交，把上述文件里的"预算账本 / V2_ONLINE_BUDGET / provider-budget"改为
"用量记录见 `usage_log`（0021）"；命令一律写成 `scripts/dev/provider-usage.py`；`docs/decisions/` 不动。

**顺带修正 `docs/refactor-input.md` 的两条错误陈述**（它已被跟踪，是本轮重构的输入清单）：

- 第 1 条"遗留表 `window` / `attempt` 仍在库中"→ 实测 dev 库全 schema 无此二表，迁移里也从未创建；
  改为"仅历史命名残留，库中不存在，无需迁移决策"。
- 第 5 条"`docs/development.md` 已不再描述账本"→ 实测仍有 1 处；改为
  "`scripts/check.sh` 已移除 provider-budget 路径，现役脚本为 `scripts/dev/provider-usage.py`；
  文档侧残留以总纲 R0 的实测输出为准"。

**验收**

```bash
# 1) 指令性语句必须归零（重构文档自身讨论这些键，排除在白名单外）
grep -rn 'V2_ONLINE_BUDGET\|provider-budget' docs/ AGENTS.md ROADMAP.md --include='*.md' \
  | grep -v 'docs/decisions/' \
  | grep -vE 'docs/(governance-plan|refactor-assessment|refactor-input|[a-z-]+-plan)\.md' | wc -l   # 期望 0
# 2) 允许保留历史提及，但每行必须带 0021 的链接
grep -rn '预算账本' docs/ AGENTS.md ROADMAP.md --include='*.md' | grep -v 'docs/decisions/'   # 人工核对
```

### R1 单写者不变量已破（必修）

**证据**（生产代码，非测试）：

- `app_user`：`internal/admin/staff.go`（员工创建）与 `internal/localauth/store.go:468`（CLI 建号）都 INSERT
- `admin_audit`：`internal/admin/*.go` 与 `internal/localauth/*.go` 都 INSERT

其余 `tenant`/`shop` 的 INSERT 全部出现在 `_test.go`，属测试夹具，不计入违规。

**判定**：0004 的红线"一张表只有一个写入方"事实上失效，且无决策记录说明例外。

**修法（二选一，推荐 A）**

- A：把 `app_user` 与 `admin_audit` 的写入统一到 `internal/admin`，`localauth` 通过 admin 暴露的 store 方法写入，`localauth` 只碰 `local_*` 表
- B：写 `docs/decisions/0022-account-write-paths.md`，显式声明"账号与审计允许两条写入路径"，并加测试钉住"没有第三个写入方"

**验收**

```bash
grep -rn "INSERT INTO app_user" go/internal --include='*.go' | grep -v _test.go   # 期望只剩 1 个包
grep -rn "INSERT INTO admin_audit" go/internal --include='*.go' | grep -v _test.go # 期望只剩 1 个包
go test ./internal/admin/... ./internal/localauth/... -tags integration            # 全绿
```

### R2 `gateway/main.go` 单体（必修，但可分阶段）

**证据**：1706 行内同时包含进程装配、edge 挂载、路由注册、turn 执行、SSE、恢复逻辑；同包的 `fact_resource.go`、`recovery.go`、`authorization.go` 已证明拆分可行。

**规矩（立即生效）**：新代码不许再进 `main.go`；新 handler 一律新建 `gateway/<主题>.go`。

**工作包**：拆成 `wire.go`（依赖注入与启动）、`routes_*.go`（按 API 前缀）、`turn_*.go`（执行与恢复）、`sse.go`。

**验收**

```bash
wc -l go/internal/gateway/main.go          # ≤ 500（拆出 wire/routes/lifecycle 后；只留 main 与装配）
go build ./... && go test ./internal/gateway/...
bash scripts/check.sh                      # 对外行为零变化
```

### R3 provider 密钥路径未定义（必修，文档级）

**证据**：`internal/admin/provider_crypto.go` 已实现 AES-GCM（密文带 `v1:` 前缀、只存 `last4`、测试断言密文不含明文）；但 0019 只写了表结构，未定义：

1. 主密钥来源（env / 文件 / KMS）与轮换流程
2. 明文如何到达 Python（Go 解密后经 gRPC 传？还是 Python 自己解密？）
3. 日志与错误信息中如何保证不出现明文

**修法**：写 `docs/decisions/0023-provider-secret-path.md`，把上面三条写成硬规则，并在代码中加断言（错误信息不得回显密钥）。

**验收**

```bash
go test ./internal/admin -run ProviderCrypto -v     # 密文泄露/损坏密文用例通过
grep -rn "DecryptProviderKey" --include='*.go' .    # 调用点可枚举，且都在 Go 侧
```

### R4 eval 膨胀（可分批）

**证据**：12769 行，4 个 runner（batch / retrieval / lifecycle / quality-e2e）各自实现加载、校验、报告；新增用例需要复制。

**修法**：分三层

```
eval/runners/   ← 只做参数解析与编排，≤300 行/文件
eval/lib/       ← 数据集加载、证据绑定、报告写入、gate 判定（唯一实现）
eval/cases/     ← 数据（cases/corpus/contract 的 JSON）
```

**验收**

```bash
find eval/runners -name '*.py' -exec wc -l {} + | sort -rn | head -3   # 每文件 ≤ 300
ECR_REQUIRE_EVAL=1 bash scripts/check.sh                              # 全绿
```

### R5 分支与推送纪律（立即）

**证据**：**12 条分支**（m2–m7 已合并但未删、`eval-tooling` 与 `oss-backup` 停在旧提交、`docs/refinement` 为文档分支）；m8 领先 main **9 个提交**未合并。

**修法**

- 合并后 24h 内：`git push origin main` + 删除已合并分支；需要留档就打 tag（`milestone/m6`）
- 分支数上限 3：`main` + 当前里程碑 + 紧急修复

**验收**

```bash
git branch | wc -l          # ≤ 3
git rev-parse --short HEAD origin/main main   # 三者关系明确
```

---

## 4. 重构范围

**做**

- 0021 文档漂移清理（W0）
- 数据归属收口（W1，细则见 `docs/backend-refactor-plan.md` P-B）
- gateway 拆分（W2，细则见 `docs/backend-refactor-plan.md` P-A）
- provider 密钥路径决策（W3）
- eval 分层（W4）
- 跨服务旅程用例（W5）
- 分支与推送纪律（W6）

**不做**

- 不换语言、不拆仓库（Go gateway + Python ai 的分工正确）
- 不动 `internal/customer`（匿名会话与员工会话鉴权模型不同）
- 不把 eval 搬进 `python/app`（依赖方向 eval→app 是对的）
- 不重写迁移系统（35 条只增不改运行良好）
- 不为"好看"改 UI 结构

---

## 5. 工作包

### W0 0021 文档漂移清理（R0）

- 产出：一个 `docs:` 提交，改 7 个文件的预算口径
- 完成定义：第 3 节 R0 的验收命令输出为 0；`docs/decisions/` 零改动

### W1 数据归属收口（R1）

- 产出：`docs/data-ownership.md`（每张表 + 唯一写入方 + 依据迁移）
- 代码：按 A 方案收口 `app_user` / `admin_audit`
- 测试：新增一条测试，扫描 `internal/**` 的 SQL，断言 `app_user`/`admin_audit` 的写入方唯一
- 完成定义：文档、代码、测试三者一致；`grep` 验收命令输出只剩一个包

### W2 gateway 拆分（R2）

- 拆分顺序：`wire.go` → `sse.go` → `turn_*.go` → `routes_*.go`
- 每一步单独提交，提交信息用 `refactor(gateway): ...`，不改行为
- 完成定义：`main.go` ≤ 500 行（拆出 `wire.go`/`routes.go`/`lifecycle.go` 各 ≤80）；`go test ./...` 与 14 条浏览器用例全过

### W3 provider 密钥路径（R3）

- 产出：决策记录 0023 + 代码断言（错误信息不含明文）
- 完成定义：密钥生命周期四问（生成、存储、解密、轮换）在决策里都有答案

### W4 eval 分层（R4）

- 先抽 `eval/lib/`（加载 / 校验 / 报告），再逐个把 runner 改薄
- 每个 runner 迁移后单独提交，并跑 `ECR_REQUIRE_EVAL=1 bash scripts/check.sh`
- 完成定义：runner ≤ 300 行；新增一个用例的 diff ≤ 50 行

### W5 跨服务旅程用例（新增，优先于 W4 也可）

一条浏览器用例，覆盖：

```
全新安装 → /setup → 登录 → 新建员工 → 员工首登改密
→ 导入资料 → 审核 → 发布 → 顾客提问（带引用）
→ 撤回 → 顾客得到"资料未覆盖" → 转人工 → 客服接管
→ 回滚 → 顾客恢复旧答案
```

- 报告格式与 `v2-lifecycle.browser.mjs` 一致（`.local/<用例>/browser/<uuid>/report.json`，0600）
- 完成定义：一条命令跑完，失败时能指出具体步骤

### W6 分支与推送纪律（R5）

- 推送 main、合并 m8（若已验收）、删除已合并分支、打 milestone tag
- 完成定义：`git branch` ≤ 3，`origin/main == main`

### T1 测试门禁分层（引用型工作包）

- 细则、批次划分与验收命令见 `docs/testing-refactor-plan.md` 第 1、2 节
- 前置：W1 完成之后执行；不要与 W2 / M9-1 并行（三者都改 `scripts/check.sh` 或 `frontend/`）
- 完成定义：以该文档第 1 节的 G1–G5 全部达标为准

### M9-1 前端设计系统（引用型工作包）

- 细则与验收见 `docs/frontend-refactor-plan.md` 第 9 节「阶段 1」
- 前置：m8 合并完成、0024 决策已写、bundle 基线已测
- 完成定义：以该文档阶段 1 的验收清单为准（tokens + 组件 + `data-testid`，不动业务逻辑）

---

## 6. 执行顺序与停点

四份文档共用同一条时间线，但**只要不在同一批文件上并行**即可；工作包之间仍互相独立，可按下面的顺序单点执行。

```
W0（半天）   0021 文档漂移清理
W6（10 分钟）分支收敛 + 推送 main（m8 合并前先确认 0021 已落地）
W1（半天）   单写者收口 + docs/data-ownership.md
── 下面开始动代码结构，先门禁后结构 ──
T1（1 天）   测试批次 1：门禁分层（--db/--e2e/--all + UNCONFIGURED）
W2（1–2 天） gateway/turn 拆分（后端方案 P-A）
W3（2 小时） provider 密钥生命周期决策（0023）
W5（1 天）   跨服务旅程用例
W4（1–2 天） eval 分层
M9-1（1 天） 前端设计系统（tokens + 6 组件 + data-testid，不动业务）
M9-2+       前端页面迁移 / 顾客端移动版 / eval 收尾
```

每完成一个工作包**停下来汇报**，包含：改动文件、验收命令的真实输出、未完成项。不要连续做两个。

**文件争用提示**：T1 改 `scripts/check.sh`；W2 改 `go/internal/gateway` + `go/internal/admin`；
M9-1 改 `frontend/src` 与 `frontend/tests/*.browser.mjs`。三者不同时进行。

## 6.1 决策编号（唯一分配）

| 编号 | 主题 | 谁写 |
| --- | --- | --- |
| 0021 | 用 token 用量替代货币账本 | 已存在（M8） |
| 0022 | 数据归属与账号写入路径 | W1 产出 |
| 0023 | provider 密钥生命周期 | W3 产出 |
| 0024 | 前端设计系统与两端分离 | M9 阶段 1 之前产出 |

**0021 已被占用**，M9 文档里原写的 `0021-frontend-design-system.md` 一律改为 `0024`。新增决策前先 `ls docs/decisions/` 确认下一个编号（见 AGENTS.md 的流程）。

---

## 7. 全局验收矩阵

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 构建与单测 | `go build ./... && go test ./...` | 全绿 |
| 数据库集成 | `V2_INTEGRATION_ENV=<env> go test -tags integration ./...` | 全绿 |
| 全量检查 | `bash scripts/check.sh` | `PASS foundation checks` |
| 评测模式 | `ECR_REQUIRE_EVAL=1 bash scripts/check.sh` | 全绿（缺语料即失败） |
| 浏览器 | `cd frontend && node tests/v2-lifecycle.browser.mjs` | 13/13 |
| 登录 | `cd frontend && node tests/login.browser.mjs` | 7/7 |
| 旅程 | `cd frontend && node tests/journey.browser.mjs` | 全过（W5 交付） |

---

## 8. 红线（违反即回退）

1. 不改 `sql/migrations/` 已有文件；结构变化只能新增迁移
2. 不改 `proto/rag/v1/rag.proto` 的语义；确需改动先改 proto 再生成并跑 `check-proto.sh`
3. 不降低任何校验门槛（评测门槛、密码策略、限流阈值、CSRF、单写者）
4. 不修改冻结评测集、真值、gate 判定标准
5. 不为让检查通过而改 `check.sh` 的判断逻辑（可以在"新增检查"方向加强）
6. 不在重构提交里夹带功能变更；一个提交只做一件事
7. 守卫脚本拦截时，只能新增提交修正，不能用工作区临时改动骗过钩子

---

## 9. 汇报模板

```text
完成内容：
改动文件：
验收命令与真实输出：
未完成/降级项及原因：
下一步建议：
```

数字必须来自命令输出，不允许估算或引用旧结果。

---

## 10. 后续重构的触发阈值

出现任一情况，重新启动一次"收敛周"：

| 指标 | 当前 | 阈值 |
| --- | --- | --- |
| `gateway/main.go` 行数 | ≈1706 | > 2200 |
| 单文件 > 800 行的生产文件数 | 3 | ≥ 5 |
| eval runner 重复逻辑份数 | 4 | ≥ 5 |
| 活跃分支数 | 11 | > 4 |
| 同一功能需要改动 ≥ 3 个包 | 偶发 | 连续 2 次 |

---

## 附：本文件不改变任何产品范围

功能增删一律回到 `ROADMAP.md` 与 `docs/decisions/`。本文档只回答一个问题：**在不改变对外行为的前提下，怎样把结构收回到可控范围。**
