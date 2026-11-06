# 0038 上游证据按 chunk id 去重

- 状态：已采纳（2026-11-06 提议，同日采纳并实现）
- 日期：2026-11-06
- 工作包：[68](../workpackages/68-decision-batch.md)
- 关联：[0028](0028-orchestration-boundary.md)、[contracts](../contracts.md)

## 问题

上游 `Evidence` 可能返回重复 id。当前不去重会重复写 `evidence_snapshot`、重复计数
citation，并让 `validateEvidenceRanks` 因“重复 id 或重复 rank”把整轮判成
`invalid_evidence_labels`（fail-closed，但错误地失败）。refactor-input #10 要求
先确认去重键再实现。

## 决定

在 Go 消费检索结果的边界（`runRetrievalStage` → 授权/过滤之前）按 `Evidence.id`
去重：

- 保留第一次出现的对象，忽略后续同 id 项；
- 保持上游流式顺序，不排序、不合并内容；
- 对保留列表重排 `Rank` 为连续的 1..N，`citation_index` 与引用计数都按去重后列表；
- 重复项不是“被拒绝证据”，不写入 `rejected`；只有授权、披露或资格规则淘汰的项
  才进 `rejected`。

## 为什么按 id 而不是 (id, version)

本库 chunk id 全局唯一且版本不可变：同一 id 不可能合法地指向两个不同版本。若按
`(id, version)` 去重，同 id 的重复项会被保留成两条引用，重复计数的问题仍在；而
“同 id 不同 version”是上游契约破坏，应视为重复而不是两条合法证据。按 id 去重也能
在授权查询前减少无谓的数据库校验。

## 状态与失败模式

- 去重发生在 `authorizeEvidence` 之前，因此重复项不会重复查 ACL/版本；
- 去重后仍执行原有授权、披露类、customer eligible 与 rank 校验；
- nil 项和空 id 不吞掉，留给 `FilterEvidence` 的契约检查保持 fail-closed；
- 去重不会改变 accept/reject 的规则，只改变参与规则的候选集合。

## 取舍

| 方案 | 不采用理由 |
| --- | --- |
| 在 Python 检索层去重 | Go 是授权和终态边界；上游仍可绕过或有第二实现 |
| 只在持久化时去重 | 授权计数和 citation 计数已经重复，太晚 |
| 按 `(id, version)` | 不解决同 id 重复引用，且把契约破坏当成合法证据 |
| 排序后去重 | 会改变证据顺序，影响稳定引用和既有 smoke 输出 |

## 验证

- `go/internal/domain/evidence_test.go:TestDedupeEvidenceKeepsFirstAndReindexes`：
  3 条输入（a, b, a）→ 2 条，保留首个 a，rank 重排为 1,2。
- `TestDedupeEvidenceLeavesContractErrorsToFilter`：nil / 空 id 仍交给契约检查。
- 现有 `FilterEvidence` 测试与 `--smoke` 基线不变。
