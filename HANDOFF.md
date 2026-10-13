# 交接简报

## 当前进度

- 活动工作树 `/opt/ecommerce-rag-v2`，分支 `refactor/v2-rebuild`。本地提交、不推送、不合并；V1 代码和数据库保留。代理语义复核受用户委托，必须记录 `reviewer_kind=agent`、`human_review_completed=false`，不得把要求“全部通过”写成结论。
- 已新增并冻结 `eval/evidence-boundary-contract.v1.json`（SHA-256 `33d3a9b2f8006a6134f4698ccd3d70a10a4c5d61119bd16b7a6ac0895376eb6e`）。60 案例、37 文档、原答案点、alignment、context-v2 evidence sidecar 和历史报告不变。新边界门禁独立于原答案点门禁，不能用合理澄清抹去原遗漏。
- 已提交 GenerateRequest.history_summary=6、Go 当前权限过滤的六条/8192 字节历史、Python 非可信指代处理；数据库检索向 Rerank 最多交付 20 个正文候选，生成最多 8 条。业务日期与身份/权限/配额真实时间分离。HTTP/SSE、浏览器和 DDL 无新增契约。
- 独立验收库 `ecr_v2_dev_20261013_temporal` 保留 37 文档实际生命周期证据：36 发布、1 个 unclassified 不可见。本库已经填充，不能再称为空库；报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-live-lifecycle-temporal-20261013.json`。
- 最新完整历史轨批次 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-history-full-r6-20261013.json` 执行 53 案例；其代理包在 `/var/lib/ecommerce-rag/quality-review/boundary-agent-full-r6-20261013/`，答案 capture 在 `/var/lib/ecommerce-rag/quality-review/boundary-history-full-r6-20261013/`。新门禁复查 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-gate-full-r6-recheck-20261013.json` 为 NOT_PASSED：47/53，高风险 39/42，普通 8/11，必需证据交付 47/49，适用引用 45/49；失败 syn-019/021/027/035/040/046。此批次早于最新修复，不能当作当前代码验收。
- 原答案点门禁独立报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-original-gate-full-r6-independent-20261013.json` 仍 NOT_PASSED：入口 39/53 通过，答案点支持 40、遗漏 12、失真 1、矛盾 0；组件已批准基线仍 PASS。新边界门禁没有改变这些原评分。
- 同批七安全案例和四项撤权/旧结果回放 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-safety-full-r6-20261013.json` 为 PASS，Provider 增量 0。syn-004 三次独立入口报告 `v2-boundary-syn004-{1,2,3}-20261013.json` 均 DONE；旧非确定性截断根因尚未证实，不能称已彻底解决。
- 已修复数据库 chunk 指纹与正文 SHA-256 混用的门禁误报：新批次记录 document/version/source/chunk 指纹和 snippet hash，缺持久证据不放行。历史 r6 不回填；另写只读事后完整性报告 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-full-r6-binding-audit-20261013.json`，64 条绑定核对、0 不一致。事后核验不替代新批次或语义通过，旧报告缺新证明字段的原因保留。
- 新补充驱动 `eval/run_evidence_boundary_diagnostics.py` 默认 PLAN，覆盖缺前文/多候选/错误指代、缺标准及补标识子轮次；真实父 turn、授权 fact_read、幂等和失败保留均有测试。补充场景尚未在线执行或语义复核，不能声称通过。
- generation 新增无日期摘要时效边界、已成功 facts 的观测优先与 mock 说明，缩短澄清正文；Provider 严格校验 enum/额外字段/长度/数组唯一性，并将 FreeTierOnly 分类为不重试的 allocation_quota、RPC RESOURCE_EXHAUSTED。原共享账本、四槽位、1024 维、control 256/generation 512 token 和 60 RPM 不变；历史账本不回写。
- 在线定向 r8/r9 已遇到 generation 的 403 AllocationQuota.FreeTierOnly：账户启用“仅使用免费额度”且额度耗尽。用户不限额度授权仍有效，但不能解除供应商账户设置；暂未收到解除确认。本接续未重复失败付费请求、未换模型或创建新账本。
- 验证：eval 273 passed；更新后 eval/Provider 定向 105 passed；基础 `bash scripts/check.sh` PASS（Python 357 passed/27 skipped、Go test/vet、Proto、双前端 19+5 测试和构建、facts fake 故障矩阵）。数据库跳过另用 `ecr_v2_test_20261013_checks` 补跑 Python 30 项 PASS、Go 管理授权/命令对账 PASS；四角色均非超级用户、无 BYPASSRLS，25 迁移 checksum、vector 0.8.6/pg_trgm 核验通过。
- 实际浏览器 PASS：重启恢复 6、撤权/权限版本 3、双前端资料生命周期 13。总脱敏证据 `/var/lib/ecommerce-rag/real-docs/reports/v2-boundary-offline-verification-20261013.json`，逐项本地路径由报告绑定。首次 lifecycle 因管理页空白超时、0 写入，保留失败报告；重启本轮 Vite 后新报告 13 项通过，不覆盖失败。
- 本轮评测四进程和临时 Vite 已停止；其他既有开发服务保留。启动入口 `bash scripts/v2-local-services.sh --env /path/to/protected-runtime.env`，事实 fake 须按已有配置启动。历史轨和当前轨使用各自受限配置，不在命令行传秘密。
- 接续功能提交：`08b7db3` 证据绑定及补充驱动、`100db40` 生成边界、`e9b2d84` schema 与账户配额分类。先前本计划提交自 `653b84b` 至 `2371f4b` 保留。评测说明、私有验收/来源/模型/接口专题及 FACTS 已同步；本轮记录 `.local/dev-docs/archive/20261013-证据边界门禁接续与账户阻塞.md`。

