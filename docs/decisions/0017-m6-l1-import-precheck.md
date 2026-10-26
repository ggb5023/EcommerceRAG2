# 0017 M6-L1 导入预检：数据模型与接口（提议）

- 状态：提议，未实现
- 日期：2026-10-27
- 范围：P2-M6 的 L1。L2/L3 见 0018/0016；开发期把关见 0015。

## 1. 目标与边界

商家上传资料包后，系统解析、预检、展示问题与建议值；商家点击确认后，才通过既有摄取链路落库为
`review_status='pending'` 的版本；未发布前整批软撤回。预检只给建议，不发布，不写
`reviewed_by/reviewed_at/reviewed_hash`。

- **agent 判定**：R1–R4 的 issue/severity/suggestion；不改发布状态。
- **商家确认**：上传、核对、confirm、revoke。
- **不做**：L2 建议、L3 巡检、付费模型、R4 的关键词内容差异判定。

## 2. 写入方与批次关联（已确认）

- `persistParsed` 只提交现有摄取 RPC；`document/document_version/chunk` 仍由 Python 摄取写。
- 每个批次建一条 `source_manifest` 记录；确认后的版本通过
  `document_version.source_manifest_id` 关联该批次，不给 `document_version` 加列、不换写入方。
- 上传阶段若需要保存字节，走 stage-only 摄取调用（Python 写 `source_manifest/source_file`，
  不建 document/version）；Go 只写 `import_batch/import_item`。stage-only 的 proto 细节在阶段 2
  落地前单独确认，不改变上面的写入方。

`parseManifest` 为纯函数：校验 manifest schema、路径、文件哈希、R1–R4 所需字段，返回
`ParsedDocument`；不写库。`persistParsed` 只做确认后的 RPC 提交。

## 3. 迁移 0032（三张新表，不改旧表）

### 3.1 import_batch / import_item

```sql
CREATE TABLE import_batch (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           BIGINT NOT NULL REFERENCES tenant(id),
    shop_id             TEXT   NOT NULL,
    created_by          BIGINT NOT NULL REFERENCES app_user(id),
    status              TEXT   NOT NULL DEFAULT 'staged'
                        CHECK (status IN ('staged','confirmed','revoked')),
    business_date       DATE   NOT NULL,
    agent_rules_version TEXT   NOT NULL,
    agent_model_id      TEXT   NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX idx_import_batch_scope ON import_batch (tenant_id, shop_id, created_at DESC);

CREATE TABLE import_item (
    id                          BIGSERIAL PRIMARY KEY,
    batch_id                    BIGINT NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
    source_file_id              BIGINT REFERENCES source_file(id),
    document_id                 BIGINT REFERENCES document(id),
    source_hash                 TEXT NOT NULL CHECK (source_hash ~ '^[0-9a-f]{64}$'),
    suggested_disclosure_class  TEXT CHECK (suggested_disclosure_class IN
                                  ('external_allowed','internal_only','unclassified')),
    suggested_effective_from    DATE,
    precheck_result             JSONB NOT NULL DEFAULT '[]',
    precheck_detail             JSONB NOT NULL DEFAULT '{}',
    UNIQUE (batch_id, source_hash)
);
CREATE INDEX idx_import_item_batch ON import_item (batch_id, id);
```

`business_date` 必须显式落库，R2 与复现都不使用 `now()`。`source_hash` 是 R3 的判重键；给定列
没有它，但不存就无法做可索引的重复检测。preview 阶段 `source_file_id/document_id` 可空，
confirm 后回填；`reviewed_*` 三列始终不写。

### 3.2 tenant_agent_review（append-only）

```sql
CREATE TABLE tenant_agent_review (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      BIGINT NOT NULL REFERENCES tenant(id),
    enabled        BOOLEAN NOT NULL,
    policy_version TEXT NOT NULL,
    actor_user_id  BIGINT NOT NULL REFERENCES app_user(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tenant_agent_review_latest
    ON tenant_agent_review (tenant_id, created_at DESC, id DESC);

CREATE FUNCTION ecr_tenant_agent_review_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tenant_agent_review is append-only' USING ERRCODE='55000';
END $$;
CREATE TRIGGER trg_tenant_agent_review_append_only
    BEFORE UPDATE OR DELETE ON tenant_agent_review
    FOR EACH ROW EXECUTE FUNCTION ecr_tenant_agent_review_append_only();
```

