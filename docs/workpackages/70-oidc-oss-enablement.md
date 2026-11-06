# 70 启用 OIDC/OSS 时修复 OSS 存储构造缺陷

- 状态：见 INDEX.md
- 依据：用户要求启用 Keycloak/OIDC 与 OSS 并以商家视角跑通全流程；
  启用时暴露的真实缺陷
- 前置：无

## 问题

首次把 `INGEST_STORAGE_PROFILE` 切到 `oss`，AI 进程启动即失败：

```text
TypeError: AlibabaOSSObjectStore.__init__() takes 1 positional argument but 2 were given
```

`python/app/ingest/runtime.py:load_artifact_store` 以位置参数调用关键字专用构造器
（`AlibabaOSSObjectStore(load_oss_config())`）。该路径自 OSS 适配器引入以来从未被
启用，因此一直未被测试覆盖。修好构造后暴露出第二个缺口：`oss2` 在
`pyproject.toml` 的 `cloud` 可选组里，而 venv/自部署镜像/CI 的 `uv sync` 都不装
该组 → `OSS SDK is unavailable`。

## 修复

1. `runtime.py`：`AlibabaOSSObjectStore(config=load_oss_config())`；
2. `scripts/check.sh` 的 `python-sync`、`docker/selfhost/python.Dockerfile`、
   `bootstrap-dev.sh` 的提示统一为 `uv sync --frozen … --group cloud`
   （锁文件已含 `oss2==2.19.1`，无新增依赖版本）；
3. 回归测试：`test_object_storage.py::ArtifactStoreSelection` 锁定
   “oss profile → 用 `config=` 关键字构造”。

## 验证

```bash
bash scripts/check.sh            # 44/0/0（含新测试与 python-lint）
bash scripts/check.sh --smoke    # 47/0/0，五条基线逐字节一致
# 真实 OSS：ingest 同款 store 往返（put → get sha256 → delete）通过
# 真实 OIDC：dev 栈 oidc_browser 登录 → /v1/auth/session 200 → /admin/v1/session 200
```

## 环境切换记录（仓外配置，不入库）

- Keycloak 容器 `ecr-keycloak`（26.0，127.0.0.1:26080，restart unless-stopped）；
- realm `ecr-dev`：confidential client `ecr-gateway`（redirect 127.0.0.1:5173）、
  audience mapper、用户 `merchant`；
- `/etc/ecommerce-rag/oidc-dev.json`（authorization_source=database，绑定
  v2-demo-tenant/dev-user/shop-demo）与 `oidc-client-secret`（0600）；
- Keycloak 以 `--http-relative-path=/identity` 启动，Vite dev server 代理
  `/identity`（与自部署 compose 的形态一致），issuer 为
  `http://127.0.0.1:5173/identity/realms/ecr-dev`——远端访问只需转发 5173 一个端口；
- `dev.env`：身份档改为 OIDC 浏览器档、追加 OIDC 配置路径、存储档改为 oss；
  原文件有备份 `dev.env.bak-oidc-*`。

## 不要做

- 不改 Tavily/answer 链路；不动 smoke 基线；不改历史迁移；不改 CONTRACTS_* 棘轮；
  不把任何凭据/secret 提交进仓库。

## 汇报

五节格式；附启动失败原始 traceback、修复 diff、真实 OSS 往返与 OIDC 登录输出。

## 测试账号（仅 dev 环境，密码由主 agent 口头交付，不入仓库）

| 用户名 | 管理角色 | 入口 |
| --- | --- | --- |
| merchant | merchant_admin + knowledge_reviewer | /admin |
| reviewer | knowledge_reviewer | /admin（审核/发布） |
| observer | platform_observer | /admin（只读） |
| access | access_admin | /admin/access/*（账号与权限、授权审计） |
| agent | 无管理授权 | /support、/chat |
| 顾客 | 匿名，无账号 | /shop/shop-demo |

绑定在 `/etc/ecommerce-rag/oidc-dev.json`（subject→tenant/user/shop），
角色由 dev 库 `admin_role_grant` 决定；access_admin 按 install.md 走带外 SQL 授予。

## 情报服务启用（2026-11-06 追加）

dev 栈已开启情报服务（DSN 用 rag_v2_intel 角色，限流与轮询按工作包约定）：
worker 监听 127.0.0.1:50054，网关 `/admin/v1/intel/observations` 返回 200。
启用时发现并修复一个真实缺陷：每请求的 `scoped()` 副本漏拷贝 intel RPC 客户端，
导致接口恒 503（提交 3a5cc80 + 回归测试）。当前 dev 是 mock provider，观测嵌入为
零向量，语义查询会显示 `query_degraded=true`；真实 provider 下才有相似度排序。
创建一条监控配置后，worker 才会真正发起 Tavily 搜索（产生外部调用与用量记录）。
