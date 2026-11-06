# 单商家安装

[项目入口](../README.md) · [导入知识](knowledge.md) · [客服操作](support.md) · [备份恢复](backup.md)

此部署运行一个商家、一家店铺。客户匿名咨询；员工默认使用 Go 内置账号登录，Keycloak 改为可选 profile。订单、实时价格、库存与配送承诺不在首版能力内。项目仍在开发阶段，尚未经过生产环境验证。

## 准备

Linux、Docker Engine 与 Compose v2、Git、Python 3.12、uv、openssl。建议至少 4 核、8 GiB 内存、20 GiB 可用磁盘。克隆仓库后，在仓库根目录运行 `uv sync --project python --frozen`。应用依赖以锁文件为准。Docker 构建上下文通过 `.dockerignore` 排除私有目录、密钥、运行数据及已有依赖目录。

准备 HTTPS 域名、证书完整链、私钥、CA 链，以及独立模型账户。模型配置是拥有者可读写的普通 `0600` 文件，格式参考 [Provider 配置](../python/app/providers/config.py)：`PROVIDER_PROFILE=aliyun-bailian`，明确填写 endpoint、region、API key、control、embedding、rerank、generation 四槽位，Embedding 1024 维、60 RPM。模型调用只按 token 记录用量（`usage_log`，见决策 0021），不统计货币，初始化不会赠送额度。不要在终端、Git 或截图中暴露密钥。

## 生成配置并启动

使用仓外目录。下面路径是安装者自备文件，不能提交到 Git。初始化需要 root，以创建与容器 UID 对应的受限目录和文件：

```bash
sudo python/.venv/bin/python scripts/ops/selfhost.py init \
  --directory /var/lib/ecommerce-rag/merchant \
  --origin https://shop.example.com:8443 \
  --provider-config /etc/ecommerce-rag/providers.env \
  --tls-cert /etc/ecommerce-rag/web.crt \
  --tls-key /etc/ecommerce-rag/web.key \
  --tls-ca /etc/ecommerce-rag/web-ca.crt
sudo docker compose --env-file /var/lib/ecommerce-rag/merchant/compose.env \
  -f docker/selfhost/compose.yml build
sudo docker compose --env-file /var/lib/ecommerce-rag/merchant/compose.env \
  -f docker/selfhost/compose.yml up -d
```

`up -d` 会自动按顺序执行一次性任务：`db-init`（建角色/库/扩展）→ `migrate`（checksum 迁移）→
`bootstrap`（tenant、shop 与 owner 骨架），然后启动 `gateway`、`ai`、`postgres`。三者幂等，
`gateway` 通过 `service_completed_successfully` 等它们成功。**不要手工再跑一次 bootstrap 或迁移**；
这一步失败时先看 `docker compose ps -a` 与对应一次性任务的日志。

Python 镜像在构建时拷贝 `sql/`，并把 `V2_MIGRATION_DIR` 指向 `/app/sql/migrations`；当前
镜像快照包含 56 条迁移。升级代码后必须先重建 `ai` 镜像再 `up -d`——只重启旧镜像会继续
携带旧迁移快照（本机旧镜像实测只有 31 条），与库内 checksum 或新表结构不一致。

首次安装（内置账号档）：

```bash
# 1. 确认服务就绪（CA 就是初始化时生成的 web CA）
curl --cacert /var/lib/ecommerce-rag/merchant/web/web-ca.crt \
  https://shop.example.com:8443/healthz
# 2. 取出一次性安装令牌（只打印一次、不落盘，30 分钟内有效）
sudo docker compose --env-file /var/lib/ecommerce-rag/merchant/compose.env \
  -f docker/selfhost/compose.yml logs gateway | grep -m1 SETUP_TOKEN=
# 3. 浏览器首次访问需信任 /web/web-ca.crt（或临时忽略证书错误），然后打开：
#    https://shop.example.com:8443/setup?token=<令牌>
#    页面会把令牌换成短期的 HttpOnly cookie，并 303 到不带 token 的 /setup；
#    反向代理的访问日志请对 token= 参数打码（例如 log_format 里替换该查询参数）。
# 4. 设置 owner 密码 → 自动跳转 /login?app=admin → 登录 → /admin（普通员工登录进入 /chat）
```

