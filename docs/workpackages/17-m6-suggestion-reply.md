# 17 M6 客服工作台建议回复——阶段 2 实现

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[0031](../decisions/0031-m6-suggestion-reply.md)（本包唯一规格）、
> [0005](../decisions/0005-copyable-reply-policy.md)、[0028](../decisions/0028-orchestration-boundary.md)

## 目标

实现 M6 首版：粘贴顾客问题或从 `/support` 会话生成建议回复，展示引用卡片，
满足 0005 时一键复制。建议生成复用 Go turn 引擎；不自动发送、不写顾客消息。

## 范围

- 迁移 0045：`conversation.kind` / `turn.kind`（`user`/`suggestion`）。
- 指标排除：outcomes / gaps 与员工会话列表只含 `kind='user'`。
- `can_copy`：终态事务按 DONE、非 mock、引用数 ≥1 计算；mock/无引用/非 DONE 分别
  写 `mock` / `no_citable_evidence` / `incomplete`。
- 建议端点：
  - `POST /v1/support/suggestions`（粘贴）；
  - `POST /v1/support/conversations/{id}/suggestions`（最后一条顾客消息）；
  - `POST /v1/support/suggestions/{id}/copy`（复核 + `admin_audit`）。
- 前端 `SuggestionPanel.vue` + `SupportDesk.vue`：生成、引用卡片、复制禁用态/原因。
- 契约表新增“建议结果/引用”；0031 转已采纳。

## 隐藏会话

每个员工/店铺一条 `kind='suggestion'` 的隐藏 `conversation`，建议 turn 也标记
`kind='suggestion'`；不进入员工会话列表、outcomes、gaps。保留/清理策略**延后**，
累积的隐藏会话与建议消息是已知债务（本包不做清理任务）。

## 验收命令

```bash
# 迁移从零与三库
ECR_SCRATCH_DSN=... ECR_SCRATCH_GATEWAY_DSN=... bash scripts/ops/verify-migrations-from-zero.sh
python/.venv/bin/python scripts/ops/v2-migrate.py --env /etc/ecommerce-rag/migration.env
python/.venv/bin/python scripts/ops/v2-migrate.py --env /etc/ecommerce-rag/test-integration.env
python/.venv/bin/python scripts/ops/v2-migrate.py --env /etc/ecommerce-rag/eval-migration.env

cd go && go build -o ../.local/v2-gateway ./cmd/gateway && go vet ./...
cd go && go test ./internal/...
bash scripts/check.sh
bash scripts/check.sh --smoke
cd frontend && npm test && npm run build
```

## 不做

- 不自动发送、不写顾客侧消息、不改 `customer_conversation` 状态；
- 不做隐藏会话保留/清理、多轮建议、批量建议；
- 不改 Python 提示词/检索；不调棘轮/基线、不重采 smoke；不做阶段 3；不 push。

## 未覆盖路径

- dev 档是 `v2_langchain_mock`：建议接口产出的 `is_mock=true`，按 0005 永远
  `can_copy=false`；可复制正向路径用非 mock 数据库 fixture 验证，真实模型质量未验证。
- 隐藏会话不清理，长期会累积；本包只保证不进入指标/列表。
- 未做真实平台发送、批量/多轮、跨员工协作。
