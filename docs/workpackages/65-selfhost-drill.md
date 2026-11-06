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
2. `python3 scripts/ops/selfhost.py init --runtime <新目录> --identity-profile local`
   （先 local：无 keycloak 依赖；oidc 剖面单独评估）。
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

## 汇报

五节格式；附 compose ps 输出、各一步容器的退出码、curl 序列真实输出、
install.md 校正 diff 行号、未覆盖路径（oidc 剖面/quay.io 可达性等）。
