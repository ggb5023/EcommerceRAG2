# 68 决策批（refactor-input #15 / #9 / #10，各一小决策 + 实现）

- 状态：见 INDEX.md
- 依据：docs/refactor-input.md 对应条目；2026-11-06 主 agent 独立复审（③）
- 前置：WP67 已关

## 目标

三个待决问题各写一个决策记录（0036/0037/0038，只追加编号）并实现。每个决策
先写「提议」→ 主 agent 认可后转「已采纳」→ 同日实现。决策记录 ≤ 60 行。

## 0036 幂等键并入 batch id（#15）

- 现状：`"admin:<shop_id>:<manifest_sha256>"` 使同店同内容两批折叠成同一 job，
  批次与 job 不再一一对应，对账/撤销/审计都会踩到。
- 决定方向：幂等键改为 `"admin:<shop_id>:<batch_id>:<manifest_sha256>"`，批次与
  job 一一对应；**向后兼容**——提交时若旧格式键存在未完成 job 仍复用（避免同一
  批次重复解析与双写）。决策记录写明新键、兼容查找与测试锁定。

## 0037 撤回后同名重导入语义 + 错误映射（#9）

- 现状：①`IngestAuthorizationError` 与迁移不完整被统一映射成 503
  `ingestion_queue_failed`，与"临时排队失败"混淆；②active 版本被 revoke 后，
  同名再导入在 confirm 阶段失败。
- 决定方向：①错误按类型映射——授权类 403、状态冲突 409、临时失败 500/503，
  响应带 cause code（error-taxonomy 同步）；②撤回后同名重导入**允许**：
  同 logical_key 存在 document 时创建新版本，document 已删除则新建 document；
  决策记录写明状态机与 fencing 校验。

## 0038 上游证据去重（#10）

- 现状：上游 `Evidence` 可能返回重复 id，当前不去重，重复落库与重复引用。
- 决定方向：在 Go 消费处按 `id` 去重（保留首次出现，顺序稳定），引用计数与
  `citation_index` 按去重后计算；决策记录写明"按 id 还是按 (id, version)"
  的取舍与理由（本库 chunk id 全局唯一 → 按 id）。

## 实现与验证

- 每个决策：决策记录 + 实现 + 两侧/单侧测试 + 相关文档同步（contracts /
  error-taxonomy / metrics 视需要）；`docs/decisions/INDEX.md` 登记三行。
- 0037 的错误映射若改动管理端响应码，`docs/openapi.yaml` 与契约测试同步；
  前端对 409/403 的展示路径如有断言需同步。
- 五条 smoke 逐字节一致（turn/import 链路涉及 0037/0038 时重点回归）。

## 验收命令

```bash
bash scripts/check.sh            # 44 项 FAILED 0（decisions-index/doc-numbers 全绿）
cd go && go test ./internal/admin/ -run "Idempoten|Reimport|Dedupe|Import" -v
cd python && uv run --frozen pytest -q
bash scripts/check.sh --smoke    # 47 项五条基线逐字节一致
```

## 不要做

- 不改冻结评测集与棘轮；不重采 smoke 基线；不加迁移（0037 语义若需要状态值
  必须走 0057 新迁移并先登记 ownership）；
- 决策记录不超 60 行、只追加编号；不 push；提交身份固定；不用 `git add .`。

## 汇报

五节格式；三个决策记录全文、每项实现 diff 行号、负向验证（旧幂等键兼容、
重复证据去重后计数、撤回重导入的新版本链路）。

## 执行记录（2026-11-06）

- **0036**：`ingestIdempotencyKey(shopID, batchID, manifest)` 生成
  `admin:<shop>:<batch>:<sha256>`；`queuePackage` 只为当前批次查询旧键，
  且 `UnfinishedJobByKey` 只认 `pending`、`running` 或 `awaiting_review`。直连
  `/sources/import` 无批次行，保留旧键。见 [0036](../decisions/0036-batch-scoped-ingest-idempotency.md)。
- **0037**：错误映射在 WP68 之前已落地（`queue_failure.go` + error-taxonomy），
  本包补 openapi 与决策记录；重导入根因是旧键命中已完成 job，已由 0036 修复。
  未新增状态值，0057 未使用。见 [0037](../decisions/0037-reimport-and-import-error-mapping.md)。
- **0038**：`domain.DedupeEvidence` 在 Go 消费处按 id 保留首次、稳定重排 rank；
  `runRetrievalStage` 在授权前调用。见 [0038](../decisions/0038-evidence-dedupe-by-chunk-id.md)。
- **负向验证**：`TestUnfinishedLegacyJobIsReusedWithoutSubmit` 与
  `TestCompletedLegacyJobDoesNotAbsorbANewBatch` 通过；`TestReimportAfterRevokeUsesANewBatchKey`
  通过；`test_reimport_after_revoke_creates_a_new_version` 在测试库通过；
  `TestDedupeEvidenceKeepsFirstAndReindexes` 以 3 条输入得到 2 条并重排 1,2。