`/setup` 返回 `409 setup_requires_bootstrap` 说明一次性编排没有完成（`db-init`/`migrate`/`bootstrap`
任一失败），先修复编排再重试；返回 `409 setup_already_completed` 说明本地凭据已存在，重置走 CLI。
`APP_ENV=production` 下 loopback 免令牌不生效，必须带令牌。**迁移顺序不能颠倒**：schema 必须先于
owner 骨架，owner 骨架必须先于 `/setup`。

若构建工具镜像无法下载，可使用仓库锁定的本地 Go/Node/uv 工具链，并通过 uv 管理的独立 CPython 3.12 运行 `scripts/ops/selfhost-build-local.py --directory 仓外新目录`。它仅使用经锁清单校验的本机 pgvector 导入镜像作为可选运行基底，生成 source-only 应用镜像；依赖仍须按锁文件安装。公开 Compose 的数据库引用使用官方来源 manifest；本机离线导入的镜像摘要与 registry manifest 分开，不能互换宣称已验证公网拉取。

默认仅绑定本机 Web 端口：Compose 发布 `127.0.0.1:8443`，`ECR_WEB_BIND` 默认就是 `127.0.0.1`。
从其他机器访问时，要么在初始化时显式 `--bind 0.0.0.0`，要么前置一层反向代理；两种方式都要配主机防火墙和有效证书。
即使发布到 `0.0.0.0`，`/setup` 仍强制一次性令牌（`APP_ENV=production` 下 loopback 豁免不生效）。
数据库和内部 RPC 不发布主机端口。测试环境可显式使用 `--local-certificate` 代替三项证书参数；
只在自己的测试域名上安装生成的测试证书信任，不关闭证书校验。DNS/hosts 必须让浏览器解析该域名；
容器内同一域名由 gateway 的网络别名解析。

模型调用没有货币账本（0021）：每次调用的 token 用量写入 `usage_log`，用 `scripts/dev/provider-usage.py status` 查看（读 `V2_USAGE_DSN` 或 `V2_GATEWAY_DSN`）。运行目录与文件的所有者必须与运行 AI/worker 的 UID 一致，不要靠放宽权限来绕过检查。

安装账号保存在受限 bootstrap 目录下的 accounts.json 文件，只在本地私下查看，首次使用后修改密码。

- **员工与 access_admin**：管理台「新增员工」只能授予 `merchant_admin` 与 `knowledge_reviewer`；`access_admin` 属于带外授权，页面不提供。转移或新增 `access_admin` 需要运维以 migrator 身份执行
  `INSERT INTO admin_role_grant(...) ... ON CONFLICT (tenant_id,user_id,role) DO UPDATE ...` 并自行记录审计，不通过 HTTP 接口。
- **内置账号（默认，`--identity-profile local`）**：`selfhost.py` 只写 `accounts: []`，bootstrap 创建 `merchant` 租户、`main-shop` 店铺与一个 owner 骨架（不写密码），不启动 Keycloak。启动后打开 `https://<host>/setup` 设置 owner 密码：该页面只在 loopback 或携带启动日志中一次性 `SETUP_TOKEN` 时可访问，成功后永久关闭。之后的密码重置走 `go run ./cmd/local-account reset-password --env <0600 运行配置> --user <用户名>`。
- **OIDC（`--identity-profile oidc_browser`）**：保留 Keycloak 路径，账号来自 realm 导入；Keycloak 的 bootstrap 管理员属于独立运维身份，配置在受限 `identity.env`。首次导入 realm 后不要用旧导入文件覆盖运行账号。

### Keycloak realm 导入（`oidc_browser`）

Keycloak 26 的 realm JSON 必须同时满足以下四点，否则登录或授权会在不同阶段失败：

