# 55 P2：拒绝路径补默认层单测

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P2](../audits/2026-11-04-global-audit.md)、
> [agent-entry §2](../agent-entry.md)、[environment-facts](../environment-facts.md)

## 目标

把三条目前只有 integration/HTTP 覆盖的拒绝路径抽成纯函数，补默认层表驱动单测：
`turn_idempotency` 冲突、customer scope 的过期/epoch/staleness、ingest fencing 的
`expected_fencing_epoch` 比对。行为零变更，不搬现有 integration 测试。

## 纯函数与测试

### gateway：turn idempotency

- 新增 `classifyTurnIdempotency(found bool, storedHash, payloadHash string)`：
  - 未找到 → 新建；
  - 找到且 hash 相同 → replay；
  - 找到但 hash 不同 → conflict。
- `findIdempotentTurn` 改为使用该分类，错误码和状态保持原样。
- 测试：无记录、同 hash、不同 hash、空 hash 的边界。

### auth：customer scope

- 新增 `scopeExpiryInvalid(expiresAt, now int64) bool`，由 `ValidateScope` 使用；
  覆盖过期、边界 `now`、`now+70`、超前 `now+71`。
- 新增 `customerScopeMismatch(scope, current)`：比较 customer / tenant / shop /
  permission revision / conversation epoch，并检查 allowed document 不扩张；
  返回 `customer_scope_stale` 或 `customer_document_forbidden`，由
  `ValidateCustomerScope` 原样转成 gRPC 错误。
- 测试：每个字段单独变化、epoch 落后、revision 变化、document 扩张、完全一致。
- session 过期/撤销仍由 `IssueCustomerScope` 的 SQL 判定，属于 integration 覆盖，
  本轮不把 SQL 判定搬成纯函数。

### ingestclient：expected fencing epoch

- 新增 `FencingEpochMatches(expected, current int64) bool`（相等即匹配），
  `admin.confirmVersion` 用它替换原有 `in.ExpectedFencingEpoch != fence` 比较。
- 测试：相等、expected 落后、expected 超前、零值与负值边界。
- 不改变 `ExecuteCommandRequest` 的构造字段，不改变 Python 侧 fencing 规则。
- **盘点发现**：`admin.confirmVersion` 当前没有调用方；因此该 helper 不会出现在
  运行中的 gateway 二进制里。真实 Go/Python fencing 比对仍在 Python
  `python/app/ingest/commands.py` 与数据库触发器；本轮不复活死函数、不改行为。

## 验收命令

```bash
cd go && go test -count=1 ./...
cd go && go vet ./...
bash scripts/check.sh
bash scripts/check.sh --smoke
```

新增用例必须是默认层（不依赖 `V2_INTEGRATION_ENV`、不连数据库），每条按包统计。

## 验证结果（2026-11-05）

新增 5 个测试函数、28 个表驱动子用例：

| 包 | 测试函数 | 子用例 | 覆盖 |
| --- | --- | --- | --- |
| `gateway` | `TestClassifyTurnIdempotency` | 6 | 新建 / replay / conflict / 空 hash |
| `auth` | `TestCustomerScopeMismatch` | 10 | customer/tenant/shop/revision/epoch/document |
| `auth` | `TestScopeExpiryInvalid` | 5 | 过期、now、now+70、now+71 |
| `ingestclient` | `TestFencingEpochMatches` | 6 | 相等、落后、超前、负数边界 |
| `ingestclient` | `TestPayloadIncludesExpectedFencingEpoch` | 1 | 构造 payload 包含 expected fence |

```text
go test -count=1 ./...        → 23 package 结果，0 FAIL
go vet ./...                  → ok
```

重建 gateway 并重启 systemd 后：

```text
classifyTurnIdempotency=1
customerScopeMismatch=2
FencingEpochMatches=0   # 仅被无调用方的 admin.confirmVersion 引用，未链接
```

`bash scripts/check.sh` → `PASSED 43 / UNCONFIGURED 0 / FAILED 0`；
重启后 `bash scripts/check.sh --smoke` → `PASSED 46 / UNCONFIGURED 0 / FAILED 0`，
五条 diff 与 `.local/w2/` 基线逐字节一致。

## 不做

- 不改产品行为、错误码、HTTP 状态或 gRPC 状态；不搬 integration 测试。
- 不调棘轮/基线；不重采 smoke；不用 `git add .`；不 push。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- `IssueCustomerScope` 的 session 过期/撤销 SQL、`customer` 包内的 claim lease
  仍需 integration 覆盖；本轮只覆盖可离开数据库的字段比较和 expiry 边界。
- `expected_fencing_epoch` 的真实比对仍在 Python `python/app/ingest/commands.py` 和数据库
  触发器；Go 侧 `admin.confirmVersion` 无调用方，helper 只到默认层测试，不进入
  运行路径。若后续复活 confirmation HTTP 路径，应直接复用该 helper。
- 远端 Actions、e2e、真实模型/OSS/Keycloak 不在本轮范围；Go 侧只验证了
  `go test`、`go vet`、重建/重启和 smoke。
