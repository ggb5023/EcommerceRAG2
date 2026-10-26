# 0017 M6-L1 导入预检：数据模型与接口（提议）

- 状态：提议，未实现
- 日期：2026-10-27
- 范围：P2-M6 的 L1（导入预检）。L2/L3 另文。
- 前置：`0013` 的必要人工清单、`0015` 的开发期 agent 把关。

## 1. 目标与非目标

**目标**：商家上传资料包时，系统先解析、预检、展示问题与建议值；商家确认后才落库为
`review_status='pending'` 的版本；未发布前可整批软撤回。预检本身只给建议，不发布、不写
`reviewed_*`。

**非目标**：不做 R4 内容差异/披露冲突的完整判定（见第 8 节阻塞项），不做 L2 审核建议，
不做 L3 巡检，不接任何付费模型；v1 预检是纯规则。

## 2. 与现有代码的差异（必须先对齐）

当前 `main@6d34818` 没有名为 `importSources` 的函数。实际路径是：

`adminImportPackage`（`go/internal/admin/admin.go:766`）
→ 校验 multipart、manifest、路径、大小
→ `queuePackage`（`go/internal/admin/ingestion.go:34`）
→ `Ingest.Submit`（Python 摄取 RPC）
→ Python 写 `source_manifest/source_file/document/document_version/chunk`。

因此：

- 阶段 1 的 `parseManifest` 只能从 `adminImportPackage` 的校验段提取，不能假设已有
  `importSources`。
- 阶段 1 的 `persistParsed` 若按字面“写 document/document_version”，会违反架构红线 3
  （一张表一个写入方；Python 是摄取写者）。**建议**：`persistParsed` 只把已确认的解析结果
  提交给现有 `Ingest.Submit`，由 Python 写表；Go 只写 `import_batch/import_item` 与审计。
- 若确认必须由 Go 直接写 `document/document_version`，需要单独的架构豁免记录；本轮不这样做。

### 2.1 阶段 1 的签名草案

```go
type ParsedDocument struct {
    LogicalKey       string
    Title            string
    SourceHash       string
    Content          string
    EffectiveFrom    *time.Time
    EffectiveTo      *time.Time
    DeclaredDisclosure string
}

// 纯函数：只校验 manifest schema、路径、文件哈希并返回解析结果；不写库。
func parseManifest(manifest string, files []*ragv1.PackageFile) ([]ParsedDocument, error)

// 确认时调用：把已确认结果交给现有摄取链路；Go 不直接写 document/document_version。
func (g *Handler) persistParsed(ctx context.Context, parsed []ParsedDocument, batchID int64) error
```

解析发生在哪一侧仍需确认：若解析留在 Python，`parseManifest` 的返回来自只读解析 RPC，
纯函数只做校验与规则；若在 Go 解析 DOCX，需要新增依赖并偏离组件职责。

## 3. 迁移 0032（只新增两张表）

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

说明：

- `business_date` 显式落库，R2 与复现都以它为准，不用 `now()`。
- `source_hash` 是 R3 的判重键；给定列里没有它，但不存就无法做可索引的重复检测。
- `source_file_id/document_id` 在 preview 阶段可空：preview 不创建 document；confirm 后回填。
- 不写 `reviewed_by/reviewed_at/reviewed_hash`；三列只在商家审核动作发生时由既有审核链路写。

## 4. 上传、预览、确认、撤回

### 4.1 创建批次 + 上传

`POST /admin/v1/merchant/imports`，`multipart/form-data`，复用现有约定：

- `shop_id`：必须在当前管理员 scope 内；
- `manifest`：YAML，≤256 KiB；
- `paths[]` + `files[]`：数量一致，≤100 个，单文件 ≤8 MiB；
- `business_date`：必填，`YYYY-MM-DD`；
- `agent_rules_version`：服务端固定 `v1`，客户端不得覆盖。

成功返回 `201`：

```json
{"batch_id":"123","status":"staged","item_count":20,
 "issues":[{"item_id":"...","issue_code":"missing_source","severity":"block",
            "snippet":"...","suggested_action":"补充来源后重新上传"}]}
```

### 4.2 预览

`GET /admin/v1/merchant/imports/{id}`：返回批次元数据、逐条 staging 片段
（≤200 字）、预检问题、建议 `disclosure_class/effective_from`、确认所需的前置条件。
预览不返回 document 链接；此时 document 尚不存在，前端不得伪造跳转。

### 4.3 确认

`POST /admin/v1/merchant/imports/{id}/confirm`：

