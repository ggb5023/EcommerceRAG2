# 0018 M6-L2 审核建议：判据与接口（提议）

- 状态：提议，未实现
- 日期：2026-10-27
- 范围：P2-M7 的 L2。身份、发布链路、reviewed_* 写入方不变。

## 1. 目标与边界

对某个 `document_version` 生成可复查的 `approved/needs_revision` 建议，附理由与证据 chunk；
商家可筛选 `needs_revision` 并批量确认，批量确认仍走既有 review 链路。

**不做**：agent 发布、agent 写 `document_version.reviewed_*`、agent 写
`admin_permission_request`、付费模型调用。v1 为确定性规则。

## 2. 迁移 0033

```sql
CREATE TABLE review_suggestion (
    id                       BIGSERIAL PRIMARY KEY,
    tenant_id                BIGINT NOT NULL REFERENCES tenant(id),
    version_id               BIGINT NOT NULL,
    content_hash             TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    business_date            DATE NOT NULL,
    agent_rules_version      TEXT NOT NULL,
    decision                 TEXT NOT NULL CHECK (decision IN ('approved','needs_revision')),
    reasons                  JSONB NOT NULL DEFAULT '[]',
    evidence_chunk_ids       TEXT[] NOT NULL DEFAULT '{}',
    agent_model_id           TEXT NOT NULL,
    agent_attestation_sha256 TEXT NOT NULL CHECK (agent_attestation_sha256 ~ '^[0-9a-f]{64}$'),
    agent_input_sha256       TEXT NOT NULL CHECK (agent_input_sha256 ~ '^[0-9a-f]{64}$'),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, version_id, content_hash, agent_rules_version),
    FOREIGN KEY (tenant_id, version_id) REFERENCES document_version (tenant_id, id)
);
```

- `content_hash = document_version.source_hash`，是“被审内容”的哈希。
- pending 版本的 `document_version.reviewed_hash` 必须为 NULL；建议不读也不写它。
- 商家确认后既有审核流程写 `reviewed_hash`，值与 `content_hash` 相同，语义不同，审计不混用。
- `business_date` 是给定列之外新增的：四项判据需要可复现的业务日期，不使用 `now()`。
- 唯一键保证同一版本、同一内容哈希、同一规则版本只有一条建议。

## 3. 四项判据（代码可复查）

输入：version（source_hash/effective_from/effective_to/disclosure_class）、本版本 chunk、
`document.meta_json.authorization`、`business_date`、租户开关、规则版本。输出：

```json
{"decision":"needs_revision","reasons":[{"code":"missing_authorization","detail":"..."}],
 "evidence_chunk_ids":["db-chunk-12"]}
```

| 判据 | 检查 | 失败原因码 |
| --- | --- | --- |
| C1 引用绑定 | 每个 `evidence_chunk_id` 属于本版本/tenant，chunk 存在且 `chunk_hash` 匹配；引用不为空 | `missing_citation` / `unbound_chunk` |
| C2 生效覆盖 | `effective_from <= business_date`，且 `effective_to` 为空或 `>= business_date` | `effective_expired` |
| C3 披露一致 | version 为 `external_allowed` 时，全部本版本 chunk 为 `external_allowed` | `disclosure_mismatch` |
| C4 来源授权 | `document.meta_json.authorization` 存在，且 `basis/reference/checked_at` 三个键齐全 | `missing_authorization` |

- C1 的“属于本版本、可随版本发布”指 chunk 绑定本 version，不要求当前 `status='active'`；
  否则所有 pending 版本都会被误判。
- 来源授权键固定为 `document.meta_json.authorization = {basis, reference, checked_at}`；
  缺失即 `needs_revision`。
- 不判“答案要点被正文支持”：商家侧没有真值，v1 不做无法验证的判定。

`decision='approved'` 仅当四项全部通过；任一失败为 `needs_revision`。`reasons` 只存原因码与
可复查字段名，不存正文；snippet 如需要截断到 200 字。

## 4. 审计与幂等

- `agent_input_sha256`：规范化 JSON（version 元数据、chunk id/hash、authorization、
  business_date、规则版本）的 SHA256；只存哈希，不存正文。
- `agent_attestation_sha256`：对 `{input_sha256, decision, reasons, evidence_chunk_ids,
  rules_version, model_id}` 规范化后取 SHA256；64 hex。
- `agent_model_id`：v1 固定 `local-rules-v1`；不产生付费调用。
- 唯一键命中返回既有行；`content_hash` 变化生成新建议，不复用旧结论。
- 商家批量确认时在 `admin_audit.metadata_json` 记录 suggestion id/hash。

## 5. 接口

### 5.1 生成建议

`POST /admin/v1/merchant/versions/{id}/suggestion`：

```json
{"business_date":"2026-10-27"}
```

返回 `201` 或 `200`（幂等）：`decision/reasons/evidence_chunk_ids/created_at`。
租户开关关闭返回 `409 agent_review_disabled`。

### 5.2 批量确认

`POST /admin/v1/merchant/versions/batch-approve`：

```json
{"items":[{"version_id":"12","suggestion_id":"34"}],"confirm":true,"reason":"..."}
```

服务端逐条校验 shop 权限、版本仍 pending、`content_hash` 与建议一致、建议为 `approved`；
随后以**商家账号**调用既有 review 逻辑，写 `reviewed_by/reviewed_at/reviewed_hash`。
任一条失败整批不写，返回逐条错误；重试幂等。

### 5.3 筛选

`GET /admin/v1/merchant/reviews?suggestion=needs_revision&shop_id=...`：
按最新建议筛选待审版本；无建议的版本不伪造状态。

## 6. 前端

- 版本详情显示建议徽标、原因码、证据 chunk、规则版本与“agent 建议仅供参考”；
- 批量确认页只展示 `approved` 建议，商家勾选后一次提交；
- `needs_revision` 进入待办，不自动驳回；
- 开关关闭时不显示建议入口。

## 7. 测试

- C1–C4 正反例：空证据、跨版本 chunk、过期、internal chunk、缺 authorization；
- 幂等：同 key 两次返回同一行；`content_hash` 变化生成新行；
- 批量确认：商家身份写 reviewed_*；agent/服务账号被拒；stale hash 整批回滚；
- 不写 `admin_permission_request`；不改 0001–0031；`check.sh` 全绿。

## 8. 责任与状态

- **agent**：C1–C4、decision、reasons、evidence_chunk_ids；只写 `review_suggestion`。
- **商家**：批量勾选并确认；`reviewed_*` 由商家动作产生。
- **本阶段**：只更新方案，无迁移/接口/前端/测试运行。
