# 53 P2：死代码/死表清理（先盘点，再按类别处理）

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P2 表](../audits/2026-11-04-global-audit.md)、
> [数据归属](../data-ownership.md)、[0032 抓取与 Tavily](../decisions/0032-intelligence-domain-and-crawling.md)

## 目标

对全局审查列出的七张疑似死表和 crawler/MinerU/Tavily 三个未接 runtime 的模块做
可复核盘点，按“真死 / 预留 / 不确定”分类；删除无争议的死 Python 代码，死表只登记
DROP 待授权清单，本包不执行 DROP、不新增迁移。

## 范围

- 七张表：`ingest_stage`、`ingest_package_file`、`ingest_package_upload`、
  `eval_case`、`eval_run`、`provider`、`provider_model`。
- 三个模块：`python/app/web/crawler.py`、`python/app/providers/mineru.py`、
  `python/app/providers/tavily.py`。
- 盘点写进本文件的表；删除只限零 runtime 引用且无决策保留的项。
- 文档与 research/refactor-input 的引用同步。

## 分类口径

- **A 真死**：当前代码、查询、视图、触发器均无引用，且无预留/决策依据；登记 DROP
  待授权。
- **B 预留**：有明确 P3/后续保留依据；文档标注，保留。
- **C 不确定**：证据不足或与决策冲突；停下报告，不删。

## 盘点结果

证据命令（只读）：

```bash
git grep -n -w <table> -- . ':!sql/migrations/*' ':!docs/*'
rg -n 'from app\.web|import app\.web|app\.web\.crawler' python/app
rg -n 'from app\.providers(\.| import )?(mineru|tavily)' python/app
git log --all --oneline -S 'ingest_package_upload' -- python go scripts frontend
psql "$V2_GATEWAY_DSN" -X -At -c "SELECT count(*) FROM <table>"
```

`provider` 的普通标识符匹配很多（Go/Python 变量、fact provider），上表结论只按
**SQL/表名上下文**归类，不把普通变量名算作表引用。

### 七张表

| 表 | 创建迁移 | 当前代码/查询引用 | 最后写方 | dev 行数 | 结论 |
| --- | --- | --- | --- | --- | --- |
| `ingest_stage` | `0001_init.sql`，0002/0022 授权 | 0；仅迁移中的建表/外键/GRANT | 无（`git log -S` 无写入历史） | 0 | **A 真死** |
| `ingest_package_file` | `0013_durable_ingestion_worker.sql` | 0；仅迁移 | 历史 `go/cmd/server`，1cd64f2 移除 | 0 | **A 真死** |
| `ingest_package_upload` | `0013_durable_ingestion_worker.sql` | 0；仅迁移 | 历史 `go/cmd/server`，1cd64f2 移除 | 0 | **A 真死** |
| `eval_case` | `0001_init.sql` | 0；仅建表 | 无 | 0 | **A 真死** |
| `eval_run` | `0001_init.sql` | 0；仅建表 | 无 | 0 | **A 真死** |
| `provider` | `0034_tenant_providers.sql` | 0 个表引用；0034/0019/data-ownership 标 P3 预留 | 无（预留） | 0 | **B 预留，保留** |
| `provider_model` | `0034_tenant_providers.sql` | 0 个表引用；同上 | 无（预留） | 0 | **B 预留，保留** |

补充证据：

- 五张 A 表在 dev 库均 0 行；`provider` / `provider_model` 也 0 行，但有 0034 的
  P3 预留与 `data-ownership.md` 的写入方声明，不能按死表删。
- `pg_constraint` 只显示 `provider_model -> provider` 的外键；五张 A 表没有被其它表
  引用，也没有用户视图/触发器/函数引用。
- `ingest_package_file/upload` 的旧读写代码在 `1cd64f2` 的 `go/cmd/server` 拆分中
  整体删除；当前 `git grep` 无命中。
- `eval_case/eval_run` 只有 `0001_init.sql` 建表，没有代码、评测脚本或查询使用。

### 三个模块

| 模块 | 行数 | runtime 引用 | 测试 | 结论 |
| --- | --- | --- | --- | --- |
| `python/app/web/crawler.py` | 478 | 0（`python/app` 无 import） | `python/tests/test_crawler.py`（263 行） | **删除候选；无决策保留** |
| `python/app/providers/mineru.py` | 365 | 0（仅测试 import） | `python/tests/test_external_providers.py` | **保留**：0007 明确作为离线实验能力保留 |
| `python/app/providers/tavily.py` | 163 | 0（仅测试 import） | `python/tests/test_external_providers.py` | **保留**：0032 明确保留复用，未接线 |

`crawler-html-v1` fixture 仍被 `python/tests/test_source_fixture_pipeline.py` 使用，
它是 HTML 解析夹具，不是 `crawler.py` 的 runtime 引用，删除模块时保留。

### 分类结论与 DROP 待授权清单

- **A 真死（待授权 DROP）**：`ingest_package_file`、`ingest_package_upload`、
  `eval_run`、`eval_case`、`ingest_stage`。
- **B 预留（保留）**：`provider`、`provider_model`。
- **C 不确定**：0 项，无阻塞。
- **DROP 顺序建议**：`ingest_package_file` → `ingest_package_upload` → `eval_run` →
  `eval_case` → `ingest_stage`；用新迁移，不修改已执行迁移，执行前另获主 agent 授权。
- **删除清单（本包 §3 执行）**：`python/app/web/crawler.py`、`python/tests/test_crawler.py`；
  `python/app/web/__init__.py` 若只含包注释也一并删除。MinerU/Tavily 不删。

## 验收命令

```bash
cd python && uv run --frozen pytest -q
cd go && go test ./...
bash scripts/check.sh
bash scripts/check.sh --smoke
```

盘点证据使用 `git grep`、`psql` 只读计数、`rg` 引用图和 `git log -S`。

## 不做

- 不 DROP 表、不新增迁移；不删 provider/provider_model 预留；不删 Tavily。
- 不删有决策保留的 MinerU；不删 crawler 相关的 HTML fixture（只删模块与其测试）。
- 不调棘轮/基线；不重采 smoke；不用 `git add .`；不 push。
- 提交身份：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 未覆盖路径

- 远端 GitHub Actions 未验证；smoke / e2e 仍为本地合并前必跑项。
- 真实模型、OSS、Keycloak 不在本轮范围。
- DROP 表的实际迁移与执行留待主 agent 单独授权。
