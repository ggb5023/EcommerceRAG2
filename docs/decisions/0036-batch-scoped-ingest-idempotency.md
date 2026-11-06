# 0036 导入幂等键并入 batch id

- 状态：已采纳（2026-11-06 提议，同日采纳并实现）
- 日期：2026-11-06
- 工作包：[68](../workpackages/68-decision-batch.md)
- 关联：[0027](0027-document-source-ownership.md)、[contracts](../contracts.md)、[error-taxonomy](../error-taxonomy.md)

## 问题

旧键是 `admin:<shop>:<sha256(manifest)>`。同一店铺的两个内容相同的批次会折叠成
同一个 ingest job，批次与 job 不再一一对应；撤回后重导入同一份内容也会命中旧 job，
不会产生新版本（refactor-input #15）。

## 决定

键改为：

```text
admin:<shop_id>:<batch_id>:<sha256(manifest)>
```

- `batch_id` 是 `import_batch.id`，确认路径必须传入；
- 同一批次重试得到同一个键，复用同一个 job；
- 不同批次即使 manifest 字节相同，也得到不同的键和不同的 job；
- 直连旧路径 `/admin/v1/merchant/sources/import` 没有 batch 行，保留旧键
  `admin:<shop>:<sha256>`，不对无批次身份的上传伪造批次号。

兼容窗口：提交新键之前，先按旧键查找**未完成** job（`pending` / `running` /
`awaiting_review`）。找到就复用；`done` / `failed` / `cancelled` 一律不复用，
否则历史 job 会吸收新批次，重新制造 0037 要修的重导入问题。

## 状态机与边界

- 查找只按 `(tenant_id, shop_id, idempotency_key)`，不会跨租户或跨店铺复用；
- 复用旧 job 时返回该 job 的 id、manifest hash、payload hash、item count 和状态；
- 新键的第一段仍校验店铺；批次 id 只在租户内唯一，键本身不跨租户；
- Python 仍以 `(tenant_id, idempotency_key)` 去重；Go 只负责生成和兼容查找，
  不改变“唯一写入方”。

## 取舍

| 方案 | 不采用理由 |
| --- | --- |
| 每次提交加随机 nonce | 同一批次重试会排队两个 job，破坏幂等 |
| 继续用 manifest hash | 同店同内容批次永远无法一一对应 |
| 只在 Python 侧加 batch 参数 | 键的生成在 Go，跨进程契约需要两侧同时改 |
| 旧键永远不复用 | 升级瞬间仍在跑的旧 job 会被重复解析、双写 |

旧 job 兼容复用会短暂破坏“批次与 job 一一对应”，这是有界的迁移例外；新键生效后
不再产生新的折叠。

## 验证

- `go/internal/admin/ingestion_key_test.go`：新键格式、同批次稳定、不同批次不折叠、
  店铺/内容隔离、直连路径旧键。
- `go/internal/admin/ingestion_legacy_integration_test.go`：旧键未完成 job 被复用且
  不调用 `Submit`；旧键 `done` job 不被复用。
- `python/tests/test_v2_ingest_database.py:test_reimport_after_revoke_creates_a_new_version`：
  撤回后新键产生新版本并重新发布（见 0037）。