- **自定义属性声明**：在 realm 的 user profile 中声明 `tenant_id`、`user_id`、`role`、`shop_ids`、`permission_revision`、`revoked`。未声明的属性会被 Admin REST API 静默丢弃：账号能建，但 token/数据库里没有 claim。
- **`shop_ids` 类型**：该属性必须 `jsonType=JSON`，值为 JSON 数组字符串（如 `["main-shop"]`），不能写成逗号分隔字符串。
- **audience mapper**：客户端加 `oidc-audience-mapper`，把 API audience 写进 access token；否则 token 的 `aud=account`，go-oidc 会拒绝。
- **数据库侧**：`tenant.status='active'`，且 token 的 `permission_revision` 与 `app_user.permission_revision` 一致；否则授权失败。

### OSS 归档（可选）

`oss.env`（仓库外，0600）除凭据外必须满足：

- 含 `OSS_REGION`；
- `OSS_ENDPOINT` 带 `https://` 前缀；
- 文件尾保留换行（按行解析，最后一行的值会被截断或丢失）。

桶授权策略模板（对象动作挂 `BUCKET/*`，桶级动作挂 `BUCKET`）：

```json
{
  "Version": "1",
  "Statement": [
    {"Effect": "Allow", "Action": ["oss:GetObject", "oss:PutObject", "oss:DeleteObject"],
     "Resource": ["acs:oss:*:*:BUCKET/*"]},
    {"Effect": "Allow", "Action": ["oss:ListObjects", "oss:GetBucketInfo"],
     "Resource": ["acs:oss:*:*:BUCKET"]}
  ]
}
```

### 情报 worker 与报告页

- `rag_v2_intel` 是集群级角色，由 `selfhost-db-init.py` 按 bootstrap 密码表自动创建；迁移不执行 `CREATE ROLE`。
- `V2_INTEL_ENABLED` 默认 0；为 0 时不启动 intel 进程，也不占用连接与端口。启用时需要 `V2_INTEL_DSN`（`rag_v2_intel` 角色）与 `V2_PROVIDER_CONFIG`。
- 报告页读取 `ECR_REPORT_PATH`，应指向报告 CLI 写出的 `demo-report.json`。selfhost 默认未挂载报告文件；未设置或文件不存在时接口返回 404 `report_unavailable`，这是预期，不是 500。需要展示时把报告文件以只读方式挂进 gateway 容器并设置该变量。

无论哪种 profile，应用都不执行 DDL；升级先备份，再运行 bootstrap 迁移任务，再启动同一版本的应用。


## 源码部署（systemd）

不使用容器时，用两个单元分别托管后端与前端，二者都以仓库外的受限配置启动：

