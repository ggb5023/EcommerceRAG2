# 94 受控 runtime 与 login 收尾

- 状态：见 INDEX.md
- 依据：WP91 残留（login 未验证、受控 e2e 抖动）；用户 2026-11-08 要求解决残留
- 前置：WP91 已集成（`969d8f9`）

## 目标

让 local identity 的 `login` 在专用数据库上可重复通过；修掉受控栈的 Keycloak 泄漏与浏览器网络
瞬态；提供稳定的 `--controlled-only` 验收，不停止 dev 栈、不改断言。

## 范围

1. 主 agent 预建 `ecr_v2_test_login` 与 0600 `/tmp/ecr-login/{migration,login}.env`；
   local stack 用 `--db` 指向它，不再与 dev/test 共用 gateway ownership lock。
2. 隔离栈脚本：启动前按 `current_database()` 预检 advisory lock 742019,1，
   被占用时给出明确错误；login seed 每次 `reset-password`；`browser.env` 写 `LOGIN_NEW_PASSWORD`。
3. `scripts/dev/customer_runtime.py`：login 前校验 health/local mode、传播新密码、调用 reset；
   文件缺失 exit 2，栈坏 exit 1。
4. `frontend/tests/login.browser.mjs`：`/chat`→`/assistant`，匿名文案改“请登录 AI 查询台”，
   登录后断言 `退出登录`/session；保留 7 个检查。
5. 受控栈销毁：restore/down 加 `--profile oidc`；新增 harness-only sweep（仅 `ecr-wp91` /
   `ecr-restore-wp91-*`），成功后才删 runtime；`ecr-merchant` 只告警。
6. 新增共享 transport retry（只重试网络类错误，3 次），应用到 customer-control/keycloak/
   service/source/selfhost-restore；POST 幂等键在 retry 外生成并复用。
7. `scripts/check.sh` 增加 `--controlled-only`（fast+5 受控，跳过 d2 与普通 spec）与 `--skip-login`；
   login 分支委托 `customer-runtime.sh run-spec login`。

## 验收

- login 在专用 DB 上连续两次 7/7 PASS。
- `bash scripts/check.sh --controlled-only --controlled-e2e` 连续两次 49/0/0。
- `bash scripts/check.sh --e2e`（隔离 + login）56/6/0。
- 全量 `--e2e --controlled-e2e` 目标 61/1/0（v2-restart 仍 UNCONFIGURED）；若 Docker 网络仍抖动，
  记录两阶段矩阵并说明，不谎称单次全绿。

## 不要做

- 不停止 dev 栈来换取 login；不改共享 test DB 的默认值；
- 不删除 `ecr-merchant` 或非 harness 容器/网络；
- 不重试断言、HTTP 4xx/5xx、409 幂等冲突；
- 不改 smoke 基线/棘轮、不新增迁移/SECURITY DEFINER；
- 不提交本地未跟踪的审查目录，不用 `git add .`。

## 实现（2026-11-08）

1. `isolated-stack.sh`：新增 `check_target_database`，在 `stop_stack` 之后、端口检查之前按
   `current_database()` 过滤 `pg_locks` 的 `742019,1`：被占用 exit 1 且不回显 DSN；无法连接/读取
   exit 2（UNCONFIGURED）。login seed 改为 `create`（已存在则跳过）+ 每次 `reset-password`，
   `browser.env` 写 `LOGIN_NEW_PASSWORD`。
2. `customer_runtime.py`：`local_login_environment` 校验 ports/browser.env/runtime.env、health 与
   `mode=local`，传播 `LOGIN_NEW_PASSWORD`；`reset_local_login` 用 stdin 传密码调用
   `go -C go run ./cmd/local-account reset-password`；缺文件 exit 2，栈坏/健康检查失败 exit 1。
   gateway 按源 IP 限流 10 次/分钟而单轮 login 用例会发 8 次登录请求，故 `run-spec login` 记录上一轮
   结束时间，距上次不足 65s 时先打印 `WAIT` 并等待，使同一栈上连续两轮都可执行。
3. `login.browser.mjs`：`/chat`→`/assistant`，匿名文案「请登录 AI 查询台」，登录后断言「退出登录」与
   `/v1/auth/session`（authenticated=true、mode=local）；保持 7 个检查。
