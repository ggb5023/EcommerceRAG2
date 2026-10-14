# 二次开发边界

[项目入口](../README.md) · [工程规则](../AGENTS.md) · [架构](architecture.md)

面向商家的开源电商客服 RAG 系统，首版单商家单店铺。平台连接器由商家或集成方实现；Medusa、公开商品双轨、实时订单/库存、API Key 和独立检索 API 暂缓。既有开发 facts fake 只服务冻结合成回归，不是安装者的默认业务输入。

Go 是所有业务 HTTP/SSE 入口，持有身份、实时授权、访客会话、人工领取、消息发布和审计。Python 处理知识摄取、检索、重排和生成。Python 的 ANSWER/CLARIFY/HANDOFF 是建议，Go 按授权、版本、披露和完整性决定发布。不得把 `can_copy` 全局改为 true。模型原始文本不直接推送客户。

Proto/gRPC 是跨语言契约；生产式容器传输使用 mTLS，授权回验使用 `AuthorizationService.ValidateScope`，不开放内部 HTTP 回验接口。空 TLS 配置只保留明确的本机开发兼容路径。权限和业务终态不交给 Python。摄取 job/item/version/chunk 由 Python 单写，客户消息/控制权/事件由 Go 单写。新增结构使用 checksum 迁移，不改历史迁移。

自部署身份 profile 在固定 subject→tenant/user/shop 映射后由 Go 数据库提供角色、店铺范围、权限版本和撤权状态。Keycloak 只提供协议身份；浏览器会话固定登录时权限，版本变化使旧会话失效，重新登录读取当前权限。旧 bearer profile 的 token claims 检查继续独立支持。

客户接口命名空间 `/v1/customer/`，员工队列 `/v1/support/`，员工辅助 `/v1/conversations` 和管理 `/admin/v1/` 分开。客户操作使用 conversation ID、message ID、execution ID，写入校验 Origin、CSRF 和 Idempotency-Key；员工控制另携带 epoch。Cookie 只保存不透明凭据，数据库保存摘要。

测试入口：`bash scripts/check.sh`；数据库、跨进程、真实模型、浏览器和语义复核另行执行。新客户闭环契约见 [评测契约](../eval/customer-service-contract.v1.json)。冻结 53 条与 7 条安全案例、原答案点门禁及证据边界门禁分别保留，不能用新的客服闭环结论覆盖原缺口。

审核接口 `GET /admin/v1/merchant/reviews/{id}` 的 `content` 兼容保留原字段，新增 `chunk_id`、`heading_path`、`content_type`、`split_reason`、白名单 `source_position`、解析元素 `char_start/char_end`、`parse_hints` 和表格结构。完整预览只授予审核权限，客户来源接口不扩大。店铺查询新增 `customer_chat_url` 和 `customer_published_knowledge_available`，地址来自已验证的 `V2_CUSTOMER_ORIGIN`，不使用请求 Host；可见知识提示复用客户 ACL 检查，实际问答仍执行完整的时效、披露和发布检查。

客户 AI trace 的 `v2_generation_input` 阶段由 Python 在调用生成前写入，仅含实际输入证据 ID、document/version、原 rank、正文 SHA-256 和上下文/请求指纹。它证明构造的请求，不证明 Provider 接收成功。checksum 迁移 0028 只扩展阶段约束，Go 继续没有 trace 读取权限。`eval/diagnose_customer_execution.py` 经两个受限连接、显式只读事务关联现有执行，不检索、不调用模型、不写业务数据；用法及可选绑定材料见 [评测说明](../eval/README.md#read-only-customer-execution-diagnosis)。

证据编号沿用 `Evidence.rank`，过滤后允许间隔，不按数组位置重新编号。员工生成的主回答引用与补充限制引用 `limit_citation_ids` 均须绑定实际交付的证据；客户生成使用独立决策结构，不把内部补充说明直接推送访客。

Provider 的可重试 429、5xx 与连接超时最多再尝试一次。第一次调用只使用剩余 deadline 的一半，第二次使用实际余量，两次均计入同一账本并受取消和限流约束；不延长请求总 deadline。非法结构、截断、授权和 TLS 配置失败不自动重试。

自有代码 [MIT](../LICENSE)，第三方许可保留 [声明](../THIRD_PARTY_NOTICES.txt)。公开文档仅放行指定 docs 文件，`.local/` 和原始资料/输出不进入 Git。运行进度见 [HANDOFF](../HANDOFF.md)，不把组件测试当作生产验收。
