# 交接简报

## 当前进度

- 产品定位已改为面向商家的开源电商客服 RAG 系统。本轮首个里程碑“单商家自部署知识客服闭环”通过独立环境验收；保持 `real_service_acceptance=false`。Medusa、Open Food Facts 双轨、实时订单/库存工具、平台专用适配器、API Key、独立检索 API、多实例暂缓，不能沿用旧集成顺序推进。
- 活动树 `/opt/ecommerce-rag-v2`、分支 `refactor/v2-rebuild`。已有未提交的 AGENTS、README、公开架构、eval README 和公开来源清单改动保持原样，不替其他任务提交；逐文件本地提交，不推送/合并。
- 引用沿用实际 Evidence.rank，过滤后允许间隔；生成主回答及限制引用绑定实际片段。Proto 增加兼容客户主体、执行 epoch、ANSWER/CLARIFY/HANDOFF 和授权回验服务。Go 检查发布资格，客户只见获准最终消息；员工旧 customer_reply/can_copy 不全局放开。
- 客户访客 Cookie 与员工身份分离，数据库只保存凭据摘要；客户接口具备 Origin/CSRF、幂等、限流、授权来源及回放过滤。Go 持久化 AI/WAITING_HUMAN/HUMAN/CLOSED、原子领取、120 秒 lease、epoch、人工消息和事件；退出、撤权、过期及重启重新排队，迟到 AI 和重复发送被拒绝。
- 商家管理台、客户页和客服工作台已接通。DOCX、Markdown、CSV、FAQ 实际摄取、预览、审核、发布及撤销/回退通过；界面生成 manifest。Compose 提供独立 PostgreSQL、Redis、正式启动 Keycloak、Go、三个 Python 进程及 Web；授权回验经 mTLS gRPC，应用不执行 DDL。新增 checksum 迁移 0026/0027，历史迁移不改。
- 本地 Keycloak 26.8.1 bearer 与双前端浏览器登录分别验收。员工 confidential 授权码/PKCE、Go 令牌托管、Cookie/CSRF、权限变化和撤权、刷新、退出及重启重新登录已实现；自部署固定 subject 映射后由 Go 数据库提供权限。生产身份未验收。
- 汇总脱敏验收报告 `/var/lib/ecommerce-rag/customer-milestone-20261014/acceptance/milestone-acceptance-r1.json` 绑定 12 份分项报告、4 份验证日志及运行状态；原答案点门禁的未通过结果单列，不改写执行或历史报告。
- 最新客户 Compose 批次执行 15/15 并逐例代理语义复核：高风险 8/8、普通 7/7、必需证据 10/10、适用引用 10/10、严重安全错误 0。一次澄清后仍缺信息时转人工，5 条最终进入队列。复核绑定实际 capture/引用/hash，`reviewer_kind=agent`、`human_review_completed=false`、`delegation_authorization=explicit_user_instruction`；报告位于 `/var/lib/ecommerce-rag/customer-milestone-20261014/acceptance/quality-compose-r4.json`，原始答案和自由文本说明仅仓外受限保存。
- 最新员工 53 条 HTTP/SSE 回归 r7：53/53 正常终态，独立证据边界 PASS，高风险 42/42、普通 11/11、证据及适用引用 49/49。同批七安全、撤权、旧结果回放均 PASS，Provider 请求增量 0；当前时效 r8 十条逐例复核通过。报告前缀 `/var/lib/ecommerce-rag/real-docs/reports/customer-staff-`，冻结 60 案例、37 文档、答案点、alignment、sidecar、契约及历史失败报告均保留。
- 原答案点门禁仍独立 `NOT_PASSED`：44/53 支持、9 遗漏、0 矛盾/失真。遗漏 syn-012/013/014/016/025/026/029/031/042；不能把正确说明证据缺口或新客户闭环通过当成原答案点支持。报告 `customer-staff-original-gate-20261014-r7.json`。
- 独立 Compose 浏览器 r3、控制权 r1、来源 r1 PASS：两客服竞争唯一领取、人工回复实际送达、访客与领取刷新、过期 epoch、SSE 回放、撤销和回退、Go 重启恢复通过。全新安装浏览器 `/var/lib/ecommerce-rag/merchant-fresh-20261014/acceptance/browser-r1/browser-report.json` PASS；隔离备份恢复和原访客 Cookie/消息、Keycloak 员工重新登录证明在 `/var/lib/ecommerce-rag/customer-restored-20261014-r2/restore-evidence.json` 与 `browser-r1/report.json`，不自动清零或复制开发预算。
- 最终检查：`bash scripts/check.sh` PASS，Python 415 passed/27 skipped、Go test/vet、Proto lint/build/breaking、前端 22+10 测试与构建、facts fake 故障矩阵通过；eval 304 passed。显式隔离库 Python 摄取/检索 33 passed、Go 客户生命周期 race 与管理权限/命令对账通过，覆盖默认跳过之外的真实 DB 证据。文档、隐私、差异检查通过；日志 `customer-milestone-final-check-20261014.log`、`customer-milestone-final-python-db-20261014.log`、`customer-milestone-final-db-20261014.log`、`customer-milestone-final-admin-db-r2-20261014.log` 留仓外。
- 真实百炼沿用原四槽位、1024 维、共享账本及 60 RPM；不限额度付费授权有效，仍记录费用和每次尝试。Provider 可重试故障最多一次，共用原 deadline，首轮保留后续时间；非法结构、截断、授权和 TLS 失败不重试。配送能力限制字段保持原 80 字硬上限，旧失败结果不覆盖。
- 用户于 2026-10-14 明确授权删除确认不用的旧数据库，覆盖此前七天保留安排。已复核旧 V1 库 `rag` 无业务表、无连接、封存备份 hash 正确，并新增受限备份、再次隔离恢复验证后删除 `rag` 及两个已核实相同内容的临时恢复库。旧库专属备份 timer 已 disable/stop；未重启数据库实例、未强制断开连接、未修改共享角色。原封存报告和备份保留，最新退役报告 `/var/lib/ecommerce-rag/legacy-retirement-20261014/retirement-report.json` 为 PASS；不证明其他历史实例或库的数据已保存。V1 代码及未提交改动仍只读保留。
- 退役后远端全部保留评测库的数据/扩展/对象权限指纹及数据库设置前后相同，共享角色不变。V2 配置引用的库与历史评测库保留，不据零连接直接删除。独立客户 Compose 的旧访客 Cookie、已有会话回读、店铺与健康接口均 200，四进程 readiness 通过；报告 `legacy-retirement-20261014/active-service-verification.json`。运行中客户/身份库全表行 hash 有变化，该诊断保留但不作为逐行未变证明；本轮未调用模型，未重复完整质量或浏览器回归。
- 自有 MIT LICENSE、第三方声明及公开安装/知识/客服/备份/开发文档已提交。主要提交：67acd79 引用修复、97c22b1 客户 Proto、be32e69 客户与人工后端、bbad1bc 三界面、b11ef51 Compose/恢复、5ed59e8 公开文档、17c1bd1 限制引用、6fc8911 Provider 重试、bcafd37 长度契约、1c07daf 部署契约说明。过程在 `.local/dev-docs/archive/20261014-商家知识客服闭环实施.md`。

