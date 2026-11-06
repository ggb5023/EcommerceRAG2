# 80 摄取 scope 行锁回归修复

- 状态：见 INDEX.md
- 依据：WP75 引入的 `FOR SHARE OF u,t,s` 在 CI db 层报 `permission denied for table tenant`
- 前置：WP79 已关

## 目标

`python/app/ingest/database.py:_validate_scope` 的共享 SELECT 不再对
`tenant/app_user/shop` 加行锁：`rag_v2_ingest` 只有 `SELECT` 权限，拿不到行锁。
正常 ingest 的串行化仍由 `ecr_lock_ingest_scope`（SECURITY DEFINER）在
`required_admin_role is not None` 时完成；adoption 路径不加锁，但保留签名身份、
店铺、revision、角色与撤销校验。

## 步骤

1. 去掉 `_validate_scope` 查询末尾的 `FOR SHARE OF u,t,s`；
2. `python/tests/test_v2_ingest_database.py` 增加真实 DB 用例
   `test_adoption_submission_runs_without_row_lock_privileges`：
   先以 ingest 角色证明无法 `FOR SHARE tenant`，再以 `required_admin_role=None`
   成功提交；revision 过期仍被拒；
3. 不改迁移、不改棘轮、不改门禁。

## 验收命令

```bash
# 以 integration 环境文件设置 V2_INTEGRATION_ENV（写法见 docs/agent-entry.md §4）
cd python && uv run --frozen pytest tests/test_v2_ingest_database.py -q
cd python && uv run --frozen pytest tests/test_ingest_adoption.py -q
bash scripts/check.sh
```

## 限制 / 未运行

- 本机 `test-integration.env` 缺 `V2_INGEST_DSN`/`V2_AI_DSN` 时 `--db` 的
  db-python 会被跳过；本轮用角色密码补齐到临时 env 文件后实跑。

## 不要做

- 不新增/修改迁移；不恢复行锁；不放宽身份与店铺校验。
