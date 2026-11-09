# EcommerceRAG

开源、可自部署的智能化小型商家多店铺管理平台
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
- **可度量**：内置电商评测集，统计回答质量和模型 token 用量。

更多能力与后续计划见 [路线图](ROADMAP.md)。

## 快速开始

本地演示使用固定合成店铺，不需要模型账号；生产部署的证书、模型配置和完整步骤见
[安装](docs/install.md)。

前置：Linux、Python 3.12 + uv、Node.js/npm、curl；隔离栈使用 26081–26083，先确认
端口空闲。首次准备依赖：

```bash
uv sync --project python --frozen
npm ci --prefix frontend
```

启动演示店铺。首次运行会启动隔离栈并导入 `demo-store-v1`，完成预检、审核和发布；
重复执行幂等：

```bash
bash scripts/dev/demo-shop.sh --ports 26081
```

生成五段评测报告（JSON + Markdown）：

```bash
python3 scripts/dev/demo-report.py \
  --base-url http://127.0.0.1:26081 \
  --output-dir .local/demo-report
```

预期看到 active 5 个演示文档、`wrong_answer_rate=0.132075`（13.21%）、
`handoff_rate=null`（演示栈暂无 customer 消息，不写成 0），以及按问题数归一的
每千次 token/调用量；报告只统计 token 与次数，不含货币、金额、汇率或价格表；
质量或转人工数据缺失时写 `NOT_MEASURED`/`null`。完整字段和口径见
[0035](docs/decisions/0035-demo-report.md)。若系统 `python3` 未安装 PyYAML，
可将 `python3` 换成 `uv run --project python python`。

生产安装、证书和模型配置见 [安装](docs/install.md)。生产部署后的入口：

| 地址 | 用途 |
| --- | --- |
| `/admin/overview` | 商家：店铺设置、资料导入与审核 |
| `/support` | 客服：人工队列与回复 |
| `/shop/my-shop` | 顾客咨询页 |

## 文档

| 读者 | 文档 |
| --- | --- |
| 商家 | [安装](docs/install.md) · [上线注意事项](docs/merchant-notes.md) · [导入知识](docs/knowledge.md) · [客服操作](docs/support.md) · [备份恢复](docs/backup.md) |
| 开发者 | [产品定位](docs/product.md) · [架构](docs/architecture.md) · [路线图](ROADMAP.md) · [开发指南](docs/development.md) · [决策记录](docs/decisions/) |
| AI 助手 | [AGENTS.md](AGENTS.md) |

## 技术栈

Go（HTTP/SSE、gRPC）、Python（LangChain、LangGraph）、Vue 3、PostgreSQL + pgvector、阿里云百炼。详见 [架构](docs/architecture.md)。

## 许可

自有代码采用 [MIT](LICENSE)，依赖保留各自的许可，见 [第三方声明](THIRD_PARTY_NOTICES.txt)。
