# 交接简报

## 当前进度

- 2026-10-14 完成主流 RAG 官方工程调研，建议优先做检索阶段诊断、解析审核/原文定位，再评测父子段落、过滤后召回、增量缓存和来源同步。详细资料在私有 `.local/dev-docs/research/20261014-mainstream-rag.md`；本轮仅调研，未改变实现、模型配置或验收结果。
- 活动树 `/opt/ecommerce-rag-v2`、`refactor/v2-rebuild`；按文件本地提交，不推送/合并。用户已确认control和generation免费额度限制解除，沿用原四模型、1024维、原共享账本及60RPM；百炼付费不限额度授权有效。代理复核是 `reviewer_kind=agent`、`human_review_completed=false`。
- 冻结60案例、37文档、答案点/alignment/evidence与边界契约保持不变。契约hash `33d3a9b2f8006a6134f4698ccd3d70a10a4c5d61119bd16b7a6ac0895376eb6e`。原历史和失败报告均保留。
- 合成V2证据边界阶段现PASS：历史r40完整53/53，高风险42/42、普通11/11、必需证据与适用引用49/49，4个无引用澄清单列；本次current r45十二例、r46八补充场景实际执行和逐案代理复核均PASS。r47对23个DONE/ASKING及1个CANCELLED实际四进程重启，HTTP/SSE hash不变、连续序列/回放正确、恢复Provider增量0。阶段汇总 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-stage-acceptance-r48-20261014.json`；只批准合成资料证据边界，不是生产外部服务。
- 原答案点门禁独立NOT_PASSED：r40 44/53、9遗漏、0矛盾/失真。遗漏012/013/014/016/025/026/029/031/042保留，不能用边界摘要/澄清改写原真值。原报告 `v2-original-gate-paid-full-r40-20261013.json`。r41 control403十二个FAILED及r42失败/取消恢复证据均不改写。
- r40同批七安全案例和四项撤权/旧结果回放PASS、Provider增量0；r38历史八补充场景PASS，r39 syn004三次连续入口语义PASS。原syn004截断根因仍NOT_PROVEN。当前轨UTC业务日2026-10-13与历史业务日2026-09-29分开；本窗口当地时间跨到2026-10-14，身份/token/权限/撤权一直用真实时钟。
- 已完成本地Keycloak26.8.1 OIDC bearer协议集成。固定官方镜像及发行Apache-2.0/third-party notice hash核验；合成账号自定义身份属性仅ADMIN_EDIT。Go每请求私有session、映射既有DB身份、签名/到期/introspection，数据库角色/店铺/revision匹配；scope回验关联token活跃性。22项实际本地入口检查PASS，含Go→gRPC→Python、logout/过期/撤权/权限变化/旧结果拒绝；真实暂停身份服务时503失败关闭。报告 `keycloak-protocol-r2-20261014.json`、`keycloak-outage-r3-20261014.json`、聚合 `keycloak-stage-acceptance-r4-20261014.json`；全部real_service_acceptance=false。浏览器登录/Cookie/CSRF与生产身份未实现。
- 代码提交：3021074（JWT/JWKS/introspection）、f350e2e（请求私有身份/Go权限/scope回验）、0ef60ab（本地服务锁/来源准入证明/评测驱动）。HTTP/SSE业务字段、Proto、DDL未改。协议验收默认离线AI；原真实百炼边界证据与此分开。
- 最新基础检查PASS：`bash scripts/check.sh`含Python394passed/27skipped、Go test/vet、Proto、双前端19+5测试及构建、facts故障矩阵；eval284passed，Go race/身份隔离、supervisor19passed，ruff/文档/隐私/差异检查PASS。默认27个DB跳过沿用先前隔离库30项和Go权限/对账证明；迁移25项checksum、非super/noBYPASSRLS、vector0.8.6/pg_trgm先前核验。实际浏览器恢复6、权限3、生命周期13及独立DB范围仍见 `v2-boundary-offline-verification-20261013.json`，不当作新增OIDC浏览器登录证据。
- 独立验收库 `ecr_v2_dev_20261013_temporal` 保留37文档生命周期：36发布、1unclassified不可见，报告 `v2-live-lifecycle-temporal-20261013.json`。本轮授权fixture已恢复，仅修改synthetic身份，未SQL写最终业务表。
- Medusa2.21.2的npm SRI、实际发行声明及source LICENSE/Enterprise范围已核验：默认启动调用明确列入Enterprise目录的policiesLoader，MIT非Enterprise声明不能证明运行排除，未部署。2.18.0作为具体纯MIT候选已核验包内声明、source许可证与SRI，报告 `medusa-2-18-candidate-audit-20261014.json`，未选定、未部署；已发异步版本问题，等待用户决定。该候选npm包缺LICENSE文件，重分发须保留对应source MIT原文。

## 下一步任务

1. 等待用户对Medusa版本选择的回复：改用核验后的2.18.0，或保留2.21.2并继续核实默认运行许可；不能擅自改变明确固定版本或开启/绕过Enterprise许可机制。版本确认后经Go facts边界做REST集成、范围/归属/故障矩阵及本地测试记录验收。
2. Medusa通过后完成Open Food Facts字段/隐私/用途准入再获取固定revision，排除图片；同一批准字段分别经Medusa目录API和现有RAG摄取/审核/发布流程，记录源hash/转换版本，保留独立数据轨。原始公开数据尚未下载。
3. 保留原答案点缺口；后续功能回归不能宣称M2/M3、生产OIDC、真实商家或生产就绪。浏览器登录尚未实现，现有双前端development profile继续独立回归。

## 遗留问题

- 总体计划进行中。质量边界与本地Keycloak协议通过；Medusa默认运行许可范围/固定版本选择待解除，OFF字段与来源准入未完成。UCI许可差异、Amazon/社区、中文FAQ政策缺口保持。
- V1代码/数据库不删除。备份恢复及只读封存尚未执行，七天保留期未开始；旧树未提交改动不是回滚基线。
- `/opt/ecommerce-rag-ops` 是职责约定，本机无该目录；生产多实例、灾备、容量及生产身份/商家API未验收。LangGraph pending-deprecation warning保留。
- 运行与停止方法见私有接口/运维专题及README；本轮日志与受限原文均仓外或.local，凭据不进入Git。窗口记录 `.local/dev-docs/archive/20261013-control解除与当前轨验收.md`。
