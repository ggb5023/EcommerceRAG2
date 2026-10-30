# 部署、发布与备份恢复（重构方案 · 运维层）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充总纲未覆盖的**运行形态**维度：部署编排、升级回滚、健康检查、备份恢复。

---

## 0. 现状（2026-10-27 实测）

| 项 | 现状 |
| --- | --- |
| Compose 长期服务 | `gateway`、`ai`、`postgres`（3 个） |
| Compose 一次性任务 | `db-init` → `migrate` → `bootstrap`（`service_completed_successfully` 串联） |
| 可选 profile | `keycloak`（`oidc`）、`redis`（`cache`） |
| 镜像 pin | 5 个镜像中 **3 个**用 `@sha256` 固定，`ecommerce-rag-python:selfhost` 与 `ecommerce-rag-go:selfhost` 用本地 tag |
| 健康检查 | gateway 用 `/probe`（HTTPS + CA + 正确 Host 头）；ai 用 `selfhost-health.py` |
| 本机 dev | systemd：`ecommerce-rag-web`（Vite 5173）+ `ecommerce-rag-dev`（gateway + ai 单进程）；撰写时 `ecommerce-rag-dev` 处于 inactive |
| 备份 | `selfhost-backup.py`（pg_dump + runtime 打包 + manifest 哈希）、`oss-backup.sh`（语料 + eval 树 + git） |
| 恢复验证 | `restore_verified: false`，**未演练** |
| OSS 通道 | **坏的**：`oss.env` 缺 `OSS_REGION`、endpoint 无 scheme、`oss2` 未安装 |

---

## 1. 问题清单

### O1 两套运行形态没有明确边界

dev 用 systemd + Vite，自托管用 Compose + gateway 内置静态。两套的命令、端口、日志位置都不同，
`docs/install.md` 只写了后者的"首次安装"，没写"日常升级"。

### O2 镜像没有版本标识，回滚只能靠重建

本地 tag 固定为 `:selfhost`，无法区分"上一版"。出事要回滚时没有可指回的镜像。

### O3 部署后没有冒烟脚本

`check.sh` 是开发门禁（不连真栈）；部署完成后没有任何一条命令能回答"这套栈现在健康吗"。
`/probe` 只回答 gateway 自身。

### O4 升级与回滚流程缺失

没有"升级前备份、升级后冒烟、失败回滚"的固定步骤；迁移只增不改，但没有"这次升级引入哪几条迁移"的清单。

### O5 备份恢复从未演练

见数据层 D-W4。运维层负责**流程**，数据层负责**数据一致性断言**。

---

## 2. 工作包

### O-W1　版本化发布（半天）

1. 镜像 tag 规则：`ecommerce-rag-{python,go}:<git-sha>`，同时保留 `:selfhost` 作为"当前版本"别名
2. `docker/selfhost/compose.env` 增加 `ECR_IMAGE_TAG`
3. 发布时写入 `.local/release/<sha>/manifest.json`：镜像 digest、迁移列表、构建时间

验收：

```bash
docker images --format '{{.Repository}}:{{.Tag}} {{.Digest}}' | grep ecommerce-rag
# 期望：既有 :<sha> 也有 :selfhost，且 digest 一致
```

### O-W2　部署冒烟脚本 `scripts/ops/deploy-smoke.sh`（半天）

一条命令回答"这套栈是否可服务"，只读、不改数据：

```
1) /healthz 200 且 JSON status=ok
2) 未登录访问 /v1/session → 401
3) 顾客通道：POST /v1/customer/session（带 Origin）→ 201
4) 静态首页 GET / → 200 且 content-type text/html
5) postgres: SELECT 1；ai: gRPC health
6) 退出码聚合：任一失败 exit 1，并打印失败项
```

验收：部署后 `bash scripts/ops/deploy-smoke.sh` → `PASS 6/6`。

### O-W3　升级与回滚手册（2 小时）

写进 `docs/install.md` 新增「升级」与「回滚」两节：

```
升级：backup → pull → build（新 sha tag）→ migrate（只增）→ up -d → deploy-smoke
回滚：up -d 指回上一版 sha 镜像 → 校验 schema 兼容（迁移只增，旧代码必须能读新表）→ deploy-smoke
```

回滚的硬约束：**旧版本代码必须能在新 schema 上运行**（只增不改的含义），这条要写进红线。

### O-W4　备份恢复演练（半天，与数据层 D-W4 同一件事）

运维层产出：演练记录 + 耗时 + 恢复后冒烟结果；数据层产出：一致性断言。

### O-W5　OSS 通道修复（2 小时）

`oss.env` 补 `OSS_REGION`、endpoint 改 `https://oss-<region>.aliyuncs.com`、安装 `oss2`；
`oss-backup.sh --dry-run` 先跑通再真传；`oss-restore.sh` 拉回并比对哈希。

---

## 3. 红线

1. 发布必须可回滚：任何一次 `up -d` 之前，上一版镜像必须还在本地
2. 迁移只增不改，保证旧代码能在新 schema 上跑
3. 冒烟脚本只读，不得创建业务数据（除临时顾客会话，且用完即弃）
4. 备份产物与密钥不进 Git；`oss.env` 权限保持 0600

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 镜像版本 | `docker images | grep ecommerce-rag` | 有 `:<sha>` 与 `:selfhost` |
| 部署冒烟 | `bash scripts/ops/deploy-smoke.sh` | `PASS 6/6` |
| 升级演练 | 按手册走一遍 | 冒烟通过、迁移数正确 |
| 回滚演练 | 指回上一版 | 冒烟通过 |
| OSS 备份 | `bash scripts/ops/oss-backup.sh` | 上传成功、哈希一致 |
