# 脚本索引

[项目入口](../README.md) · [开发指南](../docs/development.md)

`bash scripts/check.sh` 是唯一的全量检查入口。其余脚本按用途分在四个目录里，新增脚本放进对应目录，并在本表登记。一次性的调研或验收脚本不进仓库。

| 目录 | 用途 | 主要入口 |
| --- | --- | --- |
| `check/` | 被 `check.sh` 或 git hooks 调用的检查 | `privacy_guard.py`（pre-commit、pre-push）、`check-doc-baseline.py`、`check-docs.py`、`check-proto.sh`、`provider-smoke.py`、`facts-fake-matrix.sh` |
| `dev/` | 本机开发：环境、启动、契约生成、迁移测试 | `bootstrap-dev.sh`、`v2-local-services.sh`、`generate-proto.sh`、`test-migrations.sh`、`provider-budget.py`、`install-git-hooks.sh` |
| `ops/` | 自部署安装、迁移、备份恢复、私有发布 | `selfhost.py`、`v2-migrate.py`、`selfhost-backup.py`、`selfhost-restore.py`、`publication_archive.py` |
| `db/` | 在 DB 服务器上运行（`/opt/ecommerce-rag-ops`），由 `package-ops.sh` 打包 | `bootstrap-db.sh`、`backup-db.sh`、`verify-db.sh`、`db-firewall.sh` |

`ops/` 中的 `v2-migrate.py`、`selfhost-bootstrap.py`、`selfhost-health.py` 会被复制进 Python 镜像的 `/app/scripts/`。修改这些脚本时，要一并检查 `docker/selfhost/` 下的配置。
