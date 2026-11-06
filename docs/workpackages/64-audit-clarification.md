# 64 审计澄清批（六条核实结论落盘 + scripts/review 入库）

- 状态：见 INDEX.md
- 依据：2026-11-06 主 agent 对六条外部审计指控的逐条核实结论；用户决定
  scripts/review 目录 入库
- 前置：无（与 WP63 并行文档面，不碰其代码路径）

## 目标

把六条核实结论中"属实但从未写清"的部分落进文档，做三处低风险文案修正。
scripts/review 目录 按用户决定**保持仓外现状**（不入库、不进 CI）。
**不改变任何运行时行为、权限矩阵与审核流程。**

## 步骤

1. **0018 补「角色矩阵与审计说明」小节**：
   - `POST /admin/v1/merchant/versions/batch-approve` 是 merchant_admin 的商家
     自助确认路径（建议必须 approved、content_hash 一致、pending、shop 权限、
     任一条失败整批不写、幂等），与 reviewer 单审并存；两条通道都写
     `reviewed_by`、`reviewed_at`、`reviewed_hash`，审计不混用；
   - 写明「knowledge_reviewer 单角色可审可发（publish/rollback/revoke 同角色）
     是对 3 人团队的有意简化，无审核人≠发布人约束；扩团队前需引入双人复核」。
2. **前端文案**：`review-suggestion.ts` 的「AI 建议」→「规则建议」；
   `ReviewsPage.vue` 建议详情展示规则版本（`agent_model_id` 已有，显示为
   `规则版本：local-rules-v1`）；副标题「AI 建议仅供参考」→「规则建议仅供参考
   （确定性规则，非模型输出）」；同步更新两个测试文件的断言。
3. **product.md 补一句**（顾客通道边界）：顾客回答由系统自动生成并发布
   （失败关闭 + 引用校验 + 转人工兜底），**不经人工审阅**；人工把关仅存在于
   员工辅助模式（0005：客服是最后一道关）。
4. **refactor-input.md 待决问题区加一条前置条件**：任何 agent 自动发布路径上线
   前，必须先建「必要人工审查清单」门禁；当前无自动发布路径，L3（WP60）为
   发现与提示，无门禁属预期状态。**（用户本轮未点名，暂缓，未做。）**
5. **scripts/review 目录 去向**：用户 2026-11-06 拍板「保持现状不变」——继续不跟踪、
   不进 CI，按 0015 由开发/审查 agent 本地执行。本包不改动、不提交该目录。

## 验收命令

```bash
bash scripts/check.sh                    # 44 项 FAILED 0（privacy-guard/privacy-tests 含新白名单）
python3 -m unittest discover -s tests -p 'test_privacy*.py' -q   # 19 项全过
cd frontend && npm test -- --run         # 文案断言更新后全过
python3 scripts/review/collect.py; echo "exit=$?"   # 能运行并产出 packet；当前状态
# 下退出码 1 属预期（今日棘轮合法上升会报 blocker），latest.md 内容人工核对
git status --short                       # scripts/review/ 已跟踪，工作区仅 .local 忽略项
```

## 不要做

- 不改权限矩阵（batch-approve 仍 merchant）、不改 publish 角色、不加
  审核人≠发布人约束（那是后续设计决策，不是澄清）；
- 不动 WP63 的代码路径（python/app/intel、ports、proto、/admin/intel）；
- 不改 L3 巡检逻辑与四类清单（"五类"出处未提供，不在本包对齐）；
- 不改 scripts/review 目录（用户决定保持现状）；不改 CONTRACTS_* 棘轮。

## 汇报

五节格式；附每处文档 diff 的行号、前端测试输出、privacy 白名单行、
collect.py 退出码与 latest.md 首行。