```ini
# /etc/systemd/system/ecommerce-rag.service
[Unit]
Description=EcommerceRAG backend (gateway + ai)
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=ecr
WorkingDirectory=/opt/EcommerceRAG2
EnvironmentFile=/etc/ecommerce-rag/dev.env
ExecStart=/opt/EcommerceRAG2/scripts/dev/v2_services.py --env /etc/ecommerce-rag/dev.env
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/ecommerce-rag /run/ecommerce-rag

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/ecommerce-rag-web.service
[Unit]
Description=EcommerceRAG web (merged SPA)
After=ecommerce-rag.service

[Service]
Type=simple
User=ecr
WorkingDirectory=/opt/EcommerceRAG2/frontend
ExecStart=/usr/bin/npm run dev -- --host 127.0.0.1 --port 5173
Environment=VITE_GATEWAY_ORIGIN=http://127.0.0.1:8080
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

`dev.env` 只放连接串与路径，凭据在 `/etc/ecommerce-rag/providers.env`（0600）。升级流程：
`git pull --ff-only` → 依赖安装（`uv sync --frozen`、`npm ci`）→ `systemctl restart ecommerce-rag ecommerce-rag-web`；
回滚回到上一个提交后重启同样的两个单元。源码部署同样走 `/setup` 首次安装，且迁移必须在
bootstrap 之前完成（与 Compose 三段式一致）。

## 前置代理部署（D2）

D2 是「前置代理终止 TLS、网关只提供 loopback 明文」的形态，与 D1（内置 edge）二选一，
**不得叠加**。适用于多域名、已有 nginx/Caddy/ALB，或需要统一证书与 WAF 的部署。

四件事，逐项核对：

1. **TLS 终端**：证书与私钥只安装在前置代理；网关 `WEB_ROOT` 必须为空。
2. **SPA**：前置代理提供前端构建产物（`npm run build` 的输出）的静态文件，并把未知客户端路径回退到 `/index.html`。
3. **协议头只注入一次**：只有前置代理设置 `X-Forwarded-Proto`；不要在前面再加一层代理，
   也不要同时启用 D1。
4. **SSE 不缓冲**：nginx 使用 `proxy_buffering off`，Caddy 使用 `flush_interval -1`，
   否则事件会被缓存到连接结束才下发。

网关侧配置：

- `WEB_ROOT=`（空）
- `HTTP_ADDR=127.0.0.1:8080`
- `V2_CUSTOMER_PROXY_HOST=<代理地址>`（可信代理；用于客户 IP/协议判定）
- 启动期 `validateDeploymentForm` 会拒绝 D4（`WEB_ROOT` 非空且配置了代理）以及非 loopback 的明文监听。

示例配置：

- [`docker/selfhost/examples/nginx.conf`](../docker/selfhost/examples/nginx.conf)
- [`docker/selfhost/examples/Caddyfile`](../docker/selfhost/examples/Caddyfile)

两个文件按“单商家、单域名”编写，均带 R4-2/3/4 注释；部署前替换域名、证书路径与静态目录。
本仓库没有 nginx/caddy 二进制，示例未做语法校验；上线前请用 `nginx -t` 或 `caddy validate` 自查。

本地验证（不依赖 Chromium）：

```bash
bash scripts/dev/d2-proxy-smoke.sh
# 或作为 --e2e 层的一项：
bash scripts/check.sh --e2e
```

## 使用与故障定位

- `/admin/overview`：管理员登录，设置店铺名称、欢迎语及客户入口；上传、预览、审核和发布知识。
- `/shop/my-shop`：客户页；Cookie 丢失后不能恢复匿名历史。顾客 API 的 `shop_slug` 取
  `shop.public_slug`（默认 `my-shop`），不是数据库 `shop_id`（默认 `main-shop`）；传错会得到
  `404 shop_unavailable`。
- `/support`：员工登录，查看队列、领取、回复。
- `/chat`：员工 AI 辅助区，不代替人工向客户发送消息。

管理端写接口（导入、审核、发布等）除 session cookie 外还必须带 `Origin` 与
`X-CSRF-Token`；CSRF token 取自 `/v1/auth/login` 响应的 `csrf_token`。只带 cookie 的
脚本会得到 `403 local_request_rejected`。

使用 `docker compose ... ps` 检查容器状态，`logs --tail 50 服务名` 定向检查，日志不要发布到公共 Issue。缺少模型配置、数据库迁移、OIDC、证书或账本时必须修复配置，不能切换开发身份或 fake。首次启动 Keycloak 较慢，Gateway 在身份服务就绪后自动重试启动。应用不运行 DDL；升级先备份，再运行 bootstrap 迁移任务，再启动同一版本的应用。

客户消息和排队状态持久化；Go 重启后员工重新登录，人工领取重新排队，中断 AI 转人工。应用为单实例，第二个 Gateway 会拒绝获取数据库所有权。内部 RPC 证书默认 90 天，HTTPS 测试证书 30 天；部署者须在到期前更新证书、保持 SAN/CA/文件权限并重启相关服务。正式证书由部署者管理。

**已知限制（2026-11-06 自部署演练）**：`--identity-profile local` 的本版管理端导入确认、
审核、发布命令返回 403 `command_failed` / `ingest_not_authorized`；这是运行时代码缺陷，
已记录并等待单独修复，顾客链路与只读管理页不受影响。自动化导入验收暂不要在 local
profile 上执行。

第三方许可见 [声明](../THIRD_PARTY_NOTICES.txt)。自有代码 MIT 不覆盖第三方服务、资料或模型账户的许可。
