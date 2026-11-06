# 77 权限后端：merchant 只读 + access 代理授予

- 状态：见 INDEX.md
- 依据：决策 0040；用户要求 merchant 看员工权限并提交申请、access 增删查改授权
- 前置：WP76 已关

## 目标

1. `merchant_admin`：按范围只读账号/授权列表；可提交自己的权限申请；不可审批/撤权。
2. `access_admin`：可看全量列表、创建账号（含 platform_observer）、代他人发起
   授权申请并立即审批、撤销授权；**不能提交自己的申请**（403）。
3. 复用既有请求+审批 SQL 函数，**不新增 SECURITY DEFINER、不新增迁移**。

## 后端

1. `GET /admin/v1/access/accounts`：`access_admin` 或 `merchant_admin`（只读）。
2. `POST /admin/v1/staff`：`access_admin` 或 `merchant_admin`；角色集合按 actor：
   merchant → merchant_admin/knowledge_reviewer；access → 另加 platform_observer
   （access_admin 仍带外授予）。
3. `POST /admin/v1/access/requests`：新增可选 `user_id`（目标）。
   - access：必须代他人（目标 active、目标店铺与 actor 范围包含、不能 self），
     代目标插入 pending 请求，审计带 `proxy=true,target_user_id`；
   - 其他角色：只能 self；
   - access 自申请 → 403 `access_admin_self_request_forbidden`。
4. `POST /admin/v1/access/requests/{id}/decision`：仅 `access_admin`。
5. Store：`TargetAccount`（SQL 在 store_staff.go）；`accessGrantCovers` 纯函数单测。
6. 文档：决策 0040、INDEX、constraints（37）、error-taxonomy（新码）、install.md。
7. 测试：`proxy_test.go`；acceptance 集成测试新增 merchant 只读、access 自申请 403、
   merchant 审批 403、代理申请+审批发放 grant、代理审计。

## 验收命令

```bash
cd go && go build ./... && go vet ./...
# 带集成环境跑 acceptance 用例（环境变量写法见 docs/agent-entry.md §4）
bash scripts/check.sh
# 真实链路（dev）：
# access 登录：GET accounts 200；POST 自申请 403；代理申请+驳回 200/201
# merchant 登录：GET accounts 200；POST decision 403
```

## 不要做

- 不新增 SECURITY DEFINER / 迁移；不让 merchant 撤权或审批；
- 不放过 access 自申请；不写 `admin_role_grant`/`app_user` 直连 SQL；
- 不改 smoke 基线/棘轮。

## 汇报

五节格式；附集成测试与真实 curl 输出、决策/错误码变更。