## 下一步任务

1. 解除百炼 generation 模型“仅使用免费额度”设置，继续原四槽位、参数、原共享账本和 60 RPM；先核对 current/history 受限配置与进程实现 hash，禁止换模型绕过或重复取最好答案。
2. 对 r6 六个真实失败及原 16 失败案例定向复测当前修复，保留每次报告；补充缺前文/多候选/错误指代和商品/订单标识后重新授权的实际子轮次。syn-004 的旧根因保持未证实。
3. 当前代码完整跑 53 条历史业务日期 2026-09-29 的 Go HTTP/SSE、逐例代理语义复核、双 sidecar 和与新批次绑定的七安全案例/旧结果回放；另跑当前时效的库存、价格、促销、公告、标准及报道限制。每个新报告不可覆盖历史。
4. 分别重跑原答案点与独立边界门禁。仅新门禁达到安全零严重错误、高风险全过、普通至少 90%、证据与适用引用至少 95% 才称“合成资料上的 V2 证据边界验收通过”；real_service_acceptance=false。
5. 放行后按 Keycloak → Medusa 2.21.2 → Open Food Facts 推进。许可证、字段/隐私和用途审核先于部署/下载/导入；失败门禁不进入服务集成。

## 遗留问题

- 当前门禁仍 NOT_PASSED。新代码完整在线回归、当前时效轨及补充场景未完成，受供应商账户设置阻塞；离线验证、合理缺口说明或历史安全 PASS 不能补成质量验收。
- Keycloak/Medusa 尚未部署集成，公开原始数据未下载。发行版许可、OFF 字段/隐私/用途、UCI 许可差异、Amazon/社区及中文 FAQ/政策缺口保持后续准入事项。
- V1 备份恢复与只读封存未执行；七天保留期尚未开始，不删除 V1 代码或数据库。旧代码未提交改动不是已验证回滚基线。
- /opt/ecommerce-rag-ops 仍是职责约定，开发主机无此目录。生产多实例、容量、灾备、真实身份/商家 API、M2/M3 均未验收；保留 LangGraph pending-deprecation warning。
