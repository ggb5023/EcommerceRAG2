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

## 后果

- 原 FACTS 中仍然有效的结论已并入 architecture 和 [0004](0004-retained-invariants.md)。
- `scripts/check-doc-baseline.py` 改为检查新体系。
