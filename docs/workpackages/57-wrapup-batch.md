# 57 收尾批次：any 清零 + lint 范围 + 门禁语义 + 死代码 + 镜像迁移版本

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P2（2026-11-04 本机私有快照，未入库）、
> [WP56](../workpackages/56-ingest-dead-letter.md)、[environment-facts #1/#2](../environment-facts.md)

## 目标

关闭审计 P2 剩余的本地可处理挂账：清空 frontend 的 30 处 `any`、把 `python/tests`
纳入 ruff、让 `db-localauth` 真正跑 integration、删除无用 `confirmVersion`、核实
selfhost Python 镜像的迁移快照，并记录 CI 不跑 import 套件的取舍。

## 1. frontend any 清零

- 逐文件替换 30 处 `any`：新增 `AdminContext` / `AdminRow` / `Shop` /
  `DialogTarget` / `PrecheckItem` 等具体类型；模板和过滤器跟随类型收窄。
- `eslint-baseline.txt` 从 27 条 warning 下调到 0；`eslint-any-baseline.txt`
  记录 0 处原始 `any`；`eslint-baseline.mjs` 继续拒绝新增 warning。
- 每个文件一个提交；`npm run lint` 0 warning 0 error，`npm test` 与 `build` 全绿。

## 2. ruff tests 范围

- `python tests` 纳入 `check.sh` 的 `python-lint`（不新增 fast 项，计数仍 43）。
- 原有 32 条 errors 全部修复：import 排序、dict 字面量、未用 import/变量、
  nested with；没有加 noqa，也没有测试豁免。
- `uv run --frozen ruff check app tests ../eval ...` 全绿。

## 3. db-localauth 集成标签

- `db-localauth` 改为 `go test -tags integration ./internal/localauth`，
  使用 `V2_INTEGRATION_ENV`；本机 test-integration 库实跑通过。
- `docs/agent-entry.md` 改动类型表补 `localauth / DB 集成` 一行。

## 4. confirmVersion 死代码

- 删除 `go/internal/admin/version.go` 的 `confirmVersion`（无调用方）；
  没有相关测试需要删除。
- `ingestclient.FencingEpochMatches` 保留并补充注释：当前未接线，语义与 Python /
  数据库触发器一致，供未来 Go 侧 fencing 路径复用。

## 5. selfhost 镜像迁移版本

- 调查 `docker/selfhost/python.Dockerfile`：构建时 `COPY sql ./sql`，并设置
  `V2_MIGRATION_DIR=/app/sql/migrations`，因此重建即带当前迁移，不需要改代码。
- 本机旧镜像只有 31 条迁移；重新 `docker build` 后镜像内为 52 条（含 0052）。
- `docs/install.md` 写明“升级代码后先重建 ai 镜像再 up”，并给出旧快照风险。

## 6. CI import 套件取舍

- CI db job 不起 ingest 服务，`db-import-batch` / `db-import-customer` 保持
  UNCONFIGURED 属既定取舍；workflow 注释与 WP57 记录。
- 远端 Actions 首跑仍需 push 后确认。

## 验收命令

```bash
cd frontend && npm run lint && npm test && npm run build
cd python && uv run --frozen pytest -q
cd python && uv run --frozen ruff check app tests ../eval \
  ../scripts/check/artifact-cleanup-fault-smoke.py ../scripts/dev/v2_services.py
cd go && go test -count=1 ./...
cd go && V2_INTEGRATION_ENV=/etc/ecommerce-rag/test-integration.env \
  go test -tags integration ./internal/localauth
bash scripts/check.sh
bash scripts/check.sh --smoke
```

## 验证结果（2026-11-05）

- frontend：`npm run lint` → `0 warnings, 0 errors`；`npm test` → 57 passed；
  `npm run build` 通过。`grep -Rno '\bany\b' src` → 0。
- Python：454 passed, 37 skipped；`ruff check app tests …` → All checks passed。
- Go：`go test -count=1 ./...` → 23 package 结果，0 FAIL；`go vet` 通过。
- `db-localauth` integration：TestVerify/Parse/Session/Setup/Disable 等 15 个
  integration 用例 PASS（`ok ... 0.852s`）。
- 镜像：`docker build` 成功；镜像内 `ls /app/sql/migrations/*.sql | wc -l` = 52，
  最后两条为 0051/0052；旧 `ecommerce-rag-python:selfhost` 为 31。
- `bash scripts/check.sh` → `PASSED 43 / UNCONFIGURED 0 / FAILED 0`。
- `bash scripts/check.sh --smoke` → `PASSED 46 / UNCONFIGURED 0 / FAILED 0`，
  五条 diff 逐字节一致。

## 不做

- 不新增迁移；不改产品行为/错误码；不调棘轮/基线（ESLint 基线只下调）；
  不重采 smoke；不 push；不用 `git add .`。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- 远端 GitHub Actions 首跑未验证；`db-import-*` 仍需本地/隔离栈的 ingest endpoint。
- `ingestclient.FencingEpochMatches` 仍无 Go 运行时调用方，只有默认层测试。
- 真实模型、OSS、Keycloak、e2e 不在本轮范围。
