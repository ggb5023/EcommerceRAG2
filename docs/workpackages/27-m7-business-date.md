# 27 M7 阶段 2（一）：业务日期来源

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0033](../decisions/0033-document-validity.md)、[0028](../decisions/0028-orchestration-boundary.md)、
> [contracts](../contracts.md)、[environment-facts](../environment-facts.md)

## 目标

把商家店铺时区作为业务日期来源，随 Go 签发的 scope 下发给 Python；本包只做
“时区存储 + business_date 合同 + Python 使用”，不做版本有效期、admin 有效期命令、
前端有效期表单或检索 SQL 改动。

## 设计口径（0033 已批准）

- `shop.timezone` 为 IANA 时区，默认 `Asia/Shanghai`；admin 设置入口校验。
- Go `IssueScope*` 读取店铺时区，用该时区计算 `YYYY-MM-DD`，写入
  `RequestContext.business_date` 并纳入 HMAC 签名。
- Python 取值链：`scope.business_date` → `SYNTHETIC_BUSINESS_DATE`（dev/eval）→
  UTC 当天兜底；非法格式拒绝，不静默回退。
- 不做检索 SQL、版本日期、有效期命令、前端有效期表单；不改棘轮/基线/smoke。

## 改动顺序

1. 迁移 0047 给 `shop` 加 `timezone`；admin 设置入口写 timezone（SQL 只在 `store_*.go`）。
2. proto `RequestContext` 下一字段（26）加 `string business_date`；buf 生成 Go/Python；
   authority 计算/签名；contracts 增加 scope business_date 行与两侧测试。
3. Python `graph/service.py` 使用 scope 日期；`rpc/rag.py` 透传。
4. 重启 Go/Python，跑全门禁与 smoke；scope 结构变化若导致 smoke 基线变化先停下报告。

## 验收命令

```bash
# 迁移（0047）：dev/test-integration/eval-migration 三库 + sha256；from-zero
bash scripts/dev/generate-proto.sh
python3 scripts/check/check-proto.sh
cd go && go test ./...
cd python && uv run --frozen pytest -q
# 重建 Go、strings、重启；重启 Python
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 不做

- 不做版本有效期迁移/命令/API/前端（WP28/WP29）；不改检索 SQL；
- 不调棘轮/基线；不重采 smoke；不引入依赖；
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（完成后填写）

## 未覆盖路径

（完成后填写）