4. teardown：`restore` 与 `down` 的 Compose down 加 `--profile oidc`；新增 harness-only sweep
   （仅 `ecr-wp91` / `ecr-restore-wp91-*`），`ecr-merchant` 只打印 WARN；teardown 失败保留 runtime
   目录，成功后才删除。
5. `frontend/tests/transport-retry.mjs`：只重试 `ERR_NETWORK_CHANGED`、`net::ERR_*`、`Failed to fetch`、
   `ECONNRESET`、`ETIMEDOUT`，3 次、500/1000ms；不改 HTTP 状态与断言。应用到 customer-control/
   keycloak/customer-service/customer-source/selfhost-restore，POST 幂等键在 retry 外生成并复用。
6. `check.sh`：新增 `--controlled-only`（快速层 + 受控 5 项，跳过 d2 与普通 spec）与 `--skip-login`；
   login 分支改为用 `run_optional` 委托 `customer-runtime.sh` 的 `run-spec login`（不再内联账号处理）。
7. `docs/development.md` 更新；新增 `tests/test_customer_runtime.py`（teardown profile、sweep
   allowlist、login env/reset/限流窗口、密码不外泄）。

## 本轮验证（worktree `/tmp/ecr-wp94`）

栈（dev 栈未触碰）：先停掉旧 local 栈，再按 `docs/development.md` 的 local 栈命令（`--identity local`、
`--ports 26091`、`--db` 指向 `/tmp/ecr-login/login.env`）启动
→ `PASS target database ecr_v2_test_login has no owning gateway`、
`PASS login account stack-login already present`、`PASS reset login account stack-login ...`。

- login 连续两次（`customer-runtime.sh` 的 `run-spec login`）：第一轮 7 个检查全 PASS（exit 0）；
  紧接着第二轮先打印 `WAIT 61s for the local login rate-limit window ...`，再 7 个检查全 PASS（exit 0）。
- advisory lock 预检：目标库被运行中的栈占用时换 root 启动 →
  `FAIL target database ecr_v2_test_login is already owned by another gateway (advisory lock 742019,1); ...`，
  exit 1；指向不可连接库 → `UNCONFIGURED cannot inspect ...`，exit 2，输出不含 DSN。
- `bash -n`、`shellcheck`、`node --check`（7 个 spec/helper）、`ruff`（新改动无新增；文件内 2 个既有
  BLE001 未变）、`python -m pytest tests/test_customer_runtime.py -q` → 11 passed。

## 主线验收（2026-11-08，主 agent）

- 环境：dev 栈保持运行；隔离栈 26081（`--identity development`、mock）与 local login 栈
  26091（`--db /tmp/ecr-login/login.env`）分别启动；受控 Compose 使用 harness 项目。
- `bash scripts/check.sh --controlled-only --controlled-e2e` 连续两次：
  **PASSED 49 / UNCONFIGURED 0 / FAILED 0**（5 个受控 spec 全绿，含 selfhost-restore；
  425s / 391s）。
- `bash scripts/check.sh --e2e`：**PASSED 56 / UNCONFIGURED 6 / FAILED 0**；6 项
  UNCONFIGURED 全是受控 runtime 前置缺失（预期），`e2e-login` 通过。
- 全量 `bash scripts/check.sh --e2e --controlled-e2e` 第一次 **60/1/1**：仅
  `e2e-v2-lifecycle` 在“actual published policy was not recalled”处失败；同一 spec
  立即定向重跑 **13/13 PASS**，故记录两阶段矩阵；第二次全量 **PASSED 61 /
  UNCONFIGURED 1 / FAILED 0**（`e2e-v2-restart` 仍缺 `V2_RESTART_RUNTIME_ENV`）。
  未把第一次失败改写成通过，也未改断言/基线。
- `python/.venv/bin/python -m pytest tests/test_customer_runtime.py -q` → 11 passed。
- 收尾：两个隔离栈停止；`customer-runtime.sh down --clean` 删除 `ecr-wp91` 卷与网络；
  `ecr-merchant` 按约束只 WARN、未触碰。

