# 0010 保留 revision 构建流水线

- 状态：已采纳
- 日期：2026-10-25

## 背景

M1 仓库整理时删除了 `build_aligned_fixture.py`、`build_alignment_proposal.py`、
`build_alignment_revision_proposal.py`、`review_alignment.py`、`approve_alignment.py` 与
`build_eval_revision.py`。冻结评测修订因此无法从源 manifest 重建；2026-10 服务器注销后，
既有产物随机器丢失，只剩评估结论，无法复现输入生成过程。

## 决定

恢复这六个脚本到 `eval/`，作为**可重建路径**：从受版本控制的源 manifest 生成
`documents.jsonl` 与 proposal，经人工/代理审查、显式批准后产出新的 revision。
生成结果不直接覆盖冻结评测集；修订必须产生新的 eval-set 版本与哈希，并保留审查记录。

## 边界

- 不恢复 `esci-lite`、`public-data-sources` 等公开数据轨文件，也不恢复对应的下载/同步链路。
- 不修改 `eval/synthetic_cases.jsonl`、既有 `review.json`、冻结阈值或真值。
- 脚本只做本地确定性构建，不访问网络、不调用模型；审查结论与审批仍是独立步骤。
- `human_review_completed` 在代理审查时保持 `false`，`reviewer_kind=agent` 需显式授权。

## 后果

- revision 可以从源文件重建；测试 `eval/test_rebuild_revision.py` 覆盖 fixture → proposal 最小链路。
- 公开数据轨仍不回到仓库；需要时另开决策记录。
