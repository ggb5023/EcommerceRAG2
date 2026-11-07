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