```json
{"confirm":true,"reason":"已核对预检问题","expected_item_hashes":["..."]}
```

服务端重新校验批次归属、状态、`expected_item_hashes`、租户开关；通过后由商家账号触发
`persistParsed`，把 staging 内容提交现有摄取链路，版本以 `review_status='pending'` 落库；
`import_batch.status='confirmed'`，回填 `import_item.document_id`。**agent 不调用此接口，
也不写 reviewed_*。**

### 4.4 撤回

`POST /admin/v1/merchant/imports/{id}/revoke`：仅允许 `staged`，或 `confirmed` 且该批所有版本
仍为 pending、未 active；写入 `status='revoked'`、`revoked_at`，并软删除该批未发布版本。
已发布版本走既有版本撤回，不用本接口。

## 5. 预检规则 v1（纯函数）

输入：解析后的 `ParsedDocument{logical_key,title,source_hash,content,effective_from,effective_to,
declared_disclosure}` 与 `business_date`。输出固定：

```json
{"issue_code":"missing_source","severity":"block","snippet":"...","suggested_action":"..."}
```

| 规则 | 条件 | 严重度 | suggested_action |
| --- | --- | --- | --- |
| `missing_source` | 标题为空，或正文没有可引用的来源/出处段 | block | 补充来源或标题后重传 |
| `expired_content` | `effective_from < business_date`，或 `effective_to` 非空且 `< business_date` | block | 更新生效期或替换内容 |
| `duplicate_import` | 同 tenant/shop 下 `source_hash` 已存在 | warn | 确认是否覆盖，或撤回旧批次 |

`snippet` 截断到 200 字；`precheck_detail` 存规则版本、输入 hash、命中字段，不存全文。
纯函数测试：非法 manifest、缺文件、哈希不符三种输入都返回结构化错误，不写库。

## 6. 前端三步向导

1. **上传**：选文件、填 `business_date`、显示租户开关状态；
2. **预览**：左侧片段，右侧问题列表（block/warn）、建议披露等级与生效期；
   按钮旁固定文案“agent 预检仅供参考，发布前请确认”；
3. **确认**：商家勾选“已核对”，输入原因，提交；成功后进入现有审核队列。

撤回入口只在批次详情，展示“仅未发布可撤”。

## 7. 验收与测试

- 单元：`parseManifest` 非法 schema、缺文件、哈希不符；R1/R2/R3 边界。
- 集成（`V2_INTEGRATION_ENV`）：`tests/fixtures/import-batch-20/` 20 份文档，
  断言批次创建、3 个 issue_code 命中、确认后 20 份 `review_status='pending'`、
  撤回后全部软删除且 `admin_audit` 可按 batch id 查到。
- 回归：现有 `POST /admin/v1/merchant/sources/import` 行为不变；`bash scripts/check.sh` 保持全绿。

## 8. 阻塞项（需确认后才动代码）

1. **写入方与 staging 存储**：`persistParsed` 是否接受“提交现有摄取 RPC，由 Python 写
   document/version”？上传字节放在哪里也需要定：`import_item.source_file_id` 要求先有
   `source_file`，而该表目前由 Python 写。建议增加只读/暂存 RPC，由 Python 写 staging 的
   `source_manifest/source_file`，Go 只写 `import_batch/import_item`；若要求 Go 直写，需架构豁免。
2. **租户开关**：仓库没有 `tenant.agent_review_enabled`，而 0032 限定只新增两张表、不改旧表。
   可选：0032 增加 `tenant_agent_review` 小表（推荐，默认 false）；或允许给 `tenant` 加列；
   或退化为每批 opt-in（不符合“租户开关”）。
3. **第三条 fixture**：R1–R3 无法识别“内部资料标公开”。要么 v1 增加最小
   `disclosure_conflict` 规则（声明 external 但来源标记 internal/未授权），要么把第三条
   fixture 改成重复导入（R3）。R4 的“内容差异大”阈值仍留到第二批。
4. **解析位置**：预检需要正文片段；当前解析在 Python。建议新增只读 `ParsePackage` gRPC
   （proto 变更、无 DB 写），Go 只跑规则；若坚持 Go 解析 DOCX，会引入新依赖并违反组件职责。

## 9. 责任边界

- **agent 判定**：R1/R2/R3 的 issue、severity、建议值；不发布、不写 reviewed_*。
- **商家确认**：上传、核对、点击 confirm、点击 revoke；confirm 才产生 pending 版本。
- **本阶段没跑**：未写迁移、未改 Go/Python/前端、未跑集成测试；只交方案。
