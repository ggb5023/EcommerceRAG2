# 0011 产物丢失后以 v2 重新绑定冻结契约

- 状态：已采纳
- 日期：2026-10-26

## 背景

`evidence-boundary-contract.v1.json` 与 `quality-scenarios.v1.json` 绑定的是 2026-10 服务器注销前生成的
原始产物哈希。产物随机器丢失后，v1 的 cases/corpus/alignment/evidence 四项绑定无法从仓库重建：
按源 manifest 重建出的文件哈希必然不同。继续让 v1 指向不可恢复的哈希，会让冻结输入检查只在
“语料不存在”时通过，不能作为验收依据。

## 决定

版本前滚，不就地修改 v1：

- 新增 `eval/evidence-boundary-contract.v2.json` 与 `eval/quality-scenarios.v2.json`，
  绑定本次重建产物（revision cases、documents、alignment、evidence sidecar 与 v2 scenarios 自身）；
- thresholds、checks、excluded_case_ids、behaviors 以及每例的 risk/requirements 等判定语义保持不变；
  仅重绑与产物内容直接相关的哈希、chunk id/version 与 scenario 哈希；
- 代码默认值切到 v2（`evidence_boundary.DEFAULT_CONTRACT`、`run_v2_quality_e2e.DEFAULT_SCENARIOS`、
  `run_bailian_batch.DEFAULT_SCENARIOS`），显式 `--contract` / `--scenarios` 覆盖仍然可用；
- v1 两个文件字节保持不变，测试固定其 sha256，防止被静默改写。

## 后果

- 后续 baseline 以 v2 绑定为准；v1 仅作为历史记录保留。
- 由于原始产物已丢失，v2 是重建后的**首个基线**，P1 结论只能表述为“建立首个基线”，
  不能表述为“不低于原有基线”——后者需要原始产物或可比的独立运行，当前不具备。
- 若将来再次重建，继续以新版本前滚，不修改既有 v2。
