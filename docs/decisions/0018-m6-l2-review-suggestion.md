# 0018 M6-L2 审核建议：判据与接口（提议）

- 状态：提议，未实现
- 日期：2026-10-27
- 范围：P2-M7 的 L2。身份与发布链路不变。

## 1. 目标与非目标

**目标**：对某个 `document_version` 生成可复查的 `approved/needs_revision` 建议，附理由与
证据 chunk；商家可筛选 `needs_revision` 并批量确认，批量确认仍走既有 review 链路。

**非目标**：agent 不发布、不写 `document_version.reviewed_*`、不写
`admin_permission_request`；不调用付费模型（v1 为确定性规则）；不改 0001–0031 迁移。

## 2. 迁移 0033

```sql
CREATE TABLE review_suggestion (
    id                       BIGSERIAL PRIMARY KEY,
    tenant_id                BIGINT NOT NULL REFERENCES tenant(id),
    version_id               BIGINT NOT NULL,
    reviewed_hash            TEXT NOT NULL CHECK (reviewed_hash ~ '^[0-9a-f]{64}$'),
    business_date            DATE NOT NULL,
    agent_rules_version      TEXT NOT NULL,
    decision                 TEXT NOT NULL CHECK (decision IN ('approved','needs_revision')),
    reasons                  JSONB NOT NULL DEFAULT '[]',
    evidence_chunk_ids       TEXT[] NOT NULL DEFAULT '{}',
    agent_model_id           TEXT NOT NULL,
    agent_attestation_sha256 TEXT NOT NULL CHECK (agent_attestation_sha256 ~ '^[0-9a-f]{64}$'),
    agent_input_sha256       TEXT NOT NULL CHECK (agent_input_sha256 ~ '^[0-9a-f]{64}$'),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, version_id, reviewed_hash, agent_rules_version),
    FOREIGN KEY (tenant_id, version_id) REFERENCES document_version (tenant_id, id)
);
```

`business_date` 是给定列之外新增的：C2 需要可复现的业务日期，不能用 `now()`。
不新增 `reviewed_by/reviewed_at`；建议不是审核。

**`reviewed_hash` 的取值**：pending 版本的 `document_version.reviewed_hash` 必须为 NULL，
所以建议行里的 `reviewed_hash` 写入 `document_version.source_hash`（即将被审核的内容哈希），
不读取版本的 `reviewed_hash`。商家确认后版本写回同一个 `source_hash`，两者对齐；
agent 仍不写版本的 `reviewed_*`。

## 3. 四条判据（代码可复查）

输入：version（含 `source_hash/effective_from/effective_to/disclosure_class`）、该版本 chunk、
`business_date`、租户开关、规则版本。输出：

```json
{"decision":"needs_revision","reasons":[{"code":"unbound_chunk","detail":"..."}],
 "evidence_chunk_ids":["db-chunk-12"]}
```

| 判据 | 检查 | 失败原因码 |
| --- | --- | --- |
| C1 引用绑定 | 每个 `evidence_chunk_id` 属于本 version/tenant，chunk 存在且 `chunk_hash` 匹配；引用不得为空 | `missing_citation` / `unbound_chunk` |
| C2 生效覆盖 | `effective_from <= business_date`，且 `effective_to` 为空或 `>= business_date` | `effective_expired` |
| C3 披露一致 | version 为 `external_allowed` 时，全部 chunk 为 `external_allowed`，且来源授权标记不是 internal/未授权 | `disclosure_mismatch` |
| C4 要点支持 | 每个 `answer_point` 至少被一个正文 chunk 支持（数字、单位、否定词、限定词逐项匹配） | `unsupported_point` |

`decision='approved'` 仅当四条全部通过；任一 block 级失败为 `needs_revision`。
`reasons` 只存原因码与可复查的字段名，不存正文；`snippet` 若需要，截断到 200 字。

**C4 的要点来源**是当前缺口：仓库没有 `answer_points` 字段。建议在 L1 manifest 增加可选
`answer_points`，确认后写入 `document.meta_json`；缺省时用标题与各级 heading 作为结构性要点，
并只在正文能逐项找到对应数字/单位/条件时判 supported。该口径需确认。

**C1 的“已发布 chunk”**：pending version 尚未发布。建议解释为“属于本 version 且未来可随该版本
发布的 chunk”，不要求当前 `status='active'`；否则所有 pending 版本都会被判失败。

## 4. 审计与幂等

- `agent_input_sha256`：对规范化 JSON（version 元数据、chunk id/hash、规则版本、business_date）
  取 SHA256；只存哈希，不存正文。
- `agent_attestation_sha256`：对 `{input_sha256, decision, reasons, evidence_chunk_ids,
  rules_version, model_id}` 规范化后取 SHA256；64 hex。
- `agent_model_id`：v1 固定 `local-rules-v1`；不产生付费调用。
- 唯一键命中时返回既有行；`reviewed_hash` 变化则生成新建议，不复用旧结论。
- 建议写入本身是审计记录；商家批量确认时在 `admin_audit.metadata_json` 记录 suggestion id/hash。

## 5. 接口

### 5.1 生成建议

`POST /admin/v1/merchant/versions/{id}/suggestion`，body：

```json
{"business_date":"2026-10-27","force":false}
```

返回 `201` 或 `200`（幂等）：`decision/reasons/evidence_chunk_ids/created_at`。

### 5.2 批量确认

`POST /admin/v1/merchant/versions/batch-approve`，body：

```json
{"items":[{"version_id":"12","suggestion_id":"34"}],"confirm":true,"reason":"..."}
```

服务端逐条校验：当前 session 有 shop 权限、版本仍 pending、`reviewed_hash` 与建议一致、
建议为 `approved`；随后调用既有 review 逻辑，以**商家账号**写 `reviewed_by/reviewed_at/
reviewed_hash`。任一失败整批不写，返回逐条错误；重试幂等。

### 5.3 筛选

`GET /admin/v1/merchant/reviews?suggestion=needs_revision&shop_id=...`：
按最新建议筛选待审版本；无建议的版本不伪造状态。

## 6. 前端

- 版本详情显示建议徽标、原因码、证据 chunk、规则版本与“agent 建议仅供参考”；
- 批量确认页只展示 `approved` 建议，商家勾选后一次提交；
- `needs_revision` 进入待办，不自动驳回；
- 关闭租户开关时不显示建议入口。

## 7. 测试

- C1–C4 各自的正反例；空证据、跨版本 chunk、过期、internal chunk、缺要点；
- 幂等：同 key 两次返回同一行；`reviewed_hash` 变化生成新行；
- 批量确认：商家身份写入 reviewed_*；agent/服务账号调用被拒；stale hash 整批回滚；
- 不写 `admin_permission_request`；不改 0001–0031；`check.sh` 全绿。

## 8. 阻塞项

1. 租户开关的落库位置（同 L1 第 8 节）；
2. C4 的 `answer_points` 来源与“支持”的确定性口径；
3. C1 对 pending 版本“已发布 chunk”的解释；
4. C3 的“来源授权”目前没有独立字段，需确认用 `document.meta_json` 的哪个键，或允许
   以 version/chunk 的 disclosure_class 一致性作为 v1 口径。

## 9. 责任边界

- **agent 判定**：C1–C4、decision、reasons、evidence_chunk_ids；只写 `review_suggestion`。
- **商家确认**：批量勾选并提交；`reviewed_*` 由商家动作产生。
- **本阶段没跑**：未写迁移/接口/前端/测试；只交方案。
