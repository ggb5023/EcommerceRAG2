# 66 修复 local identity 的 ingest scope 未注册（自部署导入链 403）

- 状态：见 INDEX.md
- 依据：WP65 演练暴露的运行时代码缺陷；自部署（local profile）admin 导入 confirm 403
  `ingest_not_authorized`、review/publish 403 `command_failed`
- 前置：WP65 演练栈仍在运行（/tmp/ecr-selfhost-drill/runtime）

## 根因链（已由主 agent 核实）

1. `go/internal/gateway/authorization.go:102-107`：`authority()` 仅在
   `g.scopeIdentities != nil && g.identityCheck != nil` 时挂 `RegisterScopeIdentity`；
2. `wire.go:165` 对 local profile 注册了 `scopeIdentities`（校验开关），但
   `localIdentityAdapter` 没有 `identityCheck`；
3. `go/internal/auth/authority.go:295/359` 签发 ingest 相关 scope 时
   `RegisterScopeIdentity == nil` → 拒绝 → admin confirm 403。

对照：development/oidc 适配器都有 identityCheck，因此 dev 栈从未暴露此缺陷——
它只影响自部署 local profile 的导入/审核/发布链路。

## 修复要求

- 为 local profile 提供与其它适配器同构的 `identityCheck`：用本地会话解析当前
  admin 身份（复用 `localauth.Store` 的会话校验），使 `RegisterScopeIdentity`
  在 local profile 下正常绑定；不改变 scope 的签名、TTL、business_date 等既有语义；
- 不新增接口、不新增迁移；改动范围限于 gateway 的 identity 接线与必要测试；
- 修完后在 WP65 演练栈上重跑完整 admin 链路：登录 → 导入 batch → precheck →
  review 通过 → publish → 顾客问答（DONE + 引用 + can_copy=true）。

## 测试

- Go 单测：local profile 下 `authority().RegisterScopeIdentity != nil` 且
  bind 成功/失败路径（会话无效 → 拒绝）；表驱动。
- 回归：`bash scripts/check.sh` 44 项 FAILED 0；
  `bash scripts/check.sh --smoke` 47 项、五条基线逐字节一致（dev 栈 development
  profile 行为不得变化——改动后必须重建二进制 + strings + 重启 + 亲跑 smoke）。

## 验收命令

```bash
cd go && go test ./internal/gateway/ -run "ScopeIdentity|LocalIdentity" -v
bash scripts/check.sh
bash scripts/check.sh --smoke
# 演练栈真实链路（runtime 目录沿用 WP65 演练栈，gateway 镜像重建后）：
# admin 登录 → POST /admin/v1/merchant/imports 201 → precheck → review 通过 →
# publish → 顾客 POST /v1/customer/... messages → events 里 ANSWER + citations + can_copy=true
```

## 不要做

- 不改 development/oidc 适配器行为；不改 scope 语义（签名/时效/字段）；
- 不加迁移、不加接口；不动 dev 栈与 smoke 基线（必须逐字节一致）；
- 不改 CONTRACTS_* 棘轮；不 push；提交身份固定；不用 `git add .`。

## 实施与验证（2026-11-06）

根因确认：`authorization.go` 只在 `scopeIdentities != nil && identityCheck != nil`
时挂 `RegisterScopeIdentity`；local adapter 的 `Resolve` 返回 nil check，导致
ingest scope 未绑定。

修复（`go/internal/gateway/local_identity.go`）：
- 新增窄接口 `localSessionResolver`，`localIdentityAdapter.store` 使用该接口；
- `Resolve` 在解析 cookie 后返回 `localIdentityCheck(...)`，与 OIDC principal check 同构；
- `localIdentityCheck` 复用 `localauth.Store.ResolveSession`，会话撤销/过期/账号禁用
  拒绝，数据库故障返回 Unavailable；身份 tenant/user 变化拒绝；
- 未改 scope 签名、TTL、business_date 或其它适配器。

测试：
```text
cd go && go test ./internal/gateway/ -run "ScopeIdentity|LocalIdentity" -v
  TestScopeIdentityRequiresBoundLiveCredential PASS
  TestLocalIdentitySessionCheck PASS（valid/revoked/account disabled/db unavailable）
  TestLocalIdentityScopeRegistration PASS（bind/validate + 撤销/DB 故障）
```

Dev 回归：
```text
go build -o .local/v2-gateway ./cmd/gateway
strings .local/v2-gateway | grep -c identity_changed -> 1
systemctl restart ecommerce-rag-dev
13:14:15 .local/v2-gateway / app.ai_process，HEAD 0eeb545 13:14:06
bash scripts/check.sh         -> PASSED 44 / UNCONFIGURED 0 / FAILED 0
bash scripts/check.sh --smoke -> PASSED 47 / UNCONFIGURED 0 / FAILED 0
```
五条 smoke 逐字节一致，dev development profile 行为未变。

自部署演练栈（WP65 runtime）：
```text
gateway 镜像重建：第一次 docker compose build gateway 出现一次瞬时 go-build exit 1；
  单独重试成功，dmesg/journal 无 OOM；镜像 strings 含 identity_changed=1。
gateway 重新创建后：
  POST /v1/auth/login -> 200；GET /admin/v1/session -> 200（tenant=merchant shop=main-shop）
  POST /admin/v1/merchant/imports -> 201（batch 3, item_count=1）
  POST /admin/v1/merchant/imports/3/confirm -> 202
    {"batch_id":"3","command_id":"2","job_id":"2","status":"confirmed"}
  GET /admin/v1/merchant/reviews -> wp66-shipping version 2 pending
  POST /admin/v1/merchant/versions/2/review -> 200 approved
  POST /admin/v1/merchant/versions/2/publish -> 200 active（fencing_epoch=2）
```

顾客问答（真实 admin 导入的 shipping 资料）：
```text
assistant text: 本店通常在付款后48小时内发货。若遇节假日，发货时间可能顺延，具体请以订单页面显示的信息为准。
citations=1 (document_id=wp66-shipping, version_id=2, evidence_id=db-chunk-4)
can_copy=true, blocked_reason=""
completed behavior=REPLY_BEHAVIOR_ANSWER
```

未覆盖：OIDC/Keycloak 与 OSS 剖面未重跑；自部署镜像重建的一次性构建失败未定位到
确定原因（重试成功，无 OOM 证据）；未跑浏览器 UI；未改 CONTRACTS_* 棘轮。
