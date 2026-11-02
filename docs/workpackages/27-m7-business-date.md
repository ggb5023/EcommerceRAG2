# 27 M7 阶段 2（一）：业务日期来源

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0033](../decisions/0033-document-validity.md)、[0028](../decisions/0028-orchestration-boundary.md)、
> [contracts](../contracts.md)、[environment-facts](../environment-facts.md)

## 目标

把商家店铺时区作为业务日期来源，随 Go 签发的 scope 下发给 Python；本包只做
“时区存储 + business_date 合同 + Python 使用”，不做版本有效期、admin 有效期命令、
前端有效期表单或检索 SQL 改动。

## 设计口径（0033 已批准）

- `shop.timezone` 为 IANA 时区，默认 IANA 时区（见 0047 迁移）；admin 设置入口校验。
- Go `IssueScope*` 读取店铺时区，用该时区计算 `YYYY-MM-DD`，写入
  `RequestContext.business_date` 并纳入 HMAC 签名。
- Python 取值链：`scope.business_date` → `SYNTHETIC_BUSINESS_DATE`（dev/eval）→
  UTC 当天兜底；非法格式拒绝，不静默回退。
- 不做检索 SQL、版本日期、有效期命令、前端有效期表单；不改棘轮/基线/smoke。

## 改动顺序

1. 迁移 0047 给 `shop` 加 `timezone`；admin 设置入口写 timezone（SQL 只在 `store_*.go`）。
2. proto `RequestContext` 下一字段（26）加 `string business_date`；buf 生成 Go/Python；
   authority 计算/签名；contracts 增加 scope business_date 行与两侧测试。
3. Python `python/app/graph/service.py` 使用 scope 日期；`python/app/rpc/rag.py` 透传。
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

- 迁移 0047：`shop.timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai'`；已应用到
  dev / test-integration / eval-migration 三库，47/47 校验和一致，from-zero PASS。
- admin：shop 设置 handler 接收 `timezone`，用 `time.LoadLocation` 校验；
  SQL 只在 `go/internal/admin/store_shop.go`；Shop 列表和前端 ShopSettings 表单显示/编辑。
- proto：`RequestContext.business_date = 26`；`bash scripts/dev/generate-proto.sh` 生成
  Go/Python 文件；`check-proto.sh`（lint/build/breaking）exit 0，新增字段向后兼容。
- Go authority：`IssueScopeForShopWithTurn` 查 `shop.timezone`，用
  `businessDateInTimezone` 计算并写入签名 scope；customer scope 同样写入；
  UTC 23:30 → `Asia/Shanghai` 次日 2026-11-03 的边界测试通过；MAC 变化测试通过。
- Python：`_v2_scope` 透传 `business_date`；`LangGraphRagService._business_date`
  按 scope → synthetic → UTC 兜底，非法日期拒绝；测试通过。
- contracts：新增 `scope business_date` 跨进程行，两侧测试已列出。
- Go：`go test ./...` 全绿；Python：**419 passed / 30 skipped**。
- 快速层：`bash scripts/check.sh` **40 PASS / 0 UNCONFIGURED / 0 FAILED**。
- **smoke 未通过且未重采基线**：
  - `smoke-answer` 的 `answer_sha256` 从 `bdba...` 变为 `4552...`；
  - `smoke-facts` 的 `answer_sha256` 从 `1231...` 变为 `e636...`；
  - 两者的 `structure_sha256` 未变；
  - 原因是 scope 中的店铺本地日期优先于 dev 的 `SYNTHETIC_BUSINESS_DATE=2026-09-29`，
    生成文本中的业务日期变化；按约定不重采 `.local/w2/` 基线，停下报告。
- 重启：Go 重建并 strings 命中 `shop_timezone_unavailable`；Go/Python 进程在
  2026-11-03 00:10:07 启动（晚于代码提交 `b18a2f8`）。

## 未覆盖路径

- smoke 基线变化，未通过；未重采基线，WP27 不能按“45 PASS”验收。
- 未做 admin 设置页的浏览器交互验证；只做 Go 校验单测和前端 build。
- 时区数据只有默认 `Asia/Shanghai`；未覆盖多时区真实店铺切换。
- 未实现版本有效期/命令/API/前端（WP28/WP29）；未改检索 SQL。
- 未跑真实模型/外部服务；未 push。
