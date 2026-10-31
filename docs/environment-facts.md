# 环境事实（每条对应一次真实故障）

> 权威等级：enforced-by-discipline
> 只记"不知道就会踩坑"的事实。红线在 CLAUDE.md，分层与门禁在 docs/agent-entry.md。

## 1. 改 Go 代码：必须重建二进制并确认符号已进二进制

go build -o .local/v2-gateway ./cmd/gateway
systemctl restart ecommerce-rag-dev
strings .local/v2-gateway | grep -c <新符号>     # 必须 ≥1

只 restart 不 build，跑的还是旧二进制；smoke 全绿只证明旧代码能跑。
（事故 2026-10-31：setTrace 已提交但二进制不含该符号，smoke 通过、turn_decision 恒空。）

## 2. 改 Python（ingest / retrieval）：必须重启并核对启动时间

systemctl restart ecommerce-rag-dev
ps -eo lstart,cmd | grep app.ai_process

进程启动时间必须晚于最后一次提交时间。
（事故：to_literal 的 1-based 修复提交于 09:13，worker 仍是 09:00 的进程，修复后仍报 parse_failed。）

## 3. 迁移一旦在库上执行，一个字节都不能改

AI 服务启动时比对 schema_migration.checksum_sha256 与文件 sha256，不匹配直接报
ValueError: database migration checksums do not match this build，服务起不来。
补授权、加锁等一律新写迁移。
（事故：给已执行的 0037/0038 补 advisory lock，dev 栈起不来。）

## 4. 新建表必须授权，否则运行时才炸

CREATE TABLE 之后必须有 GRANT ... ON <table>（同文件或后续迁移）。
守卫 check-migration-grants 只做静态检查，正向路径无测试兜底。
（事故：0039 建 turn_decision 未授权，日志报 permission denied (42501)，smoke 全绿但表恒空。）

## 5. 验证性导入不要加 --publish

chunk 在 ingest 阶段就已落库，验证写入无需发布。一旦发布，该文档进入 active 集合，
改变检索结果并污染 smoke 基线。
（事故：answer 的 evidence 从 db-chunk-8 变成 db-chunk-8 + db-chunk-69，answer_chars 682 → 952。）

## 6. 五条 smoke 的基线位置

.local/w2/ 下（本地，不入库）：
before-smoke.txt（turn）、before-answer-smoke.txt、before-clarify-smoke.txt、
before-facts-smoke.txt、before-denied-smoke.txt。
bash scripts/check.sh --smoke 逐字节比对；无活栈时整层 UNCONFIGURED。
不许为了让比对通过而重采基线——基线变了说明行为变了，先解释再决定。

## 7. 门禁分层

bash scripts/check.sh          快速层 39 项（默认，必须全绿）
bash scripts/check.sh --smoke  加五条轮次链路（合并前必跑）
bash scripts/check.sh --db     加持久化层（需 /etc/ecommerce-rag/test-integration.env）
bash scripts/check.sh --e2e    加浏览器层（需隔离栈）

隔离栈：bash scripts/dev/isolated-stack.sh --identity development --ports 26081 --with-vite
