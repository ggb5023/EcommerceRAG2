# 0039 员工建议回复采纳为知识草稿

- 状态：已采纳
- 日期：2026-11-06
- 工作包：[71](../workpackages/71-staff-suggestion-adoption-decision.md)
- 关联：[0005](0005-copyable-reply-policy.md)、[0018](0018-m6-l2-review-suggestion.md)、
  [0027](0027-document-source-ownership.md)、[0031](0031-m6-suggestion-reply.md)、
  [0032](0032-intelligence-domain-and-crawling.md)、[0033](0033-document-validity.md)

## 1. 问题

客服工作台生成的建议回复只有「复制」一个动作：复制成功仅写一条不含正文的审计
（`support.suggestion.copy`），建议执行还会被 50 轮保留策略清理。高质量回答无法
沉淀为可检索的知识；同时 `docs/knowledge.md` 与 `docs/support.md` 目前写明 FAQ
不从客服回复生成，需要一条显式、可审计的通道取代这句绝对表述。

## 2. 决定

员工可以对**可复制的**建议（DONE、非 mock、ANSWER、≥1 条当前有效引用）执行
「采纳为知识草稿」：

1. 服务端重新校验建议归属、终态、`can_copy` 与每条引用的 `EvidenceAllowed`，
   不信任客户端正文；
2. 服务端生成确定性 markdown（顾客问题 + 建议回答 + 编号引用附录）；
   `document_id = staff-faq-<sha256(shop_id + "\0" + markdown)[:24]>`，店铺入键
   （`document.logical_key` 租户级唯一，跨店同文不冲突）；`source.id=staff-suggestions`、
   `doc_type=faq`、`disclosure_class=internal_only`、`pipeline_version=staff-adoption-v1`；
3. 经现有 Python 摄取管线提交（`source.type='admin_manifest'`），版本落在
   `status='ready'`、`review_status='pending'`，进入现有审核队列；
4. **不自动审核、不自动发布**；`knowledge_reviewer` 审核、发布后才会参与回答；
5. 审计 `support.suggestion.adopt` 只存 `{job_id, document_id, manifest_sha256,
   citation_count, disclosure_class}`，不存问题、回答或片段；同内容重复采纳幂等。

## 3. 边界与理由

- 人工回复本身仍**永不自动入库**；采纳是员工对 AI 建议的显式动作，不是从对话
  自动生成 FAQ。`docs/knowledge.md`、`docs/support.md` 的绝对表述由本记录取代。
- 默认 `internal_only`：草稿含顾客问题，审核前只对内部可见；对外披露需商家在审核
  时显式选择并满足授权依据。
- 单写者不变：Go 生成包并通过签名 scope 提交，`document/document_version/chunk`
  仍只由 Python ingest 写入；Go 不直接写知识表。
- **`ingest_adoption` scope audience**：员工是 `operator`，没有 merchant_admin 的
  摄取授权，普通 `ingest` 受众会被 Python 拒绝。新增受众只对 `Submit` 生效，Python
  仍校验签名、身份、店铺、权限版本与运行角色；`ValidatePackage` 与命令类仍只接受
  `ingest`。Go 是授权判定点（0028）不变。

## 4. 备选方案

| 方案 | 不采用理由 |
| --- | --- |
| 从所有人工回复自动生成 FAQ | 违反 0031 与内容审核边界，噪声与 PII 不可控 |
| 采纳即发布 | 绕过 0018/0005 审核；顾客会直接看到未审内容 |
| 仅保留手工 FAQ 表单 | 丢失建议引用与来源追溯，员工无法沉淀高质量回答 |
| 新建候选表再另做发布器 | 与现有导入→审核→发布管线重复，单写者与审计分裂 |

## 5. 后果

- 采纳执行需在建议被保留策略清理前完成（当前 50 轮）；后续如需延迟采纳，
  另行设计候选持久化。
- 草稿的标题取建议问题摘要（截断），商家可在审核前通过现有导入流程修改资料。
- 角色矩阵不变：merchant_admin 在开发档同时持有 knowledge_reviewer 授权，
  审核/发布仍按现有 grants 判定。