当前状态取该 tenant 最新一行；默认 false，历史全部保留。该函数不是 SECURITY DEFINER。
`merchant_admin` 切换时写 who/when/policy_version，并写 `admin_audit`；不改 `tenant` 表。

## 4. 接口

### 4.1 创建批次 + 上传

`POST /admin/v1/merchant/imports`，multipart 复用现有约定：`shop_id`、`manifest`（≤256 KiB）、
`paths[]`+`files[]`（≤100 个、单文件 ≤8 MiB）、`business_date`。
`agent_rules_version` 服务端固定 `v1`，客户端不得覆盖。租户开关关闭时返回
`409 agent_review_disabled`，不创建批次。

返回 `201`：`batch_id/status/item_count/issues[]`；issue 固定结构
`{issue_code,severity,snippet(≤200字),suggested_action}`。

### 4.2 预览

`GET /admin/v1/merchant/imports/{id}`：批次元数据、逐条 staging 片段、问题列表、建议
`disclosure_class/effective_from`。预览不返回 document 链接；document 尚未创建。

### 4.3 确认

`POST /admin/v1/merchant/imports/{id}/confirm`：

```json
{"confirm":true,"reason":"已核对预检问题","expected_item_hashes":["..."]}
```

服务端校验归属、状态、item hashes、租户开关；由商家账号触发 `persistParsed`，
版本以 `review_status='pending'` 落库，`source_manifest_id` 指向该批次 manifest。
`import_batch.status='confirmed'`，回填 `document_id`。agent 不调用此接口。

### 4.4 撤回

`POST /admin/v1/merchant/imports/{id}/revoke`：允许 `staged`，或 `confirmed` 且该批所有版本仍
pending、未 active；写 `status='revoked'`、`revoked_at`，软删除未发布版本。
已发布版本走既有版本撤回。

## 5. 预检规则 v1（纯字段）

| 规则 | 条件 | 严重度 | suggested_action |
| --- | --- | --- | --- |
| `missing_source` | 标题为空，或 manifest 没有 `source/license` 等可引用出处 | block | 补来源/标题后重传 |
| `expired_content` | `effective_from < business_date`，或 `effective_to` 非空且 `< business_date` | block | 更新生效期 |
| `duplicate_import` | 同 tenant/shop 下 `source_hash` 已存在 | warn | 确认覆盖或撤回旧批次 |
| `missing_disclosure_basis` | 声明 `external_allowed=true` 或 `disclosure_class=public`，但缺少 `license`、`authorization.reference`、`checked_at` | block | 补授权依据后重传 |

R4 是字段判定，不做关键词/内容差异；关键词版 `disclosure_conflict` 留 v2 定阈值。
`snippet` 取 manifest 的 `excerpt`，缺失时取文本文件前 200 字；DOCX 必须在 manifest 提供
`excerpt`，否则 snippet 为空并在 detail 标注 `excerpt_missing`。

## 6. fixture 与验收

`tests/fixtures/import-batch-20/` 20 份文档，其中三份分别埋：

1. 缺出处 → `missing_source`；
2. 生效期已过 → `expired_content`；
3. 声明对外可见但删掉 `license` → `missing_disclosure_basis`。

集成测试（`V2_INTEGRATION_ENV`）断言：批次创建成功、3 个 issue_code 全部命中、确认后 20 份
`review_status='pending'` 且 `source_manifest_id` 非空、撤回后全部软删除、
`admin_audit` 可按 batch id 查到。纯函数测试：非法 manifest、缺文件、哈希不符。

## 7. 前端三步向导

上传（文件 + business_date + 开关状态）→ 预览（片段、block/warn、建议值；固定文案
“agent 预检仅供参考，发布前请确认”）→ 确认（勾选已核对、填原因）。撤回只在批次详情，
显示“仅未发布可撤”。

## 8. 责任与状态

- **agent**：R1–R4 判定、建议值、snippet；不落版本、不写 reviewed_*。
- **商家**：confirm/revoke；confirm 才产生 pending 版本。
- **本阶段**：只更新方案，无迁移/接口/前端/集成测试运行。
