# 65 自部署端到端演练与 install.md 校正

- 状态：见 INDEX.md
- 依据：2026-11-06 主 agent 独立复审结论（①）；M5 验收"新机器 30 分钟跑通"自 P2 后
  未重验；OSS/Keycloak/intel 的部署事实尚未落盘
- 前置：无（纯部署面，不改运行时代码）

## 目标

1. 用 `docker/selfhost/compose.yml` 在干净 runtime 目录跑通端到端：
   db-init → migrate → bootstrap → gateway/ai 健康 → owner 设置 → 演示店铺导入
   → 顾客问答 → 转人工；
2. 把演练暴露的问题修掉（install.md / selfhost 脚本 / compose 的缺陷）；
3. 把已完成的真实验证结论落进 install.md：Keycloak realm 导入要求（KC26 三坑）、
   OSS 配置三坑、rag_v2_intel 角色、报告页 `ECR_REPORT_PATH`。

## 步骤

1. 重建 selfhost 镜像（python/go Dockerfile 从当前 main 构建，含 0056 迁移）。
2. 按 install.md 记录的方式初始化 runtime 目录（identity 先选 local：
   无 keycloak 依赖；oidc 剖面单独评估）。
3. `ECR_RUNTIME=<目录> docker compose up -d`，观察 db-init/migrate/bootstrap
   退出码与 gateway/ai 健康检查。
4. 按 install.md 步骤实操：读 SETUP_TOKEN 设置 owner 密码 → 登录管理台 API →
   导入演示店铺（demo-shop 脚本或最小资料）→ 顾客问答（answer + 引用）→
   转人工链路 → 报告页/巡检页只读验证。
5. 修演练问题；**不改任何历史迁移**；产物只动 install.md / development.md /
   selfhost 脚本 / compose / Dockerfile。
6. install.md 校正清单：
   - OIDC：realm 导入必须含——用户资料里声明自定义属性（tenant_id/user_id/role/
     shop_ids/permission_revision/revoked，否则 REST API 静默丢弃）；shop_ids 用
     jsonType=JSON 且属性值为 JSON 数组字符串；客户端加 audience mapper
     （否则 access token aud=account，go-oidc 拒绝）；DB 侧 tenant 必须 active 且
     permission_revision 与 token 一致；
   - OSS：oss.env 必须含 OSS_REGION、端点必须带 https:// 前缀、文件尾换行；
     桶授权策略模板（对象动作挂 bucket/*，桶级动作挂 bucket）；
   - intel：rag_v2_intel 角色由 selfhost-db-init 自动创建；V2_INTEL_ENABLED 默认 0；
   - 报告页：ECR_REPORT_PATH 指向 CLI 产出的 demo-report.json。

## 验收命令

```bash
ECR_RUNTIME=/tmp/ecr-selfhost-drill docker compose -f docker/selfhost/compose.yml up -d
docker compose -f docker/selfhost/compose.yml ps          # gateway/ai healthy
curl -sk https://127.0.0.1:8443/healthz                   # 200
# owner 设置 + 登录 + 导入 + 问答 + 转人工（curl 序列，报告贴真实输出）
bash scripts/check.sh                                      # 44 项 FAILED 0
```

## 不要做

- 不改历史迁移；不把演练产物（runtime/密码/日志）提交进仓库；
- 不动 answer/intel 运行时代码（发现问题只修部署面，运行时代码缺陷记录后另行处理）；
- 不在 dev 栈上演练（自部署栈独立，避免污染 dev 数据与 smoke 基线）；
- 不改 CONTRACTS_* 棘轮。

## 演练结果（2026-11-06）

自部署栈独立于 dev：`docker compose ... ps` 显示 postgres/ai/gateway healthy；
`db-init`/`migrate`/`bootstrap` 均 `Exited (0)`；`/healthz` HTTP 200。

发现的部署缺陷并修复：Go 最终镜像原为 `scratch`，只带 CA 证书，没有
`/usr/share/zoneinfo`；`time.LoadLocation("Asia/Shanghai")` 失败，导致
review queue 503、导入 confirm 503 `confirm_queue_failed`。已在
`docker/selfhost/go.Dockerfile` 增加
`COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo`；重建 gateway 镜像后
`/admin/v1/merchant/reviews` 恢复 200，并能看到导入的 pending 版本。

owner 与顾客链路：
- JSON 登录 200；`/admin/v1/session` 200，tenant=`merchant`、shop=`main-shop`；
- 顾客 `shop_slug` 必须用 `public_slug=my-shop`，不是 `shop_id=main-shop`；
- 无资料时顾客链路：session 201 → conversation 201 → message 202 →
  SSE `message,state,message`，state=WAITING_HUMAN，reason=knowledge_missing，
  0 citations（转人工兜底生效）。

导入链路：
- 最小 markdown 包 precheck 通过，`POST /admin/v1/merchant/imports` 201，
  preview 200（`precheck_result=[]`）；
- tzdata 修复后，confirm 不再是 503，但返回 **403 `ingest_not_authorized`**；
  admin review 同样返回 **403 `command_failed`**。这是运行时代码缺陷，不在本包修：
  本地身份 `localIdentityAdapter.Resolve` 返回 nil identity check，
  `scopeIdentities` 校验已启用但 `RegisterScopeIdentity` 为 nil，ingest scope 未注册，
  Go authority 拒绝该 scope。已按约定记录，另开运行时代码包。
- 为继续验证后续检索/回答路径，用 AI 容器内直接 `IngestDatabase.submit` 诊断提交
  （job id=1，awaiting_review），再用 SQL 将 document 4 / version 1 发布为
  active+approved+external_allowed（这是演练工作区，不是产品发布路径）。随后顾客
  问答：assistant 文本“本店支持7天无理由退换货…”，citations=1，
  `can_copy=true`，`blocked_reason=""`，completed behavior=ANSWER。产品 admin
  导入/审核/发布路径仍被上述运行时缺陷阻塞。

只读页面：
- `GET /admin/v1/report`（selfhost 未设置 ECR_REPORT_PATH）→ 404
  `report_unavailable`，不是 500；
- `GET /admin/v1/inspections?status=open` → 200 `{"items":[]}`。

`install.md` 已补：
- Keycloak realm 导入四要点（自定义属性声明、shop_ids JSON、audience mapper、
  DB tenant/revision），`docs/install.md:79`；
- OSS 三坑与 bucket 策略模板，`docs/install.md:88`；
- `rag_v2_intel` 自动创建、`V2_INTEL_ENABLED` 默认 0、`ECR_REPORT_PATH` 404 语义，
  `docs/install.md:110`；
- `shop_slug=public_slug`、管理写接口的 Origin+CSRF，`docs/install.md:210`；
- local profile 导入/审核/发布运行时缺陷的已知限制，`docs/install.md:224`。
- 自部署 DB 已验证 `rag_v2_intel` LOGIN 且 `market_observation` 有
  SELECT/INSERT/UPDATE GRANT。

门禁：`bash scripts/check.sh` PASSED 44 / UNCONFIGURED 0 / FAILED 0。

未覆盖：OIDC/Keycloak 剖面未实跑；OSS 归档未实跑；浏览器 UI 未跑；远端 Actions
未跑；未复现 compose build 的一次性 gateway 构建失败（重建成功）；admin 导入/审核/
发布被运行时缺陷阻塞，当前用诊断工作区完成后续 Q&A 验证。
