# 07 测试分层剩余项（批次 2 / 批次 3 尾项）

> 状态：pending　适用提交：4a625b1　验收：见 §验收命令
> 详细规格：`docs/archive/testing-refactor-plan.md` §4（覆盖率）、§5.1（迁移从零建库）、§5.2（固定等待禁令）、§5.4（覆盖率实装）、§5.5（db-env-health）

## 目标

补齐 `--cov`、迁移从零建库、固定等待禁令、db-env-health 四项，使测试层能自证环境健康。

## 改动范围

- `scripts/check.sh`：`--cov` 实装（Go `-cover`、Python `coverage`）。
- `scripts/check/check-db-env-health.py`（待建）（新）：schema_migration 计数 + `document>0 && document_version=0` 探针。
- 测试内的固定 `sleep` 全部替换为轮询等待（清单见 §5.2）。

## 不碰

- 不改任何产品代码。
- 不提高既有覆盖率门槛来"凑绿"。

## 验收命令

详见 `docs/agent-entry.md` §4。本包额外：

```bash
bash scripts/check.sh --cov
```


## 行为变更

零。

## 未覆盖路径

- `--cov` 在无隔离栈时可能 UNCONFIGURED；需在文档里写明前置。
