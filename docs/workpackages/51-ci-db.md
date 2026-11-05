# 51 P2：CI 增加数据库层

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[全局审查 P2](../audits/2026-11-04-global-audit.md)、
> [agent-entry §4](../agent-entry.md)、[development 测试分层](../development.md)

## 目标

在 GitHub Actions 的 fast job 之外增加独立 DB job：用固定 pgvector/PostgreSQL
service container 初始化角色/库/扩展，跑迁移和 `scripts/check.sh --db`，让数据库
集成层进入 CI；smoke 仍为本地合并前必跑项，不在本阶段伪装成 CI 已覆盖。

## Workflow 设计

- `.github/workflows/check.yml` 增加 `db` job，`runs-on: ubuntu-24.04`；
  service `postgres` 使用固定 digest 的 pgvector PostgreSQL 16 镜像；
- 安装 toolchains、`postgresql-client`、Python venv；数据库密码在 runner 临时生成，
  不写入仓库；
- 初始化 `ecr_v2` 角色/库/扩展，跑到当前迁移；再建空的 `scratch` 库供
  migrations-from-zero；
- 生成临时 `test.env` / `import.env`，设置 `ECR_SCRATCH_DSN` 与
  `ECR_SCRATCH_GATEWAY_DSN`；import 测试缺 ingest endpoint 时按 UNCONFIGURED
  跳过，不伪造通过；
- 运行 `bash scripts/check.sh --db`，断言 summary `FAILED 0`；不运行 smoke。

## 本机等价验证

在 dev docker PostgreSQL 上创建独立 `ecr_v2_ci_*` 库，按 workflow 顺序执行：
创建库/扩展 → 迁移 → 生成临时 env → `bash scripts/check.sh --db`。完成后删除所有临时库，
不改 dev 主库数据。

## 验收命令

```bash
# YAML 解析 + 无凭据痕迹
python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/check.yml"))'
grep -Rni 'password\|dsn\|secret' .github/workflows/check.yml

# 本机等价
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：本机 `check.sh --db` 的 db-init/migrate/db 层输出；文档守卫全过。

## 不做

- 不放真实凭据、DSN、密钥或任何生产地址；不把 smoke 放进 CI 并宣称已跑。
- 不改产品代码、迁移、评测集；不调棘轮/基线；不重采 smoke。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 未覆盖路径

- 远端 GitHub Actions 是否启用无法在本机验证；文件提交后首跑仍需外部确认。
- smoke / e2e 仍为本地合并前必跑项，CI DB job 不启动应用栈。
- 不验证真实模型、OSS 或 Keycloak。
