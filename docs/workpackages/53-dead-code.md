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
