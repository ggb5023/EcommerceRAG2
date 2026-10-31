# Claude Code 入口

@docs/agent-entry.md
@docs/environment-facts.md

MUST READ FIRST: `docs/agent-entry.md`（113 行，仓库地图 / 分层 / 39 项门禁 / 验证命令 / 红线 / 索引）。
本文件只补充 Claude Code 特有的三条约定，其余以 agent-entry 为准。

## 1. 工作单位是工作包

`docs/workpackages/<NN>-*.md`，每份 ≤150 行，含验收命令与"不要做"。
每轮开始先读 `docs/workpackages/CURRENT`。
一次只做一个工作包；做完停下汇报，等我授权再进下一个。

## 2. 汇报固定五节（**只在交付时用；排查、讨论、探索时按正常方式回答**）

1. 完成内容  2. 改动文件  3. 验证结果（贴真实命令与真实输出）
4. **未运行项和限制 / 未覆盖路径**（必须点名，不许省略）
5. 下一步

数字必须附可复现命令。"实测"两个字不算证据。

## 3. 红线（违反即回滚）

- 不许调高任何棘轮/预算/基线（`UNDECLARED_BUDGET`、`CONTRACTS_*`、`WARN_BUDGET`、
  `STAGE_FUNC_BASELINE`、`RUN_TURN_BUDGET`）；确需变更先说明原因并单独提交。
- 不许为了让门禁通过而改断言或重采基线。
- 不许修改已执行过的迁移文件（`sql/migrations/00*`）；改一个字节会让 AI 服务启动失败。
- 不许 `git push` / `git merge` / 删分支，除非明确授权。
- 提交身份固定：`EcommerceRAG Maintainers <maintainers@example.invalid>`。

## 4. 验证顺序（缺一不可）

```bash
bash scripts/check.sh          # 39 项快速层
bash scripts/check.sh --smoke  # 5 条轮次链路 smoke，与 .local/w2/ 基线逐字节比对
go build ./... && go vet ./...
```
改了 ingest / retrieval 代码，验证前先重启服务；改了 Go 代码，先 `go build` 再重启。
