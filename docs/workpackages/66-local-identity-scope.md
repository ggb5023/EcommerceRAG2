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

## 汇报

五节格式；附根因行号、修复 diff、演练栈完整 curl 序列输出、smoke 输出。
