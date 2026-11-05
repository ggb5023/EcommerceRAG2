# 47 P1-7：商家库备份恢复演练

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：全局审查 P1-7（2026-11-04 本机私有快照，未入库）、
> [backup](../backup.md)、[constraints §2](../constraints.md)

## 目标

在隔离 project 中执行一次商家库备份 → 恢复 → 核对，证明数据库、迁移版本和
本地 artifact 可恢复；不触碰 dev 主库，不改变生产部署或备份脚本行为。

## 隔离范围

- 源 project：`ecr-wp47-source`，仅启动独立 PostgreSQL；使用
  `docker/selfhost/compose.yml` 的 `postgres` 服务与现有 `selfhost-backup.py`。
- 恢复 project：`ecr-restore-wp47`，由 `selfhost-restore.py` 创建，恢复到新的
  外部目录；应用保持停止。
- 数据：迁移生成的 schema + 少量合成行（`tenant`、`conversation`、`turn`、
  `conversation_execution`、`document_version`、`chunk`、`usage_log`、
  `turn_decision`）和一个本地 artifact 文件。
- OSS 远端对象存储不在本次范围；无 OSS 凭证，按 UNCONFIGURED 处理。

## 验收命令

```bash
# 源 project：初始化 + 迁移 + 合成数据
docker compose --env-file <runtime>/compose.env -f docker/selfhost/compose.yml up -d --wait postgres
docker compose --env-file <runtime>/compose.env -f docker/selfhost/compose.yml run --rm db-init
docker compose --env-file <runtime>/compose.env -f docker/selfhost/compose.yml run --rm migrate
# 合成数据后停止写入，创建备份
python3 scripts/ops/selfhost-backup.py --runtime <runtime> --output <backup> --maintenance-stopped

# 恢复到新的隔离 project
python3 scripts/ops/selfhost-restore.py --backup <backup> --directory <restored> --project ecr-restore-wp47
```

`<runtime>`、`<backup>`、`<restored>` 均为仓库外目录；证据保存在
`.local/wp47/restore-evidence.json`。核对项：

- 备份前后及恢复后行数/行内容 md5：`turn`、`conversation`、`document_version`、
  `chunk`、`usage_log`、`turn_decision` 等；
- `schema_migration` 记录与 `sql/migrations/*.sql` 文件 sha256 全部一致；
- 恢复目录 artifact 文件 sha256 与源文件一致；
- 备份 manifest 哈希、恢复数据库 evidence 与 `restore-evidence.json`。

## 棘轮与门禁

```bash
bash scripts/check.sh
bash scripts/check.sh --smoke
```

四类文档守卫见 [agent-entry §4](../agent-entry.md#4-验证命令)。

不调 `HANDLER_SQL_BASELINE`、smoke 基线或其它棘轮。

## 不做

- 不在 dev 主库上备份/恢复；不启动生产 Web/Keycloak；不改生产备份脚本。
- 不演练 OSS 远端对象存储，不写真实商家数据。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。
- 不改迁移、评测集、ROADMAP 或其它门禁。

## 未覆盖路径

- OSS 远端备份未演练（无凭证）。
- Keycloak/身份恢复、浏览器 Cookie、入口切换不在本次范围；数据库 evidence
  只覆盖 PostgreSQL 行数/指纹与迁移 checksum。
- 恢复后的隔离 project 在取证后删除；只有 `.local/wp47/` 证据保留。

## 实际交付

- 隔离源：`ecr-wp47-source`，只运行 `postgres` 服务；使用 49 条当前迁移，
  插入合成 `tenant` / `conversation` / `turn` / `conversation_execution` /
  `document_version` / `chunk` / `usage_log` / `turn_decision` 行及
  `wp47-artifact.txt`（位于 runtime 的 artifacts 目录）。
- 备份：`selfhost-backup.py --runtime /tmp/ecr-wp47-runtime --output
  /tmp/ecr-wp47-backup --maintenance-stopped` 输出
  `PASS restricted backup created; independent restore verification still required`。
- 恢复：`python/.venv/bin/python scripts/ops/selfhost-restore.py --backup
  /tmp/ecr-wp47-backup --directory /tmp/ecr-wp47-restored --project
  ecr-restore-wp47` 输出
  `PASS isolated database/file restore; applications remain stopped and Web entry unchanged`。
- 核对：`restore-evidence.json` 的 `database_restore_verified=true`、
  `all_tables_equal=true`、`migration_checksums.files=49` / `recorded=49` /
  `all_match=true`、artifact 源/恢复 sha256 相同。
- 证据位置：`.local/wp47/restore-evidence.json` 与
  `.local/wp47/backup-manifest.json`（本地、不入库、0600）。
- 清理：取证后 `ecr-wp47-source` 与 `ecr-restore-wp47` 均 `docker compose
  down -v`，对应容器、network、volume 和 `/tmp/ecr-wp47-*` 目录全部删除。
- OSS 远端对象存储：无凭证，`oss_remote.status=UNCONFIGURED`，仅演练本地
  PostgreSQL + filesystem archive。
- Keycloak 数据库在本次隔离源中未初始化，`keycloak.dump` 只验证了空库
  备份/恢复路径；身份数据、浏览器 Cookie 与入口切换未演练。
