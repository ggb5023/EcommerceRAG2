# 37 P2 验收收尾：README 快速开始 + ROADMAP 勾选

> 状态：见 INDEX.md　验收：见 §验收命令
> 依据：[ROADMAP](../../ROADMAP.md) P2、[0035](../decisions/0035-demo-report.md)、
> [README](../../README.md)、[install](../install.md)

## 目标

只改文档，闭合 P2 的技术验收入口：README 的快速开始改为演示店铺全流程，
ROADMAP 勾选 P2 四项并更新阶段。本轮不改产品代码、迁移、评测集或脚本行为。

## README 快速开始

按 P2 验收原文“README 的快速开始能在演示数据上走完全流程”，写清：

- 前置：Linux、Python 3.12 + uv、Node/npm、curl；隔离栈使用 26081–26083，
  先确认端口空闲；`uv sync --project python --frozen`、`npm ci --prefix frontend`；
  完整部署/证书/SMTP 等细节只链接 [install](install.md)，不重复其步骤；
- 第一步：`bash scripts/dev/demo-shop.sh --ports 26081`，启动隔离栈、导入
  `demo-store-v1`、预检、审核、发布；重复执行幂等；
- 第二步：`python3 scripts/dev/demo-report.py --base-url
  http://127.0.0.1:26081 --output-dir .local/demo-report`，生成五段 JSON+Markdown；
- 预期：active 5 个演示文档；`wrong_answer_rate` 13.21%；handoff 分母为 0 时
  `handoff_rate=null`；token 按问题数归一为每千次；报告不含货币、金额或价格表；
- 报告读取失败或质量报告缺失时写 `NOT_MEASURED`/`null`，不伪造 0。

## ROADMAP

- P2 的 M6、M7、M8、M9 四项勾选 `[x]`；
- 当前阶段改为 P2 完成、下一阶段 P3 真实商家试用；
- 只更新阶段、勾选和验收边界，不写报告路径、过程记录或测试数字。

## 验收命令

```bash
bash scripts/dev/demo-shop.sh --ports 26081
python3 scripts/dev/demo-report.py --base-url http://127.0.0.1:26081 \
  --output-dir .local/demo-report
python3 scripts/check/check-docs.py
python3 scripts/check/check-docs-drift.py
python3 scripts/check/check-doc-paths.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

另需：按 README 命令原样复制执行一次并记录 demo-shop 输出、报告五段字段；
README 命令可直接复制粘贴；README 不重复 install.md 的详细步骤；ROADMAP diff。

## 不做

- 不改产品代码、迁移、评测集、脚本行为、棘轮或基线。
- 不做管理台报告页；不重采 smoke；不新增依赖。
- 不用 `git add .`；不 push；不提交/修改/删除 `scripts/review/`。

## 实际交付

（README、ROADMAP 与实测完成后填写；未完成项在汇报第 4 节点名。）

## 未覆盖路径

（README、ROADMAP 与实测完成后填写。）
