# 91 受控运行时 harness（本地 5+1 spec）

- 状态：见 INDEX.md
- 依据：WP90 后候选 1；用户 2026-11-07 要求并行解决三个后序候选
- 前置：WP90 已关（`68685a2`）

## 目标

让 `customer-control`、`customer-service`、`customer-source`、`keycloak`、`selfhost-restore`
与 `login` 在本地可重复运行。受控 selfhost 栈必须使用真实 Bailian provider（`wire.go` 禁止
selfhost mock）；不实现 v2-restart、CI nightly。

## 范围

1. 新增 customer-runtime 的 shell 入口与 Python 编排模块：
   `up|status|run|run-spec|restore|down`；独立 Compose project、fresh volume、0600 runtime；
   gateway 容器内监听 18443 并映射 18443:18443，保证 OIDC issuer 从容器内可达。
2. `scripts/ops/selfhost.py`：额外 `dev-user` 账户、IP origin 的 IP SAN、可配置监听端口；
   使用真实 provider 配置（只读，不复制密钥）。
3. selfhost 备份脚本：`ingest.env`/`worker.env` 不存在时跳过，带单测。
4. `scripts/check.sh` 增加 `--controlled-e2e`：Phase 1 按
   customer-service → customer-source → customer-control → keycloak 顺序；Phase 2 停源栈、
   backup/restore、同 origin 启动后跑 selfhost-restore；exit 2 保持 UNCONFIGURED。
5. `frontend/tests/keycloak.browser.mjs` 支持自签 HTTPS；`customer-control` 的 mock 专属
   “零 assistant 消息”断言改成“handoff/重启前后消息集合不变”，其余断言保留。
6. `login` 用 local identity 栈 + reset-password；由主 agent 在 WP93 集成后运行。

## 验收

- Phase 1：`bash scripts/check.sh --e2e --controlled-e2e` → 60 PASS / 0 UNCONFIGURED / 0 FAIL。
- Phase 2：restore 1 PASS；聚合 61/1/0（v2-restart 仍 UNCONFIGURED）。
- 非受控 `bash scripts/check.sh --e2e` 仍 55/7/0。
- 重复一次 fresh project 得到同样计数；`down --clean` 可清理。

## 结果（2026-11-07，分支 wp91）

实现要点：

- `scripts/dev/customer-runtime.sh` + `scripts/dev/customer_runtime.py`：`up|status|run|run-spec|restore|down`；
  独立 project `ecr-wp91`、fresh volume、0700 runtime、报告写
  `/var/lib/ecommerce-rag/wp91/runs/<run-id>/<spec>`，origin `https://127.0.0.1:18443`。
- gateway 容器内监听 18443 并映射 `18443:18443`；因 discovery 早于监听器，`identity.Config` 新增
  `backchannel_url`（Compose 内 `http://keycloak:8080`），discovery/JWKS/introspection 走内网，
  issuer 与浏览器端点仍是公开 origin。bootstrap 以 root + CHOWN/DAC_OVERRIDE/FOWNER 写 realm 文件。
- 启停顺序固定 postgres → db-init → migrate → bootstrap → keycloak → gateway/ai；一次性容器用
  `up -d` + `docker inspect` 轮询，避免 `up --wait` 与即时退出竞争。
- `scripts/ops/selfhost.py`：`--dev-user`、IP origin 的 IP SAN、`--listen-port`；
  `selfhost-backup.py` 跳过不存在的 `ingest.env`/`worker.env`（单测覆盖）。
- `scripts/check.sh --controlled-e2e`：每次 `up --fresh` 起干净 project/volume；Phase 1 固定
  顺序四 spec；Phase 2 停 writers → backup → restore 到仓库外新 project
  （默认 `/tmp/ecommerce-rag-controlled-restore/restored-*`）→ 同 origin 启动 →
  `selfhost-restore`；exit 2 记 UNCONFIGURED。
- `keycloak.browser.mjs` 支持 `KEYCLOAK_BROWSER_IGNORE_HTTPS=1`；`customer-control` 两处 mock 专属
  零 assistant 断言改成 handoff/重启前后消息集合不变，其余 CSRF/幂等/lease/越权/关闭写断言保留。

本轮命令与结果（逐项 `run-spec`，非 `check.sh` 聚合）：

```bash
bash scripts/dev/customer-runtime.sh up --fresh                    # exit 0，origin health=ok
bash scripts/dev/customer-runtime.sh run-spec customer-service     # exit 0，8 checks PASS
bash scripts/dev/customer-runtime.sh run-spec customer-source      # exit 0，5 checks PASS
bash scripts/dev/customer-runtime.sh run-spec customer-control     # exit 0，9 checks PASS
bash scripts/dev/customer-runtime.sh run-spec keycloak             # exit 0，6 checks PASS
bash scripts/dev/customer-runtime.sh restore                       # exit 0，2 checks PASS
bash scripts/dev/customer-runtime.sh down --clean                  # exit 0，容器/卷/runtime 目录清空
```

