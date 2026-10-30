# 认证、授权与密钥（重构方案 · 安全层）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充总纲未覆盖的**安全边界**维度：授权判定收口、密钥清单与轮换、个人信息保留。

---

## 0. 现状（2026-10-27 实测）

| 能力 | 现状 |
| --- | --- |
| 登录 | 本地账号（argon2id `time=3/m=64MiB/p=4`）+ 可选 OIDC（`--identity-profile oidc_browser`） |
| 会话 | 服务端保存会话，`local_session` 可即时吊销（五类原因） |
| 会话 Cookie | `HttpOnly`、`SameSite=Lax`、HTTPS 下 `Secure`；CSRF 由 cookie+密钥派生 |
| 限流/锁定 | IP 与账号双维度；5 次/15 分钟；锁定只影响本地密码登录 |
| 权限模型 | `admin_role_grant`（四种角色）+ 申请/审批（`ecr_issue_admin_grant`） |
| 授权判定位置 | `internal/auth`（scope 签发）、`internal/admin`、`internal/customer`、`internal/localauth` 各有一段 |
| 密钥材料 | `providers.env`（模型密钥）、`oss.env`（对象存储）、`SCOPE_SIGNING_KEY_FILE`、RPC CA/证书、provider 主密钥（32B/0600） |
| 租户供应商密钥 | Go 侧 AES-GCM，密文带 `v1:` 前缀，只存 `last4`（`internal/admin/provider_crypto.go`） |
| 个人信息 | `customer_identity` 为匿名 ID；`customer_message` 存顾客正文 |

---

## 1. 问题清单

### S1 授权判定分散在四个包（应重构）

- 现象：`auth`（scope）、`admin`（角色路由）、`customer`（匿名会话 + Origin/CSRF）、`localauth`（会话/锁定）各自实现判定片段
- 风险：新增一个入口时容易漏掉其中一项（例如只在 admin 加了角色校验，顾客侧没加 Origin 校验）
- 判定：不是"重复代码"，是"边界不唯一"

### S2 密钥材料没有清单

- 现象：五类密钥分散在 4 个位置，没有任何文档说明"哪些在文件、哪些在库、谁能读"
- 风险：轮换时漏改、备份时误打包、排障时误打印

### S3 轮换流程缺失

- 现状：`SCOPE_SIGNING_KEY_FILE`、RPC 证书（90 天）、HTTPS 证书（30 天）、provider 主密钥都没有轮换步骤
- 风险：证书过期直接停服；主密钥无法平滑更换

### S4 个人信息保留期未定义

- 现象：`customer_message` 保存顾客原文，但没有保留期限、导出、删除流程
- 风险：商家自部署时按 PIPL 有义务，产品不提供能力就等于把合规风险丢给商家

### S5 密钥不得出现在日志与错误里（已有部分保障）

- 现状：`provider_crypto_test.go` 断言"密文不含明文、损坏密文报错不回显"
- 缺口：没有全局断言（例如 HTTP 500 的 detail 字段是否可能带出配置）

---

## 2. 工作包

### S-W1　授权判定收口（1–2 天，与后端 P-A/P-B 同批）

目标形态：

```
internal/auth/decide.go     唯一判定入口：Decide(principal, action, resource) (Decision, error)
   ├─ internal/admin 路由：只把 (session, action, resource) 交给 Decide
   ├─ internal/customer：同上，resource 带 shop 与 customer_id
   └─ internal/localauth：只负责"是不是有效会话"，不做业务授权
```

验收：

```bash
# 业务包里不再出现角色字符串比较
grep -rn "'merchant_admin'\|'access_admin'" go/internal --include='*.go' | grep -v _test | grep -v internal/auth | wc -l  # 期望 0
go test ./... && bash scripts/check.sh
```

### S-W2　密钥清单（2 小时）

产出 `docs/secrets.md`：每行 = 名称 / 存放位置 / 权限 / 谁能读 / 轮换周期 / 轮换步骤 / 失效后果。

必须覆盖：`providers.env`、`oss.env`、provider 主密钥、`SCOPE_SIGNING_KEY_FILE`、RPC CA 与证书、HTTPS 证书、数据库口令。

验收：`grep -c '^| ' docs/secrets.md` ≥ 7，且每行都有"轮换步骤"。

### S-W3　轮换演练（半天）

1. 轮换一次 `SCOPE_SIGNING_KEY_FILE`：新旧密钥并存窗口（验签接受旧、签发用新）→ 全量重签 → 移除旧
2. 轮换一次 HTTPS 证书：`selfhost.py` 重新生成 → 重启 gateway → `/probe` 通过
3. 记录耗时与失败点

### S-W4　个人信息保留（半天，先出决策再实现）

写 `docs/decisions/0025-customer-data-retention.md`（提议 → 采纳），至少定义：

- `customer_message` 保留 180 天（可配置），到期物理删除
- 顾客请求"删除我的对话"：一个管理端按钮 + 一个 CLI（二选一，按 0020 的 A 级要求放 UI）
- 导出：按 `customer_id` 导出 JSON

实现放在 P2，不阻塞重构；本文件只要求**决策先落地**。

### S-W5　密钥泄露的机械断言（2 小时）

`scripts/check/check-secrets.py`：

- 扫描 `go/internal`、`python/app` 里所有 `http.Error` / `writeJSON` 的 detail 字段，若引用了配置变量则告警
- 扫描日志调用，禁止打印 `*Key*`、`*Secret*`、`*Token*` 变量

---

## 3. 红线

1. 任何密钥不得进入 Git、日志、错误响应、评测报告
2. 停用/锁定/吊销三态语义不得合并（见 0008）
3. 授权判定只能有一个入口（S-W1 完成后）
4. `human_review_completed` / `real_service_acceptance` 的标注不得被安全重构改动

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 判定收口 | §S-W1 的 grep | 0 |
| 密钥清单 | `docs/secrets.md` 行数 | ≥ 7，含轮换步骤 |
| 轮换演练 | 按 S-W3 记录 | 两项通过 |
| 日志不含密钥 | `python3 scripts/check/check-secrets.py` | PASS |