## 下一步任务

1. 商家试用按公开 `docs/install.md`、`knowledge.md`、`support.md`、`backup.md` 执行；选定获授权的实际店铺知识和试用账号，再冻结试用评测与运维范围。本轮合成通过不替代真实商家质量或生产验收。
2. 围绕知识质量继续做检索阶段诊断、解析审核和原文定位，再评测父子段落、过滤后召回、增量缓存和来源同步。已有官方调研在 `.local/dev-docs/research/20261014-mainstream-rag.md`；先确定范围，不擅自更换模型或添加平台依赖。
3. 保留原答案点缺口，后续功能回归同时运行原门禁、新证据边界和客户闭环契约；新答案逐例代理复核，不沿用旧答案批准。

## 遗留问题

- 生产 OIDC、生产商家 API、真实商家资料质量、正式 CA、公网镜像完整拉取/默认 Docker 构建、容量/灾备/多实例均未验收。本地验收采用文档所述锁定工具链构建与已核验本机导入镜像；官方 registry manifest 与本地镜像摘要分开记录，不能互称验证。
- Compose 当前八服务运行，关键服务 healthy，仅 loopback Web 18443 暴露。构建证据 `/var/lib/ecommerce-rag/selfhost-build-r5-20261014/build-evidence.json` 绑定实现提交 1c07daf；本轮后续只有交接文档变化。启动/检查：`docker compose --env-file /var/lib/ecommerce-rag/customer-milestone-20261014/compose.env -f docker/selfhost/compose.yml -f /var/lib/ecommerce-rag/customer-milestone-20261014/imported-ops.yml up -d`；检查改末尾为 `ps`，停机为 `stop`，不删除 volumes。测试域名必须配置信任和解析；入口 `/admin/overview`、`/shop/my-shop`、`/support`、`/chat`。
- 旧 `v2-test-20261013-checks` 库停在迁移 0025，新增店铺字段管理检查因此失败；原报告保留，在具备 0026/0027 的独立客户库复测已通过。原答案点 NOT_PASSED 和 LangGraph pending-deprecation warning 保留。默认检查的数据库跳过须与显式 DB、跨进程及浏览器证据分开记录，不能升级为生产通过。
- V1 库已按本轮明确授权退役，代码、受限备份与历史报告保留。旧受限配置和历史运维验证脚本仍可能指向已删除的 `rag`，不得再作为活动入口或重新启用旧库专属 backup timer；需要历史恢复时另建隔离库。DB 主机的 `/opt/ecommerce-rag-ops` 经现有内网 SSH 可访问，本机无该目录；公网 SSH/PG 此次仍超时。Medusa 审计阻断及 OFF/UCI/Amazon/社区来源缺口保留，当前不阻塞知识客服首版，也不授权下载或部署。