报告目录：`/var/lib/ecommerce-rag/wp91/runs/20261107T111848Z-cdc827/customer-service`、
`.../20261107T111930Z-62062a/customer-source`、`.../20261107T112829Z-b139b7/customer-control`、
`.../20261107T113117Z-12c132/keycloak`、`.../20261107T113216Z-d36136/selfhost-restore`。
`customer-service` 的 AI 回答来自真实 Bailian（引用命中已发布版本），非 mock。

定向测试：

```bash
python/.venv/bin/python -m pytest -q tests/test_selfhost_backup.py tests/test_selfhost_provisioning.py  # 8 passed
cd go && go test ./internal/identity/          # ok
bash -n scripts/check.sh && shellcheck scripts/dev/customer-runtime.sh && go vet ./internal/identity/  # 全通过
```

## 未覆盖 / 限制

- 按主 agent 的资源限制，本轮**未运行** `bash scripts/check.sh`、`--db`、`--smoke` 与非受控
  `--e2e`；因此验收里的 60/0/0、聚合 61/1/0 与 55/7/0 需要主 agent 集成后实测。本轮只证明
  五条受控 spec 在同一受控栈上逐条通过。
- `login` 未运行：需要 `isolated-stack.sh --identity local --ports 26291` + reset-password，
  由主 agent 在 WP93 集成后执行。
- 本分支基于 `196e935`；`check-docs` 因 `91-93-parallel.md` 的 `decisions/0041...` 相对链接
  报错（主分支 `e18c186` 已改为 `../decisions/`，不属于本包文件）。
- 产品观察（未改产品代码）：`StaffWorkspace.vue` 挂载时把 `/staff?tab=queue` 归一化并丢掉
  `id`，刷新后不会自动重开已领取会话，spec 改为从队列重新点开（ownership 仍由服务端保持）；
  `/support` 是到 `/staff` 的客户端重定向，静默 SSO 会直接落在 `/staff`，受控登录断言接受两者。
- `customer-service` spec 的三处调整（关来源抽屉、按可访问名定位人工回复框、重开队列会话）
  提交于 `6ffa05a`；`customer-control` 的 SSO 重登录提交于 `9048104`。
- 恢复栈 `compose down` 时网络报 `Resource is still in use`（容器刚删除的瞬态），不影响
  restore spec；恢复卷按设计保留，`down --clean` 统一清理。

## 集成结果（2026-11-07，主 agent）

- 受控 5 spec + restore 在独立运行时通过：`check.sh --e2e --controlled-e2e`（此时隔离栈停止）
  得到 **PASSED 49 / UNCONFIGURED 13 / FAILED 0**（44 fast + 5 controlled；普通 spec 因栈停止
  记 UNCONFIGURED）。
- 与隔离栈同时运行时仍有 Docker 网络变更导致的 Chromium 瞬态失败（`ERR_NETWORK_CHANGED` /
  `Failed to fetch`）；已加 5s settle、Chromium `--ignore-certificate-errors`、导航/API 重试，
  但未再追求单次全量 60/0/0。
- `login` 未验证：local identity 栈在本机未启动成功，仍为 UNCONFIGURED。
- 普通 e2e 单独运行为 53 PASS / 7 UNCONFIGURED / 2 FAILED（v2-authority、v2-lifecycle）；
  两者单独重跑均通过，属共享 test DB/数据态的波动。
- 附加修正：`import_batch_integration_test` 按 WP32 的 409 `precheck_blocked` 语义更新，
  用 clean manifest 测完整生命周期；`--db` 从 51/3/1 变为 52/2/0。

## 不要做

- 不编辑 `go/internal/gateway/wire.go` 放行 selfhost mock；
- 不修改隔离栈脚本（WP92 所有）；
- 不改 smoke 基线、不改 contracts 棘轮、不新增 SECURITY DEFINER；
- 不把环境失败写成 PASS；缺 Docker/镜像/凭据时记 UNCONFIGURED；
- 不提交本地未跟踪的审查目录，不用 `git add .`。

## WP94 收口（2026-11-08）

WP91 的 login 与受控 e2e 遗留项由 [WP94](94-controlled-runtime-login.md) 关闭，主 agent 在
集成后实测：

- login 在专用 `ecr_v2_test_login` 上连续两次 7/7（`customer-runtime.sh run-spec login`）。
- `--controlled-only --controlled-e2e` 连续两次 49/0/0（5 个受控 spec + selfhost-restore）。
- 常规 `--e2e` 56/6/0；全量第一次 60/1/1（仅 `v2-lifecycle` 发布/检索时序抖动，定向重跑
  13/13），第二次 61/1/0（`v2-restart` 仍 UNCONFIGURED）。
- 因此上文“未再追求单次全量 60/0/0”只描述 WP91 当时的状态；聚合目标 61/1/0 已达成。
- 受控 runtime 已知残留（预检 DSN、sweep 作用域、login 标记、`up --fresh` 假 PASS）见
  [WP94 的“已知问题”](94-controlled-runtime-login.md#已知问题2026-11-08-审计待修)。
