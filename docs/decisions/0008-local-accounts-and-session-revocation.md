# 0008 本地账号与会话吊销

日期：2026-10-24　状态：提议

## 背景

当前登录依赖 Keycloak（`docker/identity`）。单商家私有部署时，用户往往只有店主和一两个客服，却要额外维护一套 OIDC 服务、一个浏览器客户端和一个账号同步流程。ROADMAP 的 M4 要求把 Keycloak 降为可选 profile，由 Go 自带账号密码登录。

## 决定（提议）

- **账号**：用户名 + 密码。密码用 argon2id 存储，参数 `time=3`、`memory=64MiB`、`parallelism=4`，随机 16 字节 salt、32 字节输出，按 PHC 字符串编码保存；参数写进代码常量并记录在这里，便于以后调参后仍能验证旧哈希。
- **不选 bcrypt**：72 字节截断，且没有内存硬度；在这个规模下 argon2id 是标准选择。
- **会话**：服务端保存会话记录（`id`、`user_id`、创建/过期时间、吊销原因），令牌用 32 字节随机值，数据库只存哈希。**登出、改密、锁定、管理员强制下线都必须能立即吊销**，不能只靠自然过期。
- **限流与锁定**：按 IP 与账号双维度计数，滑动窗口 + 指数退避；连续失败锁定 15 分钟后自动解锁，不需要人工介入。
- **Keycloak**：保留现有 OIDC 代码路径，但只在显式 profile 下启用；默认部署不启动。
- **迁移**：接在 0029 之后（从 0030 起），账号表与会话表的写入方是 Go（`rag_v2_gateway` 角色），迁移用户仍是非超级用户、无 `BYPASSRLS`。

## 后果

- 首次部署需要一个初始化管理员的方式（迁移或运维命令），不允许留默认弱口令。
- 身份契约不变：`/v1/session` 的返回结构、scope 签发链路保持兼容，Python 仍然只使用 Go 签发的 scope。
- 与 [0002](0002-keep-go-python.md) 一致：认证完全留在 Go 侧，Python 不感知账号。

## 补充（2026-10-25）：实现约定与边界

本节补充上文，不修改原有条目。

### 参数与密码策略

- 实现按上文常量：argon2id `v=19`、`time=3`、`memory=64 MiB`、`parallelism=4`、salt 16 字节、输出 32 字节，PHC 编码保存。此前草稿里出现过的 `parallelism=2` 作废，以本条为准。
- 校验时解析 PHC 参数重算，并设上界 `memory≤256 MiB`、`time≤10`、`parallelism≤8`，防止被篡改的哈希拖垮进程。
- 密码长度 12–256 字节，不得等于用户名。

### 存储与单写者

- 迁移 0030 只新增 `local_credential`、`local_session`、`local_auth_event`，并给 `app_user` 增加 `revoked_by`、`revoked_reason`；不改 0001–0029。
- 三张新表只授权 `rag_v2_gateway`；`rag_v2_ai` 与 `rag_v2_ingest` 无任何权限；审计表只给 INSERT/SELECT。
- tenant/shop 的写入方仍是迁移与 `selfhost-bootstrap.py`（migrator 角色）；0030 不新增任何 `SECURITY DEFINER` 的建租户函数，gateway 不获得建租户能力。

### 登录响应与限流

- 密码错误与账号不存在统一返回 401 `invalid_credentials`；未知用户也执行一次固定 dummy hash，响应体一致、耗时相近。
- 423 `account_locked` 只用于已确认的锁定态，且该 IP 未超过探测阈值；同一 IP 超过阈值后一律 429 + `Retry-After`，不再返回 423。
- 账号维度连续 5 次失败锁定 15 分钟，到期自动解锁；IP 维度 10 次/分钟、60 次/小时。
- 审计事件：`login_failed`、`account_locked`、`rate_limited`。

### 会话与吊销

