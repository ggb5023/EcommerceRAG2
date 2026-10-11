# 交接简报

## 当前进度

V1→V2 全量重构已按用户方案启动，工作树为 `refactor/v2-rebuild`。既有未提交业务实现经 Go/Python 回归审阅后保存为 `8b54891`。能力迁移映射见私有 `docs/V1到V2能力迁移清单.md`，执行记录见 `archive/20261011-v2-refactor-implementation.md`。

Python 新增资料控制命令与事务内 receipt、artifact journal、向量摄取、独立 RPC 与 worker 入口。独立空库已应用 24 个 migration 且 checksum 校验通过；隔离数据库写入权限与摄取故障测试通过。资料命令 RPC 已接入 Go 管理接口；Go 控制面已拆为 `cmd/gateway` 与 `internal/{gateway,admin,auth,facts,platform,httpapi}`。新 Python AI/Ingest/worker 入口已接入 Dockerfile、活动 smoke 和跨进程测试；旧 `app.server` 与 `app.v2_grpc_server` 仅保留兼容测试用途，禁止活动运行。实际空库 Gateway 启动、四进程资料全链路和浏览器全链路仍未验收，不能据此标记整个重构完成。

## 下一步任务

完成 Go 命令 RPC 的空库启动验收，补齐四进程独立启动和资料生命周期链路；随后运行浏览器提交→摄取→审核→发布→问答→回退链路。连续一轮完整回归通过后，再删除旧兼容入口及其专属测试。

## 遗留问题

旧 `go/cmd/server` 文件已迁移到最终 Go 包路径。旧 Python 入口仍被历史兼容测试引用，但已从活动 Dockerfile、V2 smoke 和新跨进程测试中退出；旧 synthetic 脚本仍需归类为 legacy 手工工具。当前四进程空库资料全链路、浏览器流程和长期双机运行尚未验收。真实 OIDC、真实电商 API、真实 Provider 与生产质量留待后续外部验收。旧工程参考、历史验收与 snapshots 保留，不改写历史。
