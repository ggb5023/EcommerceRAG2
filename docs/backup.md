# 备份与恢复

[项目入口](../README.md) · [安装](install.md)

一次可恢复备份必须包含 RAG 数据库、Keycloak 数据库、资料 artifact 目录、初始化配置与身份映射、HTTPS/RPC 信任材料、模型用量记录（`usage_log`）和所需密钥。秘密与数据正文都在仓外，备份保持 `0700` 目录、`0600` 文件及加密的离线副本；不能提交 Git。

维护窗口先停止 gateway、ai、ingest、worker 和 Keycloak 写入，再使用 [备份工具](../scripts/ops/selfhost-backup.py) 生成数据库 custom dump、资料与受限配置归档、SHA-256 清单。不要删除 volumes，也不要只备份 PostgreSQL 数据目录。`usage_log` 随数据库 custom dump 一起备份，不需要单独的账本文件。恢复必须使用同一受审 build、迁移 checksum 和资料版本。

使用 [恢复工具](../scripts/ops/selfhost-restore.py) 恢复到新的独立 Compose project，不能覆盖运行数据库。工具先验证清单 SHA-256 和归档路径，再创建空隔离 PostgreSQL 与受限服务角色，将两个 custom dump 恢复并核对所有公共表的行数、行内容指纹及扩展版本。原 artifact、身份配置与信任材料恢复在新的受限目录；`usage_log` 随数据库 dump 一并恢复，不重建、不清零，不修改源安装。

```bash
sudo python/.venv/bin/python scripts/ops/selfhost-backup.py \
  --runtime /var/lib/ecommerce-rag/merchant \
  --output /var/lib/ecommerce-rag/backup-new --maintenance-stopped
sudo python/.venv/bin/python scripts/ops/selfhost-restore.py \
  --backup /var/lib/ecommerce-rag/backup-new \
  --directory /var/lib/ecommerce-rag/restored-new \
  --project ecr-restore-verification
```

恢复后应用仍停止，原 Web 入口保持不变。检查新目录的 `restore-evidence.json`，确认角色、证书有效期及文件所有权，再安排身份和浏览器恢复验证与正式入口切换。不要同时在两套安装上写入同一店铺；丢失或损坏的 `usage_log` 不能靠重新初始化消除历史请求。

验收恢复时核对数据库结构与迁移 checksum、资料/版本/chunk 数量及 hash、客户和人工消息、来源撤销、匿名 Cookie 恢复、员工重新登录及队列重排。恢复验证和失败报告分别保留。恢复成功之前，备份不算可用；本文件不替代实际恢复证据。

## OSS 异地备份

本节面向工程维护：备份的是冻结评测语料、eval 资产与 git 历史，不含商家业务数据；商家备份见前文。

冻结评测语料、`eval/` 工程资产与 git 历史每天备份到阿里云 OSS（`ecr1-beijing`，`oss-cn-beijing`，
ZRS、版本控制、AES256）。备份前缀 `ecr-backup/`；`drill/` 下的旧演练数据只读保留，脚本不写入也不删除。

**备份范围**

- 语料目录下的 corpus/revision/current 子目录：冻结语料 5 个 revision 文件；目录不存在时只写带 `corpus_revision=absent`
  标记的清单与快照，不上传任何替代数据。
- `/var/lib/ecommerce-rag/eval/current/`：`eval/` 下的用例、契约、review 与校验脚本（排除 `__pycache__`、`.pytest_cache`、`*.pyc`）
  以及清单 `manifest.sha256`。
- `git/<short-sha>.bundle`：`git bundle create --all` 的整仓历史，上传前先 `git bundle verify`。
- `reports/<ts>/`：仅当显式传 `--with-reports DIR` 时归档；默认留空。

**不备份**：`/etc/ecommerce-rag/` 下的密钥、DSN、`.local/` 与任何运行时凭据。
如未来需要备份密钥，必须另开任务并先加密、另存密钥。

**清单与幂等**

清单是 `sha256sum` 兼容格式（`<sha256>  <相对路径>`），`#` 行记录生成时间、git 提交与
`corpus_revision=present|absent`。每次运行先拉取远端 `manifest.sha256` 逐对象比较：一致则打印
`NOOP` 并跳过上传；语料有变化时快照写到 `corpus/revision/snapshots/<UTC ts>.sha256`。
上传后逐对象回读并比对 sha256，不依赖 ETag（服务端加密/分片下 ETag 不等价）。

**运行**

```bash
sudo scripts/ops/oss-backup.sh              # 内网 endpoint（服务器）
sudo scripts/ops/oss-backup.sh --dry-run    # 只打印计划，不写入
sudo scripts/ops/oss-backup.sh --public-endpoint
```

凭据只从 `/etc/ecommerce-rag/oss.env`（0600）读取；脚本不打印、不记录、不上传其内容。
cron 每天 03:17 以 `flock -n /run/ecr-oss-backup.lock` 防重入，日志 `/var/log/ecr-oss-backup.log`。

**恢复**

```bash
sudo scripts/ops/oss-restore.sh --dest /tmp/ecr-restore --git-bundle
```

脚本按清单下载对象、写入 `manifest.sha256` 并逐个 `sha256sum -c`；git 侧优先读取 备份目录下的 git/latest 子目录 指针（bundle 名 + sha256），校验后 `git bundle verify` 并打印所选 bundle 与提交号；指针缺失时才按对象修改时间取最新；演练可用 `ECR_RESTORE_IGNORE_POINTER=1` 强制走该回退分支并打印警告与所选 bundle。语料存在时执行
评测语料的完整性检查与 `ECR_REQUIRE_EVAL` 的用法见 `docs/development.md` 的「评测与门禁」一节，语料缺失时明确 `SKIP` 并在
`--require-revision` 下失败。git bundle 用 `git bundle verify` 校验。恢复只写目标目录，不删除已有内容。

**状态含义**

- `NOOP`：远端清单与本地逐对象一致，本区未上传。
- `corpus_revision=absent`：语料目录不存在，跳过语料上传；这是“缺件”，不是“已备份”。
- `check.sh` 全绿不代表评测已验证；P1 验收要求见 `docs/development.md`。
