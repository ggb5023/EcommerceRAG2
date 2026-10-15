# EcommerceRAG

开源、可自部署的电商客服知识引擎。导入店铺资料，得到一个**回答有出处、不乱承诺、答不了就转人工**的客服助手。

面向淘宝、抖店、拼多多等平台上的中小商家。它不是电商平台，也不需要平台开放接口：客服在工作台拿到建议回复，复制回平台发送即可。

## 解决什么问题

- 发货、退换、尺码、活动这类重复问题，占了客服大部分时间；
- 答错包邮、退款或活动价，会引起售后纠纷和差评；
- 资料散在后台、文档和群里，新客服上手慢；
- 大促期间咨询量成倍增加。

## 核心能力

- **资料管理**：上传政策文档、FAQ、CSV，可预览、审核、发布、撤回、回滚，只有已发布的内容参与回答。
- **带出处的回答**：每条结论绑定来源片段；资料撤回后，旧回答随之失效。
- **不确定就收手**：最多追问一次，仍然不足就转人工，不编造。
- **客服工作台**：人工队列、原子领取、人工回复；AI 辅助只给建议，不替客服发送。
- **可度量**：内置电商评测集，统计回答质量和模型费用。

正在开发中的能力（商品表结构化、活动时效、一键复制、知识缺口）见 [路线图](ROADMAP.md)。

## 快速开始

需要 Linux、Docker Engine 和 Compose v2，以及一个阿里云百炼账号。完整步骤见 [安装](docs/install.md)。

部署后的入口：

| 地址 | 用途 |
| --- | --- |
| `/admin/overview` | 商家：店铺设置、资料导入与审核 |
| `/support` | 客服：人工队列与回复 |
| `/shop/my-shop` | 顾客咨询页 |

## 文档

| 读者 | 文档 |
| --- | --- |
| 商家 | [安装](docs/install.md) · [导入知识](docs/knowledge.md) · [客服操作](docs/support.md) · [备份恢复](docs/backup.md) |
| 开发者 | [产品定位](docs/product.md) · [架构](docs/architecture.md) · [路线图](ROADMAP.md) · [开发指南](docs/development.md) · [决策记录](docs/decisions/) |
| AI 助手 | [AGENTS.md](AGENTS.md) |

## 技术栈

Go（HTTP/SSE、gRPC）、Python（LangChain、LangGraph）、Vue 3、PostgreSQL + pgvector、阿里云百炼。详见 [架构](docs/architecture.md)。

## 许可

自有代码采用 [MIT](LICENSE)，依赖保留各自的许可，见 [第三方声明](THIRD_PARTY_NOTICES.txt)。
