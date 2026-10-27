# 数据归属与迁移纪律（重构方案 · 数据层）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充 `refactor-assessment.md` 未覆盖的**数据层**维度。与后端方案的 P-B 有交集，边界见 §5。

---

## 0. 现状（2026-10-27 实测）

| 项 | 数值 |
| --- | --- |
| 迁移 | 35 条（0001–0035），只增不改已执行良好；**0036 起由本方案约束**（M8 的 0034/0035 已合并前完成） |
| 迁移中 `CREATE TABLE` 语句 | 66 处（含后续迁移的新增与重建） |
| 数据库角色 | `ecr_migrator`（非超管、无 BYPASSRLS）、`rag_app`、`rag_v2_gateway`、`rag_v2_ai`、`rag_v2_ingest` |
| 审计/事件表 | `admin_audit`、`local_auth_event`、`customer_event`、`sse_event`、`tool_call_trace`、`usage_log` |
| 备份脚本 | `selfhost-backup.py`（pg_dump `ecr_v2` + `keycloak` + runtime 打包 + manifest 哈希）、`oss-backup.sh`（评测语料 + eval 树 + git 历史）、`oss-restore.sh` |
| 恢复状态 | manifest 里 `restore_verified: false`，**从未做过真实恢复演练** |
| 数据归属文档 | **缺失**（`docs/data-ownership.md` 不存在） |

---

## 1. 问题清单

### D1 表归属没有权威文档（必修）

- 现象：`app_user` / `admin_audit` 在 `admin` 与 `localauth` 两处 INSERT（生产代码，非测试），0004 红线事实上失效
- 风险：新功能随手再开第三个写入方，没人能发现
- 判定：这不是"代码问题"，是"缺一张表"的问题

### D2 迁移只能前进，缺"从零建库"的自动验证

- 现象：M8 期间出现过"改了未合并的迁移、导致已跑过它的测试库 checksum 不一致"（0030 那次）；目前靠人工记忆重建库
- 风险：新环境部署失败只能靠猜

### D3 新表授权容易漏

- 现象：0030 加三张表时漏了 `app_user_id_seq` 的序列授权，靠 CLI 报错才发现
- 风险：新增表的 GRANT 没有机械检查，漏了就等运行时炸

### D4 备份存在但"恢复未验证"

- 现象：`manifest.json` 里 `restore_verified: false`；OSS 通道自身还是坏的（`oss.env` 缺 `OSS_REGION`、endpoint 无 scheme、`oss2` 未安装）
- 风险：真出事时不知道备份能不能用

---

## 2. 工作包

### D-W1　数据归属表（半天）

产出 `docs/data-ownership.md`，每张表一行：

| 表 | 唯一写入方 | 读取方 | 依据迁移 | 备注 |
| --- | --- | --- | --- | --- |
| `app_user` | `internal/admin` | gateway/localauth | 0001/0005/0030 | localauth 改为调用 admin store |
| `admin_audit` | `internal/admin` | admin | 0010 | 同上 |
| `turn` / `sse_event` / … | `internal/turn`（重构后） | gateway | 0001/0010 | W2 完成后更新 |
| `customer_*` | `internal/customer` | gateway | 0026 | 不得与 turn 互写 |

验收：

```bash
# 每个 INSERT/UPDATE 目标表都必须出现在归属表里
grep -rhoE 'INSERT INTO [a-z_]+|UPDATE [a-z_]+ SET' go/internal --include='*.go' \
  | grep -v _test | awk '{print $3}' | sort -u > /tmp/written.txt
grep -oE '^\| `[a-z_]+`' docs/data-ownership.md | tr -d '|` ' | sort -u > /tmp/documented.txt
comm -23 /tmp/written.txt /tmp/documented.txt   # 期望为空
```

### D-W2　迁移从零建库测试（半天）

新增 `go/internal/admin/migrations_from_scratch_test.go`（`-tags integration`）：

```
1) 建临时库 ecr_v2_scratch_<pid>
2) 按文件名升序 apply 全部迁移
3) 断言 schema_migration 行数 == 迁移文件数，且 checksum 全部 PASS
4) 无论成败都 DROP 临时库
```

验收：`V2_INTEGRATION_ENV=<env> go test -tags integration ./internal/admin -run MigrationsFromScratch -v`

### D-W3　授权机械检查（2 小时）

`scripts/check/check-grants.py`：解析迁移文件，若出现 `CREATE TABLE x` 而同一迁移里没有 `GRANT ... ON x` 或 `REVOKE ... ON x`，则失败。
豁免：表名前缀为 `schema_migration`、或在同文件显式 `-- no-grant: <理由>` 注释。

接入 `check.sh`，验收：`python3 scripts/check/check-grants.py` 输出 `PASS`（先给存量表补白名单）。

### D-W4　恢复演练（半天，依赖 OSS 修复）

1. 修 `oss.env`（补 `OSS_REGION`、endpoint 加 `https://`、`uv pip install oss2`）
2. `selfhost-backup.py` → 产出 dumps + manifest
3. 在**独立 project + 独立库**上 `selfhost-restore.py` 恢复
4. 断言：表数一致、`schema_migration` 35 行、随机抽 3 张表行数一致
5. 把 `restore_verified` 置真并把演练记录写进 `docs/backup.md`

---

## 3. 红线

1. 不改已合并的迁移文件；结构变化只能新增
2. 迁移用户必须保持非超管、无 `BYPASSRLS`
3. 新增表必须同时给出授权，且写入方在 `data-ownership.md` 里有且只有一个
4. 备份产物只能放仓库外（`.local/` 或 `/var/lib/ecommerce-rag/`），不进 Git

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 归属覆盖 | 见 D-W1 的 `comm` | 空 |
| 从零建库 | `go test -tags integration -run MigrationsFromScratch` | PASS |
| 授权完整 | `python3 scripts/check/check-grants.py` | PASS |
| 恢复演练 | `selfhost-restore.py --manifest <path>` | 表数与行数一致 |

## 5. 与其它文档的边界

- **后端方案 P-B** 负责"代码怎么改"（SQL 搬进 store、localauth 调用 admin store）；本文件负责"改完归谁、怎么验"
- `docs/data-ownership.md` 是 P-B 的**产出物**，不是另起一份
