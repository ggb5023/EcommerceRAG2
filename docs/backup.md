# 备份与恢复

[项目入口](../README.md) · [安装](install.md)

一次可恢复备份必须包含 RAG 数据库、Keycloak 数据库、资料 artifact 目录、初始化配置与身份映射、HTTPS/RPC 信任材料、模型预算账本和所需密钥。秘密与数据正文都在仓外，备份保持 `0700` 目录、`0600` 文件及加密的离线副本；不能提交 Git。

维护窗口先停止 gateway、ai、ingest、worker 和 Keycloak 写入，再使用 [备份工具](../scripts/selfhost-backup.py) 生成数据库 custom dump、资料与受限配置归档、SHA-256 清单。不要删除 volumes，也不要只备份 PostgreSQL 数据目录。账本可能仍被其他进程使用，备份使用 SQLite 在线备份 API，而不是复制运行中的 sqlite 文件。恢复必须使用同一受审 build、迁移 checksum 和资料版本。

使用 [恢复工具](../scripts/selfhost-restore.py) 恢复到新的独立 Compose project，不能覆盖运行数据库。工具先验证清单 SHA-256 和归档路径，再创建空隔离 PostgreSQL 与受限服务角色，将两个 custom dump 恢复并核对所有公共表的行数、行内容指纹及扩展版本。原 artifact、身份配置、信任材料与 SQLite 账本恢复在新的受限目录；不会重建或清零预算窗口，不修改源安装。

```bash
sudo python/.venv/bin/python scripts/selfhost-backup.py \
  --runtime /var/lib/ecommerce-rag/merchant \
  --output /var/lib/ecommerce-rag/backup-new --maintenance-stopped
sudo python/.venv/bin/python scripts/selfhost-restore.py \
  --backup /var/lib/ecommerce-rag/backup-new \
  --directory /var/lib/ecommerce-rag/restored-new \
  --project ecr-restore-verification
```

恢复后应用仍停止，原 Web 入口保持不变。检查新目录的 `restore-evidence.json`，确认角色、证书有效期及文件所有权，再安排身份和浏览器恢复验证与正式入口切换。不要同时在两套安装上写入同一店铺或使用同一恢复账本承诺独立预算；丢失或已损坏的账本不能以重新初始化消除历史请求。

验收恢复时核对数据库结构与迁移 checksum、资料/版本/chunk 数量及 hash、客户和人工消息、来源撤销、匿名 Cookie 恢复、员工重新登录及队列重排。恢复验证和失败报告分别保留。恢复成功之前，备份不算可用；本文件不替代实际恢复证据。
