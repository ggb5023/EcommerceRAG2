# 75 采纳后端：建议→知识草稿

- 状态：见 INDEX.md
- 依据：决策 0039；用户选择「员工采纳→草稿→审核发布」
- 前置：WP74 已关

## 目标

`POST /v1/support/suggestions/{id}/adopt`：服务端校验可复制建议，生成确定性
markdown 包，经现有 Python 摄取管线提交为 `ready`、`pending` 版本，写不含正文的
审计；不自动审核/发布。

## 后端

1. Go `go/internal/gateway/support_adoptions.go`：
   - 角色 owner/admin/operator + 身份刷新；
   - `loadSuggestionForCopy` 校验 suggestion 归属/状态（DONE）；`executionResult`
     校验非 mock、`can_copy`、正文非空；`suggestionCitationsAllowed` 校验全部引用
     当前有效；
   - `buildAdoptionPackage`：问题+回答+编号引用附录；`document_id =
     staff-faq-<sha256(shop+"\0"+markdown)[:24]>`；`source.id=staff-suggestions`、
     `doc_type=faq`、`disclosure_class=internal_only`、`pipeline_version=staff-adoption-v1`；
   - 签名 scope 设 `scope_audience="ingest_adoption"` 后调用 Ingest.Submit；
     幂等键 `staff-adoption:<shop>:<manifest_sha256>`；
   - 审计 `support.suggestion.adopt`（仅 job_id/document_id/manifest_sha256/
     citation_count/disclosure_class），denied/failed 同样审计；
   - 重复采纳：已有成功审计 → 200 `already_adopted`。
2. SQL 只在 `store_*.go`：`loadSuggestionQuestion`、`suggestionAdoptionJob`；
   `catalogBrowse` 同规则。
3. Python `python/app/rpc/ingest.py`：`_authorize` 接受受众集合；**仅 Submit** 接受
   `ingest_adoption`，其余方法仍只接受 `ingest`；`database._validate_scope` 增加
   `required_admin_role`（adoption 为 None，仍校验签名/身份/店铺/revision 并加行锁）。
4. 文档：contracts 新增「建议采纳知识草稿」行；error-taxonomy 三个新码；openapi。
5. 测试：Go 包构造单测；Python `test_ingest_adoption.py`（普通 ingest 仍要
   merchant_admin；adoption 跳过该锁；未知受众拒绝）。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./internal/gateway/ -run "Adoption"
cd python && uv run --frozen pytest -q tests/test_ingest_adoption.py
bash scripts/check.sh
# 真实链路（dev，staff）：POST /v1/support/suggestions/<不存在>/adopt → 404
```

## 限制（本轮未覆盖）

- dev 为 mock 画像，无法产生可复制建议（`can_copy=false`），正向采纳的真实
  ingest 链路未在 dev 验证；由包构造单测 + Python 受众测试覆盖，正向端到端留待
  真实模型/隔离栈（WP79 演示或后续）。
- 采纳不新增表；provenance 通过确定性 document_id + 审计元数据追溯。

## 不要做

- 不自动审核/发布；不在审计写问题/回答/片段；
- 不加迁移、不新增 SECURITY DEFINER 函数、不直接写知识表；
- 不放宽 ValidatePackage/命令接口的受众；不改 smoke 基线。

## 汇报

五节格式；附端点 404 响应、Python/Go 测试输出、contracts NOTE 与棘轮处理说明。
