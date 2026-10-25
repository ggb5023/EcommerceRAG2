# 单商家安装

[项目入口](../README.md) · [导入知识](knowledge.md) · [客服操作](support.md) · [备份恢复](backup.md)

此部署运行一个商家、一家店铺。客户匿名咨询；员工默认使用 Go 内置账号登录，Keycloak 改为可选 profile。订单、实时价格、库存与配送承诺不在首版能力内。项目仍在开发阶段，尚未经过生产环境验证。

## 准备

Linux、Docker Engine 与 Compose v2、Git、Python 3.12、uv、openssl。建议至少 4 核、8 GiB 内存、20 GiB 可用磁盘。克隆仓库后，在仓库根目录运行 `uv sync --project python --frozen`。应用依赖以锁文件为准。Docker 构建上下文通过 `.dockerignore` 排除私有目录、密钥、账本及已有依赖目录。

准备 HTTPS 域名、证书完整链、私钥、CA 链，以及独立模型账户。模型配置是拥有者可读写的普通 `0600` 文件，格式参考 [Provider 配置](../python/app/providers/config.py)：`PROVIDER_PROFILE=aliyun-bailian`，明确填写 endpoint、region、API key、control、embedding、rerank、generation 四槽位，Embedding 1024 维、60 RPM。预算单位为人民币，初始化不会赠送额度。不要在终端、Git 或截图中暴露密钥。

## 生成配置并启动

使用仓外目录。下面路径是安装者自备文件，不能提交到 Git。初始化需要 root，以创建与容器 UID 对应的受限目录和文件：

```bash
sudo python/.venv/bin/python scripts/ops/selfhost.py init \
  --directory /var/lib/ecommerce-rag/merchant \
  --origin https://shop.example.com:8443 \
  --provider-config /etc/ecommerce-rag/providers.env \
  --budget-cny 20 \
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
# 4. 设置 owner 密码 → 自动跳转 /login → 登录 → /admin
```

`/setup` 返回 `409 setup_requires_bootstrap` 说明一次性编排没有完成（`db-init`/`migrate`/`bootstrap`
任一失败），先修复编排再重试；返回 `409 setup_already_completed` 说明本地凭据已存在，重置走 CLI。
`APP_ENV=production` 下 loopback 免令牌不生效，必须带令牌。**迁移顺序不能颠倒**：schema 必须先于
owner 骨架，owner 骨架必须先于 `/setup`。

若构建工具镜像无法下载，可使用仓库锁定的本地 Go/Node/uv 工具链，并通过 uv 管理的独立 CPython 3.12 运行 `scripts/ops/selfhost-build-local.py --directory 仓外新目录`。它仅使用经锁清单校验的本机 pgvector 导入镜像作为可选运行基底，生成 source-only 应用镜像；依赖仍须按锁文件安装。公开 Compose 的数据库、Redis 引用使用官方来源 manifest；本机离线导入的镜像摘要与 registry manifest 分开，不能互换宣称已验证公网拉取。

默认仅绑定本机 Web 端口：Compose 发布 `127.0.0.1:8443`，`ECR_WEB_BIND` 默认就是 `127.0.0.1`。
从其他机器访问时，要么在初始化时显式 `--bind 0.0.0.0`，要么前置一层反向代理；两种方式都要配主机防火墙和有效证书。
即使发布到 `0.0.0.0`，`/setup` 仍强制一次性令牌（`APP_ENV=production` 下 loopback 豁免不生效）。
数据库、Redis 和内部 RPC 不发布主机端口。测试环境可显式使用 `--local-certificate` 代替三项证书参数；
只在自己的测试域名上安装生成的测试证书信任，不关闭证书校验。DNS/hosts 必须让浏览器解析该域名；
容器内同一域名由 gateway 的网络别名解析。

已有预算账本时，传入 `--budget-dir`、`--budget-file`、`--budget-id` 和对应的 `--budget-cny` 复用它，不要复制或重建。账本目录和文件的所有者必须与运行 AI/worker 的 UID 一致，不要靠放宽权限来绕过检查。

安装账号保存在受限 `bootstrap/accounts.json`，只在本地私下查看，首次使用后修改密码。

- **员工与 access_admin**：管理台「新增员工」只能授予 `merchant_admin` 与 `knowledge_reviewer`；`access_admin` 属于带外授权，页面不提供。转移或新增 `access_admin` 需要运维以 migrator 身份执行
  `INSERT INTO admin_role_grant(...) ... ON CONFLICT (tenant_id,user_id,role) DO UPDATE ...` 并自行记录审计，不通过 HTTP 接口。
- **内置账号（默认，`--identity-profile local`）**：`selfhost.py` 只写 `accounts: []`，bootstrap 创建 `merchant` 租户、`main-shop` 店铺与一个 owner 骨架（不写密码），不启动 Keycloak。启动后打开 `https://<host>/setup` 设置 owner 密码：该页面只在 loopback 或携带启动日志中一次性 `SETUP_TOKEN` 时可访问，成功后永久关闭。之后的密码重置走 `go run ./cmd/local-account reset-password --env <0600 运行配置> --user <用户名>`。
- **OIDC（`--identity-profile oidc_browser`）**：保留 Keycloak 路径，账号来自 realm 导入；Keycloak 的 bootstrap 管理员属于独立运维身份，配置在受限 `identity.env`。首次导入 realm 后不要用旧导入文件覆盖运行账号。

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

## 使用与故障定位

- `/admin/overview`：管理员登录，设置店铺名称、欢迎语及客户入口；上传、预览、审核和发布知识。
- `/shop/my-shop`：客户页；Cookie 丢失后不能恢复匿名历史。
- `/support`：员工登录，查看队列、领取、回复。
- `/chat`：员工 AI 辅助区，不代替人工向客户发送消息。

使用 `docker compose ... ps` 检查容器状态，`logs --tail 50 服务名` 定向检查，日志不要发布到公共 Issue。缺少模型配置、数据库迁移、OIDC、证书或账本时必须修复配置，不能切换开发身份或 fake。首次启动 Keycloak 较慢，Gateway 在身份服务就绪后自动重试启动。应用不运行 DDL；升级先备份，再运行 bootstrap 迁移任务，再启动同一版本的应用。

客户消息和排队状态持久化；Go 重启后员工重新登录，人工领取重新排队，中断 AI 转人工。应用为单实例，第二个 Gateway 会拒绝获取数据库所有权。内部 RPC 证书默认 90 天，HTTPS 测试证书 30 天；部署者须在到期前更新证书、保持 SAN/CA/文件权限并重启相关服务。正式证书由部署者管理。

第三方许可见 [声明](../THIRD_PARTY_NOTICES.txt)。自有代码 MIT 不覆盖第三方服务、资料、模型账户或 Redis 的许可。
