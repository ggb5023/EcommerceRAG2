# 28 M7 阶段 2（二）：版本有效期实现

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0033](../decisions/0033-document-validity.md)、
> [data-ownership](../data-ownership.md)、[contracts](../contracts.md)

## 目标

在版本级实现生效/失效日期：迁移、ingest validity command、admin API 与传播；
不做过期提示前端（WP29）、不自动下架、不改检索 SQL。

## 口径（0033）

- `document_version` 增加可空 `effective_from/effective_to`；chunk 保留物化字段。
- admin 只做授权、日期校验、confirm 和审计转发；实际 SQL 由 ingest command 在
  同一事务内写 `document_version` + 全部 chunk + `meta_json`。
- `expected_fencing_epoch` 不匹配拒绝；日期 `to > from`，空表示无界。
- `effective_to` 是失效日（不含当天），检索已有 SQL 不改。

## 验收命令

```bash
# 0048 三库 + sha256；from-zero
bash scripts/dev/generate-proto.sh
python3 scripts/check/check-proto.sh
cd go && go test ./...
cd python && uv run --frozen pytest -q
# 造多 chunk 版本，设日期，SQL 断言版本/全部 chunk/meta 一致；旧 fence 重放拒绝
# curl 设置接口贴完整响应
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不做过期/即将过期提示前端（WP29）；不改检索 SQL；不自动下架；
- 不调棘轮/基线；不重采 smoke；不引依赖；
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
