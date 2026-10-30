# 工程约束总表

> 权威等级：enforced / context 混合（逐行标注）　适用提交：4a625b1　最后核对：2026-10-30
> 用途：把散在六份域计划里的规则收敛成一张可查表。**每条规则必须回答"谁在强制"**。
> 标「口头约定」的行是当前没有机械检查的规则——它们是待补门禁的清单，不要当成已保障。

## 1. 数据与迁移

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 每张表只有一个写入方 | `check-ownership.py` | ✅ 已声明表数见 `docs/data-ownership.md` |
| 迁移只追加，不修改已执行文件 | `check-doc-baseline.py` | ✅ |
| 新表必须显式授权给所需角色 | 无 | ⚠️ 口头约定（0022 只有 `GRANT SELECT ON ALL TABLES` 模式） |
| 新环境必须能"从零建库" | 无 | ❌ 未实现（批次 3 §5.1 待做） |
| 备份必须验证可恢复 | 无 | ❌ 从未演练（`restore_verified: false`） |
| 数据归属变更需改 `data-ownership.md` | `check-ownership.py`（预算棘轮） | ✅ |
| 不得统计货币金额，只统计 token 与次数 | 无 | ⚠️ 口头约定（0021）；建议加 `check-no-currency.py` |

## 2. 运维与发布

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 改动轮次链路后必须跑四条 smoke | 人工 | ⚠️ 口头约定（`.local/` 基线比对） |
| 服务启动后日志必须有输出 | `check-service-logs.py` | ✅ |
| 容器日志必须限量轮转 | `compose.yml` 的 `max-size` 与 `max-file` | ✅ |
| 部署形态只能四选一（D1–D4） | 启动期 `validateDeploymentForm` | ✅ 单测覆盖 |
| 明文 HTTP 只能绑 loopback | 同上（`requireLoopback`） | ✅ |
| 镜像以 git-sha 打 tag，可回滚 | 无 | ⚠️ 部分（当前另有 `:selfhost` 别名） |
| 升级与回滚流程成文 | 无 | ❌ 未定义 |
| 合入 main 前在无 untracked 的干净目录跑一次 `check.sh` | 无 | ⚠️ 口头约定（补齐夹具时踩过） |

## 3. 安全与密钥

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 仓库内不得出现密钥 | `privacy_guard.py` | ✅ |
| 密钥不得进入日志与错误响应 | 部分：`queue_failure_test` 断言响应体无 DSN/SQL | ⚠️ 仅覆盖一处 |
| 租户供应商密钥用 AES-GCM，只存 last4 | `provider_crypto.go` | ✅ |
| 会话 Cookie 必须 `HttpOnly` + `SameSite` + HTTPS 下 `Secure` | 代码固定 | ✅ |
| 授权判定集中在一处 | 无 | ⚠️ 现分散在 `auth` / `admin` / `customer` / `localauth` |
| 密钥材料有清单与轮换流程 | 无 | ❌ 未实现 |
| 个人信息保留期有定义 | 无 | ❌ 未定义 |
| 不得新增 `SECURITY DEFINER` 函数 | 人工评审 | ⚠️ 口头约定 |

## 4. 可观测性

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 错误码有唯一出处且带 cause | `docs/error-taxonomy.md` + `check-contracts.py` | ✅ |
| 映射只读 gRPC 码，不读 message | `queue_failure` 单测（"忽略 message 文本"） | ✅ |
| 请求级关联 ID 贯穿 Go→Python | 无 | ❌ 未实现 |
| 关键指标可暴露（`/metrics`） | 无 | ❌ 未实现 |
| 日志结构统一（含 logger 名与级别） | Python `basicConfig`；Go 用默认 logger | ⚠️ 部分 |
| 审计写入口统一 | 无 | ⚠️ `auditAdmin` 与两处直写并存 |

## 5. 评测

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 冻结语料完整性 | `check-eval-corpus.py` | ✅ |
| 基线数字必须可复现 | 人工（要求附命令） | ⚠️ 口头约定 |
| provisional 与 approved 必须可区分 | 单个布尔 `real_service_acceptance` | ⚠️ 不足以区分 |
| 语料 provenance 有台账 | 无 | ❌ 未实现 |
| 评测成本与时长有计量 | 无 | ❌ 未实现（`usage_log` 已存在但评测不写） |

## 6. 治理

| 规则 | 强制方式 | 现状 |
| --- | --- | --- |
| 决策一文件一编号，只追加 | 人工 + `check-docs.py` | ✅ 26 份 |
| 决策编号不撞车、无漏号 | 无 | ⚠️ 口头约定（0028 曾漏建） |
| 文档不得重复陈述同一事实 | `check-docs-drift.py`（预算 5） | ⚠️ 计划降到 0 并升为 FAIL |
| 文档引用的路径必须存在 | 无 | ❌ 待建 `check-doc-paths.py` |
| 里程碑 tag 作为回滚点 | `git tag`（4 个） | ✅ |
| 分支合并后即删 | 人工 | ⚠️ 口头约定 |
| 依赖与漏洞有扫描节奏 | 无 | ❌ 未实现 |

## 7. 使用方式

- 新增一条规则 → 先在本表加一行，再写实现或明确标「口头约定」；
- 「口头约定」变成门禁时，同步更新"强制方式"列，**不要只改代码不改表**；
- 本表与 `docs/archive/*-plan.md` 的关系：那边是当时的分析过程与工作包细节，本表是当前仍然生效的规则摘要。
