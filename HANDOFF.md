# 交接简报

## 当前进度

- `refactor/v2-rebuild` 已拆分 Go 控制面，新增 Python 资料命令、artifact journal、独立 AI/摄取 RPC 和 worker 入口；隔离数据库权限与摄取故障测试已有证据。已提交 `8f7c03b`（四进程启动/就绪）、`34c3899`（pending 命令恢复/回执绑定）、`33d9106`（版本化商家 facts fake/HTTP 适配器）、`1882cf5`（facts 故障矩阵、V2 浏览器 profile/evidence 契约）和 `436b5c1`（进程级 facts fake matrix smoke）。能力去向见 `.local/dev-docs/docs/V1到V2能力迁移清单.md`。
- 已确认开发身份、本地商家 API fake、受控资料与真实模型集成规则；固定身份、版本化 fake、三种格式资料和 mock 查询已在新独立库走过真实 HTTP/worker/审核/发布/回退/撤销流程。稳定边界见 `FACTS.md`，详细要求见数据来源、模型和验收专章。真实百炼全链路尚未运行。

## 下一步任务

- 在可用的独立数据库上完成四进程资料生命周期、命令对账、权限版本和对象故障验收；当前受控配置的 PostgreSQL 未运行，本窗口未新增数据库业务证据。
- 重跑显式 facts fake Gateway runtime 的正常/空/401/403/429/5xx/超时/取消/字段缺失/partial/撤权矩阵，并补 ASKING/取消/撤权浏览器证据；前端验收需显式设置两个 Vite API proxy target 指向当前 V2 HTTP_ADDR。
- 离线链路通过后，在显式 profile 和新授权预算内验证真实模型。
- 新入口完整回归通过后，按迁移门槛退出旧兼容入口和专属测试。

## 遗留问题

- 真实四槽位百炼摄取→索引→查询→重排→生成尚未验收；在线费用/配额窗口没有本轮授权。fake 目前是独立 `go/cmd/facts-fake`，不由四进程启动器托管，需显式配置 `V2_FACTS_BASE_URL`。
- fake 适配器单元矩阵和 `scripts/facts-fake-matrix.sh` 进程级 smoke 已覆盖 401/403、空值、字段/版本无效、错误归属、429、5xx、超时、取消和部分失败；完整 Gateway 事实端到端矩阵、撤权和浏览器 ASKING/取消仍待数据库可用后复核。
- 当前共享 PostgreSQL/Redis 未运行；未擅自重启。四进程就绪曾通过，但数据库 turn 在本窗口返回 `rag_unavailable`，不能据此生成新的业务 PASS 证据。
- 新空库运行证据和浏览器证据均属于 mock/fixture 层，不升级为真实商家、真实身份、M2/M3 或生产就绪。
- 真实 OIDC、商家 API、模型/ANN 生产质量及长期双机部署留待后续验收；前两项缺失不阻塞当前重构。