- cookie 沿用 `ecr_v2_session`，HttpOnly、SameSite=Lax，HTTPS 下 Secure；库中只存 cookie 值与 CSRF 的 sha256。
- 空闲 30 分钟滑动续期，绝对上限 12 小时；每次请求继续走 `Authority.Refresh` 校验 `revoked_at` 与 `permission_revision`。
- `local_session.revoked_reason` 取值：`password_changed`、`admin_reset`、`logout`、`lockout`、`account_revoked`。
- 本人改密吊销该用户全部会话（含当前会话），前端回登录页；管理员重置只吊销目标用户；账号锁定同时吊销该用户的本地会话。
- `must_change_password` 置真时只允许 `/v1/auth/session`、`/v1/auth/password`、`/v1/auth/logout`，其余请求 403 `password_change_required`；改密成功后清除。

### 停用与锁定的区分

- **停用**：管理员动作、全通道、永久。`app_user.revoked_at` 是账号级唯一开关；停用事务内同时写 `revoked_at`、`revoked_by`、`revoked_reason='account_disabled'`，并吊销全部本地会话（`account_revoked`）。`enable` 只清三列，不恢复旧会话。校验点两处：本地会话解析后查 `revoked_at`（403 `account_revoked`）；OIDC 回调完成 subject→user 绑定后同样校验，停用账号不再签发会话。审计 `account_disabled` / `account_enabled`。
- **锁定**：自动、15 分钟、只影响本地密码登录。只写 `local_credential.failed_attempts`/`locked_until`/`last_failed_at`，返回 423；不吊销既有会话，不影响 OIDC。
- 两者严格分字段、不互相推导。管理员不能停用自己；最后一个 `owner` 不能停用；账号只做停用，不做物理删除。

### 账号创建的三条边界

1. **商家 first-run setup（M4）**：`GET/POST /setup` 仅在账号表为空时可用，成功后永久关闭。写入范围严格限定 `app_user`、`local_credential`、`admin_role_grant`；`admin_role_grant` 复用迁移 0010 的 `ecr_issue_admin_grant`，不另写 SQL。单事务内写入并用 `pg_advisory_xact_lock` 串行化；重复提交 409 `setup_already_completed`；缺少 tenant/shop 返回 409 `setup_requires_bootstrap`，页面直接给出要执行的初始化命令与文档链接。默认仅 loopback；非 loopback 必须携带启动时打印到日志的一次性安装令牌（单次、进程重启作废）。
2. **员工账号（M4 收尾或 M5 初）**：管理台由店主创建员工，生成一次性初始密码，员工首登强制改密；不引入注册页、不引入邮件。`go/cmd/local-account` 与 `/setup` 共用同一套哈希参数、密码策略、`must_change_password` 与审计写入；CLI 只提供 `create`、`disable`、`enable`、`reset-password`、`list`，密码只从 stdin 或交互读取，使用 `rag_v2_gateway` 角色写入，`list` 不输出哈希。
3. **顾客账号（现在不做）**：保持匿名 `customer_identity` + `customer_session`；跨设备历史见 [0009](0009-customer-cross-device.md)（提议，待确认）。M4/M5 不引入 SMTP 依赖或任何顾客注册接口。

### bootstrap 与部署边界

- `selfhost-bootstrap.py` 支持 `accounts: []`：只建 tenant `merchant` 与 shop `main-shop`；`bindings` 为空时不生成 OIDC browser 配置文件（不写 `oidc.json`，不写 realm 用户）。
- bootstrap 保持一次性、幂等，在迁移之后、gateway 之前执行。
- M4 验收：不启动 Keycloak、不传任何 OIDC 变量（`V2_IDENTITY_PROFILE=local`），能建号、能登录、能进 `/admin`。
- Keycloak 的 Compose profile 化与默认三服务仍归 M5；M4 只做 profile 与脚本支持。

### 补充（2026-10-25）：吊销传播与审计保留

- 停用通过 `revoked_at` 的变化触发 0006/0009 的 `permission_revision` bump，因此停用不仅让会话解析失败，也让已签发的 scope 在下次校验时失效。
- `local_auth_event` 保留 90 天，由运维脚本清理；应用进程不执行清理 SQL。
