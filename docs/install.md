# 单商家安装

[项目入口](../README.md) · [导入知识](knowledge.md) · [客服操作](support.md) · [备份恢复](backup.md)

此部署运行一个商家、一家店铺。客户匿名咨询；员工使用本地 Keycloak 登录。订单、实时价格、库存与配送承诺不在首版能力内。开发验收与生产验收分开；当前不能宣称生产就绪。

## 准备

Linux、Docker Engine 与 Compose v2、Git、Python 3.12、uv、openssl。建议至少 4 核、8 GiB 内存、20 GiB 可用磁盘。克隆仓库后，在仓库根目录运行 `uv sync --project python --frozen`。应用依赖以锁文件为准。Docker 构建上下文通过 `.dockerignore` 排除私有目录、密钥、账本及已有依赖目录。

准备 HTTPS 域名、证书完整链、私钥、CA 链，以及独立模型账户。模型配置是拥有者可读写的普通 `0600` 文件，格式参考 [Provider 配置](../python/app/providers/config.py)：`PROVIDER_PROFILE=aliyun-bailian`，明确填写 endpoint、region、API key、control、embedding、rerank、generation 四槽位，Embedding 1024 维、60 RPM。预算单位为人民币，初始化不会赠送额度。不要在终端、Git 或截图中暴露密钥。

## 生成配置并启动

使用仓外目录。下面路径是安装者自备文件，不能提交到 Git。初始化需要 root，以创建与容器 UID 对应的受限目录和文件：

```bash
sudo python/.venv/bin/python scripts/selfhost.py init \
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
  -f docker/selfhost/compose.yml --profile setup run --rm bootstrap
sudo docker compose --env-file /var/lib/ecommerce-rag/merchant/compose.env \
  -f docker/selfhost/compose.yml up -d
```

若构建工具镜像无法下载，可使用仓库锁定的本地 Go/Node/uv 工具链，并通过 uv 管理的独立 CPython 3.12 运行 `scripts/selfhost-build-local.py --directory 仓外新目录`。它仅使用经锁清单校验的本机 pgvector 导入镜像作为可选运行基底，生成 source-only 应用镜像；依赖仍须按锁文件安装。公开 Compose 的数据库、Redis 引用使用官方来源 manifest；本机离线导入的镜像摘要与 registry manifest 分开，不能互换宣称已验证公网拉取。

默认仅绑定本机 Web 端口。需要对外开放时，在初始化时显式设置 `--bind 0.0.0.0`，同时配置主机防火墙和有效证书。数据库、Redis 和内部 RPC 不发布主机端口。测试环境可显式使用 `--local-certificate` 代替三项证书参数；只在自己的测试域名上安装生成的测试证书信任，不关闭证书校验。DNS/hosts 必须让浏览器解析该域名；容器内同一域名由 Compose edge 别名解析。

本工程开发验收使用已有共享账本时，另外传入 `--budget-dir`、`--budget-file`、`--budget-id` 和对应 `--budget-cny`，复用原账本，不复制、不重建。账本目录及文件所有者必须与运行 AI/worker 的 UID 一致；不能通过放宽权限绕过检查。

安装账号保存在受限 `bootstrap/accounts.json`，只在本地私下查看，首次使用后在 Keycloak 修改密码。默认管理员 `admin`、客服 `support`。Keycloak 的 bootstrap 管理员属于独立运维身份，其配置在受限 `identity.env`。首次导入 realm 后不要用旧导入文件覆盖运行账号。

## 使用与故障定位

- `/admin/overview`：管理员登录，设置店铺名称、欢迎语及客户入口；上传、预览、审核和发布知识。
- `/shop/my-shop`：客户页；Cookie 丢失后不能恢复匿名历史。
- `/support`：员工登录，查看队列、领取、回复。
- `/chat`：员工 AI 辅助区，不代替人工向客户发送消息。

使用 `docker compose ... ps` 检查容器状态，`logs --tail 50 服务名` 定向检查，日志不要发布到公共 Issue。缺少模型配置、数据库迁移、OIDC、证书或账本时必须修复配置，不能切换开发身份或 fake。首次启动 Keycloak 较慢，Gateway 在身份服务就绪后自动重试启动。应用不运行 DDL；升级先备份，再运行 bootstrap 迁移任务，再启动同一版本的应用。

客户消息和排队状态持久化；Go 重启后员工重新登录，人工领取重新排队，中断 AI 转人工。应用为单实例，第二个 Gateway 会拒绝获取数据库所有权。内部 RPC 证书默认 90 天，HTTPS 测试证书 30 天；部署者须在到期前更新证书、保持 SAN/CA/文件权限并重启相关服务。正式证书由部署者管理。

第三方许可见 [声明](../THIRD_PARTY_NOTICES.txt)。自有代码 MIT 不覆盖第三方服务、资料、模型账户或 Redis 的许可。
