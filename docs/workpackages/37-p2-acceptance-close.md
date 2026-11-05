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
  完整部署/证书/SMTP 等细节只链接 [install](../install.md)，不重复其步骤；
- 第一步：`bash scripts/dev/demo-shop.sh --ports 26081`，启动隔离栈、导入
  `demo-store-v1`、预检、审核、发布；重复执行幂等；
- 第二步：按 README 的报告命令生成五段 JSON+Markdown；
- 预期：active 5 个演示文档；`wrong_answer_rate` 13.21%；handoff 分母为 0 时
  `handoff_rate=null`；token 按问题数归一为每千次；报告不含货币、金额或价格表；
- 报告读取失败或质量报告缺失时写 `NOT_MEASURED`/`null`，不伪造 0。

## ROADMAP

- P2 的 M6、M7、M8、M9 四项勾选 `[x]`；
- 当前阶段改为 P2 完成、下一阶段 P3 真实商家试用；
- 只更新阶段、勾选和验收边界，不写报告路径、过程记录或测试数字。

## 验收命令

```bash
python3 scripts/check/check-docs-drift.py
python3 scripts/check/check-doc-paths.py
bash scripts/check.sh
bash scripts/check.sh --smoke
```

按 README 的两条命令在演示栈上原样执行一次。

另需：按 README 命令原样复制执行一次并记录 demo-shop 输出、报告五段字段；
README 命令可直接复制粘贴；README 不重复 install.md 的详细步骤；ROADMAP diff。

## 不做

- 不改产品代码、迁移、评测集、脚本行为、棘轮或基线。
- 不做管理台报告页；不重采 smoke；不新增依赖。
- 不用 `git add .`；不 push；不提交/修改/删除 本机未跟踪目录。

## 实际交付

- README 快速开始已改为演示流程：前置列出 Linux、Python 3.12 + uv、Node/npm、
  curl 与 26081–26083 端口；给出 `uv sync --project python --frozen`、
  `npm ci --prefix frontend`、`demo-shop.sh --ports 26081` 和
  `demo-report.py --base-url http://127.0.0.1:26081 --output-dir .local/demo-report`；
  生产安装/证书/模型配置只链接 `docs/install.md`，未重复其步骤。
- README 写明预期：active 5 个演示文档、`wrong_answer_rate=0.132075`（13.21%）、
  `handoff_rate=null`（分母 0 不写成 0）、每千次 token/调用量，以及报告不含货币、
  金额、汇率或价格表；缺数据写 `NOT_MEASURED`/`null`。
- ROADMAP 的 P2 M6/M7/M8/M9 全部改为 `[x]`，P2 标记技术验收完成；当前阶段改为
  “P2 完成；下一阶段 P3 真实商家试用”，当前任务同步指向 P3；M9 的“每千次成本”
  改为“每千次 token 用量”。
- 按 README 命令原样执行，演示店铺幂等路径输出：

```text
$ bash scripts/dev/demo-shop.sh --ports 26081
PACKAGE files=8 package_sha256=4924129a... manifest_sha256=2ddfeb36...
PACKAGE document=demo-products sha256=7d535c33...
...
SKIP demo-store-v1 already active manifest_sha256=2ddfeb36...
```

- 同一流程生成报告：

```text
$ python3 scripts/dev/demo-report.py --base-url http://127.0.0.1:26081     --output-dir .local/demo-report
REPORT status=MEASURED json=.local/demo-report/demo-report.json markdown=.local/demo-report/demo-report.md
HANDOFF numerator=0 denominator=0 rate=null
QUALITY wrong_answer_rate=0.132075 source_sha256=3b828cb5...
```

  报告 JSON 的 `input.version_ids` 有 5 个 active 演示文档；五段字段
  `input`、`operations`、`quality`、`handoff`、`tokens` 均存在，token 段含
  `input_tokens_per_1000`、`output_tokens_per_1000`、`calls_per_1000` 和
  `failed_calls_per_1000`。
- 文档守卫：`check-docs.py` PASS、`check-docs-drift.py` PASS、
  `check-doc-paths.py` PASS。
- 门禁：`bash scripts/check.sh` PASSED 40；`bash scripts/check.sh --smoke`
  PASSED 45（五条 smoke 与基线逐字节一致）。
- 未改产品代码、迁移、脚本行为、评测集、棘轮或 smoke 基线；未提交
  本机未跟踪目录。

## 未覆盖路径

- README 流程是在已有 26081 演示栈上实跑，`demo-shop.sh` 走的是幂等 `SKIP` 路径；
  本轮未重新执行 `--reset` 或首次导入，首次导入由 WP35 验收覆盖。
- 未在干净机器上从零执行 `uv sync`/`npm ci`；本轮用现有依赖，`check.sh` 的
  `npm-ci` 与 Python 测试通过，`uv sync` 未单独重跑。
- 未运行 `check.sh --db`、`--e2e`、`--all`；未做浏览器页面验证。
- 未调用真实模型/provider；报告使用现有合成演示栈的 frozen quality 与 usage_log
  快照。
- 未验证真实商家数据或 P3 试用；P3 是否启动由主 agent/用户决定。
- 未实现管理台报告页；README 只描述 CLI 与报告字段。
