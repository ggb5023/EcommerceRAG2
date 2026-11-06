# 0040 代理授权：merchant 只读、access 代申请并审批

- 状态：已采纳
- 日期：2026-11-06
- 工作包：[77](../workpackages/77-proxy-permission-administration.md)
- 关联：[0018](0018-m6-l2-review-suggestion.md)、[0039](0039-staff-suggestion-knowledge-draft.md)

## 1. 问题

当前仅 `access_admin` 能看账号列表、撤权与审批申请，但 `access_admin` 自己也只能
提交"自申请"等别人审批；`merchant_admin` 看不到员工授权、也无法新增/调整角色。
用户要求：merchant 只读员工权限并可提交申请；access 对账号与授权有增删查改能力，
且 access 不再提交自申请。

## 2. 决定

| 能力 | merchant_admin | access_admin |
| --- | --- | --- |
| 查看范围内账号与授权 | 是（只读） | 是（全量） |
| 新增员工账号 | 是（merchant_admin / knowledge_reviewer） | 是（另加 platform_observer） |
| 提交自己的权限申请 | 是 | **否**（403 `access_admin_self_request_forbidden`） |
| 代他人发起授权申请 | 否 | 是（`user_id` 为目标） |
| 审批申请 | 否 | 是 |
| 撤销授权 | 否 | 是 |

**代理流程复用现有链路，不新增 SECURITY DEFINER 函数、不新增迁移**：

1. access 调用 `POST /admin/v1/access/requests`，带 `user_id`（目标）；
   Go 校验目标 active、目标店铺与 actor 授权范围包含申请范围；
2. 服务端以 `requested_by=目标` 插入 pending 请求（审计 `permission.request.create`
   带 `proxy=true,target_user_id`）；
3. access 立即调用既有 `POST /admin/v1/access/requests/{id}/decision` 审批；
   `ecr_issue_admin_grant` 校验 actor 的 access_admin 授权、匹配 pending 请求、
   upsert `admin_role_grant` 并写 `permission.approve` 审计；
4. 第二步失败时 pending 请求保留，可在「权限申请」页重试。

`access_admin` 角色本身仍按 `install.md` 带外授予（避免在 UI 上自我复制平台权限）。

## 3. 边界与理由

- 权限判定仍在 Go + 既有 SQL 函数；merchant 只读接口不提供写能力，服务端也不放宽。
- 目标必须 active 且在 actor 的授权范围内；不能给自己代理申请；不能自审批
  （既有函数与触发器保证 `reviewed_by != requested_by`）。
- `platform_observer` 可被 access 代理授予；`merchant_admin` / `knowledge_reviewer`
  同为可代理角色。
- 前端隐藏与后端拒绝同时生效（access 的提交按钮隐藏，后端对 access 自申请返回 403）。

## 4. 备选方案

| 方案 | 不采用理由 |
| --- | --- |
| 修改 `ecr_issue_admin_grant` 支持直接授予 | 需要重写 SECURITY DEFINER 函数，超出本次风险预算 |
| access 继续自申请 | 用户明确要求 access 不提交申请，只做管理 |
| merchant 可撤权/授权 | 与用户「merchant 只读员工权限」冲突 |

## 5. 后果

- 账号列表接口对 merchant 只读开放（按店铺范围过滤）；撤权仍仅 access。
- 审批仅 access 执行；merchant 仍可看到申请列表但不能点批准（UI 隐藏 + 后端 403）。
- 审计保持两步：`permission.request.create`（含 proxy 元数据）与
  `permission.approve`（既有函数写入）。
