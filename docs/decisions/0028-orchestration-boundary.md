# 0028 编排边界：Go 用例层与 LangGraph 的职责划分

- 状态：提议
- 日期：2026-10-30
- 关联：[0025](0025-code-level-rules.md)（代码级规则）、[0029](0029-sparse-retrieval.md)（稀疏检索）、[contracts](../contracts.md)

## 背景

检索-生成段由 Python 的 LangGraph 编排，前后由 Go 负责用例语义。历史上两处都尝试过决定"要不要停下来问用户",
后果是同一件事有两个出处：Go 的 `decideClarification` 与 Python 的 `CLARIFICATION_WORKFLOW` 都影响澄清行为,
改一处忘一处就会出现文案不一致或该停不停。

## 决定

### 按产出物划分（不是按文件、按阶段）

| 角色 | 拥有 | 不得拥有 |
| --- | --- | --- |
| **Go 用例层** | 授权、幂等、状态机、事件顺序、落库、失败分类、**终态**（ASKING / DONE / FAILED / CANCELLED） | 段内的检索策略与提示组装 |
| **Python（LangGraph）** | intent 三分类、clarification 文案候选、confidence、检索多跳、事实工具选择、提示组装 | 终态、写库、SSE 帧、读 scope 之外的租户数据 |

**判据**：需要问"能不能给顾客看""能不能发"的归 Go；只需要问"还要不要再检索一次""要不要再问一句"的归 Python。

### `CLARIFICATION_WORKFLOW` 的真实作用（容易误读）

`python/app/graph/workflow.py:48` 的这条正则**不产出终态**。它影响的是段内编排：

- schema 变体选择（`workflow.py:455`）；
- 证据排序/取舍（`workflow.py:464`）；
- 是否先规划再检索（`workflow.py:557`、`:640`）。

它是"要不要换一种问法/再检索一轮"的判断，属**段内编排**，不是"这一轮要不要停下来问用户"。
终态由 Go 的 `decideClarification` 唯一决定；`CLARIFICATION_WORKFLOW` 一旦被搬去做终态判断，就是越界。

### 与既有决策的关系

- **0025 R1（同一件事只写在一处）**：澄清判定的唯一出处是 Go 的 `decideClarification`；
  Python 只提供 `intent` 与 `clarification` 文案候选，两者在契约表里各占一行。
- **0029（稀疏检索）**：稀疏/稠密的切换属"段内检索策略"，归 Python；但它写入的数据形状
  （`chunk.sparse_embedding` 的 tokenizer 版本、维度、空值语义）是跨进程契约，必须进契约表。

## 后果

- 契约表新增/保持两行：`clarification 信号`（Python intent → Go 判定）、`事实工具选择`（Python → Go 校验）。
- 任何"把终态判断挪进 Python"的改动都要先改本记录；反之亦然。

## 备选方案

| 方案 | 链接 | 不采用理由 |
| --- | --- | --- |
| 由 Python 决定终态、Go 只落库 | [LangGraph](https://langchain-ai.github.io/langgraph/) | 违反 0004 不变量（权限与终态只能由 Go 决定）；SSE 帧与写库会跨进程度耦合，失败分类无法统一 |
| 全部判定收回 Go，Python 只做纯生成 | [0025](0025-code-level-rules.md) | 检索多跳需要每轮看到证据再决定，收回 Go 会把检索策略硬编码进网关，失去图编排的意义 |
| 两边都保留判定，靠测试对齐 | — | 正是当前问题的成因：两处文案与两处条件，实测已经出现不一致（本轮追加的正则单测证明了"那个怎么样"并不命中正则） |
