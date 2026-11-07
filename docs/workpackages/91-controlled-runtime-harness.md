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

## 不要做

- 不编辑 `go/internal/gateway/wire.go` 放行 selfhost mock；
- 不修改隔离栈脚本（WP92 所有）；
- 不改 smoke 基线、不改 contracts 棘轮、不新增 SECURITY DEFINER；
- 不把环境失败写成 PASS；缺 Docker/镜像/凭据时记 UNCONFIGURED；
- 不提交 `scripts/review/`，不用 `git add .`。
