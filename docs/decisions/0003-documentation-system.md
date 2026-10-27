# 0003 文档体系：少而清楚

日期：2026-10-15　状态：已采纳

## 背景

原文档体系有 AGENTS、FACTS、HANDOFF、README、architecture 等文件，同一条架构规则在四处重复；HANDOFF 塞满报告路径和通过率。私有目录 `.local/dev-docs` 有 19 篇专题、3700 多个快照，每个开发窗口还要新建归档。文档维护的成本超过了它的价值，也没有起到防止方向跑偏的作用。

## 决定

| 文件 | 职责 |
| --- | --- |
| `README.md` | 给商家和访客：是什么、快速开始、文档导航 |
| `docs/product.md` | 为谁做、做什么、不做什么；防跑偏判据 |
| `docs/architecture.md` | 目标架构、设计约束、当前差距 |
| `ROADMAP.md` | 阶段、验收标准、当前任务；进度的唯一来源 |
| `docs/decisions/` | 重大取舍，每篇一事，只追加 |
| `docs/development.md` | 环境、运行、测试、提交 |
| `AGENTS.md` | AI 助手和贡献者的工作规则 |
| `docs/install.md` 等 | 商家操作手册 |

- 删除 `FACTS.md`、`HANDOFF.md`，取消每窗口归档。
- `.local/dev-docs` 打包归档，不再维护，日常开发不读取。
- 文档不写过程流水、报告路径和测试数字；运行证据留在仓库外。
- 一条规则只写在一个地方，其他地方链接过去。

## 决策一览

文件名固定为 `NNNN-短横线小写标题.md`，编号从 0001 递增、不复用；被取代的记录不删除，在原文顶部标注取代它的编号。新增记录时在下方补一行。

| 编号 | 主题 | 状态 |
| --- | --- | --- |
| [0001](0001-product-positioning.md) | 产品定位：电商客服知识引擎，首版只做辅助 | 已采纳 |
| [0002](0002-keep-go-python.md) | 保留 Go + Python 双语言，不合并技术栈 | 已采纳 |
| [0003](0003-documentation-system.md) | 文档体系：少而清楚，一条规则只写一处 | 已采纳 |
| [0004](0004-retained-invariants.md) | 重构中必须保留的 11 条设计约束 | 已采纳 |
| [0005](0005-copyable-reply-policy.md) | 可复制回复的边界：只给建议，不代发 | 已采纳 |
| [0006](0006-provider-usage.md) | 模型用量与共享预算账本 | 已采纳 |
| [0007](0007-offline-document-parsing.md) | 文档解析能力保持离线、不接入默认摄取链路 | 已采纳 |
| [0008](0008-local-accounts-and-session-revocation.md) | 本地账号：argon2id、可吊销会话、Keycloak 降为可选 | 提议 |
| [0009](0009-customer-cross-device.md) | 顾客跨设备历史：会话凭据码 vs 邮箱注册（仅比较，未实现） | 提议 |
| [0010](0010-keep-the-revision-build-pipeline.md) | 保留 revision 构建流水线：可重建、不恢复公开数据轨 | 已采纳 |
| [0011](0011-rebind-frozen-contracts-after-artifact-loss.md) | 产物丢失后以 v2 重新绑定冻结契约，thresholds 不变 | 已采纳 |
| [0012](0012-agent-delegated-evaluation.md) | 评测评审由 agent 代理：协议、反向抽样与 attestation | 已采纳 |
| [0013](0013-merchant-delegated-agent-review.md) | 商家侧 agent 代理审核 L1/L2/L3 与数据出域边界（仅提议） | 提议 |
| [0014](0014-split-gate-reasons.md) | 质量门区分 failed 与 not_measured | 已采纳 |
| [0015](0015-agent-review-owns-quality-in-development.md) | 开发阶段由 agent 评审承担质量门槛 | 已采纳 |
| [0016](0016-content-inspection.md) | 内容巡检（L3）设计草案 | 提议 |
| [0017](0017-m6-l1-import-precheck.md) | M6-L1 导入预检：数据模型与接口 | 提议 |
| [0018](0018-m6-l2-review-suggestion.md) | M6-L2 审核建议：判据与接口 | 提议 |
| [0019](0019-tenant-model-providers.md) | 商家自选模型供应商 | 提议 |
| [0020](0020-ui-only-acceptance.md) | 仅 WebUI 商家验收标准 | 已采纳 |

## 后果

- 原 FACTS 中仍然有效的结论已并入 architecture 和 [0004](0004-retained-invariants.md)。
- `scripts/check-doc-baseline.py` 改为检查新体系。
| [0021](0021-usage-log-instead-of-budget.md) | 用 token 用量替代货币账本 | 已采纳 |
