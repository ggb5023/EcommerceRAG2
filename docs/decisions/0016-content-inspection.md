# 0016 内容巡检（L3）设计草案

- 状态：提议
- 日期：2026-10-27
- 范围：P2-M7 的 L3，只出设计，不实现。

## 背景

资料发布后会出现新的失效形态：活动过期但未下架、旧版本与新版本冲突、顾客高频提问但资料
没有覆盖、引用指向的 chunk/文档已失效。L1/L2 管“导入”和“单版本审核”，L3 管“发布后的持续
巡检”。本记录只定义巡检项、数据草案与管理台交互；不新增迁移、不写代码、不接付费模型。

## 巡检项

| 巡检项 | 判定（v1 纯规则） | 严重度 | 建议动作 |
| --- | --- | --- | --- |
| `expired_not_removed` | `effective_to < business_date` 且版本仍 active | high | 提示商家下架或更新 |
| `newer_version_conflict` | 同一 document 存在更新的 pending/active 版本，旧 active 版本仍被引用 | high | 对比版本，提示切换或撤回 |
| `unanswered_high_frequency` | 近 N 天转人工/无资料命中的问题簇，没有对应已发布资料 | medium | 生成知识缺口待办 |
| `citation_invalid` | 已发布答案的 citation 绑定 chunk/version 已撤回、过期或不可见 | high | 标记旧答案需重新核验 |

阈值（N 天、问题簇最小次数）不在本提案里定死；实现前用配置或决策补充。
v1 只读现有表，不写 `document_version`；巡检结果只写 `inspection_report`。

## 表草案（尚未建迁移）

```sql
CREATE TABLE inspection_report (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      BIGINT NOT NULL REFERENCES tenant(id),
    shop_id        TEXT,
    kind           TEXT NOT NULL CHECK (kind IN
                     ('expired_not_removed','newer_version_conflict',
                      'unanswered_high_frequency','citation_invalid')),
    severity       TEXT NOT NULL CHECK (severity IN ('high','medium','low')),
    status         TEXT NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open','acknowledged','resolved','dismissed')),
    subject_type   TEXT NOT NULL,
    subject_id     TEXT NOT NULL,
    fingerprint    TEXT NOT NULL,
    evidence_json  JSONB NOT NULL DEFAULT '{}',
    detected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ,
    UNIQUE (tenant_id, fingerprint, detected_at)
);
CREATE INDEX idx_inspection_open ON inspection_report (tenant_id, status, severity, detected_at DESC);
```

`evidence_json` 只存可复查的 id/hash/日期，不存顾客对话正文；fingerprint 由
tenant/kind/subject/规则版本生成，避免重复待办。

## 管理台交互

- “待办”卡片：按 high/medium 分组，显示巡检项、受影响资料/答案、建议动作；
- 点开进入证据面板：版本、chunk、日期、最近一次检测；
- 商家动作只有“确认/忽略/已处理”，不提供“agent 自动下架”；
- 巡检报告只提示，不改变发布状态；下架/撤回仍走既有商家动作。

## 触发方式

- **手动**：商家在管理台点“立即巡检”，同步或异步返回本次报告 id；
- **每日一次**：机器侧 `/etc/cron.d` 调用受控命令；cron 文件属于部署状态，不进仓库，
  也不在本阶段实现；
- 同一 tenant/shop/kind 的未处理报告按 fingerprint 去重，不重复刷屏。

## 责任边界

- **agent 判定**：巡检项、severity、证据与建议动作；
- **商家确认**：确认/忽略/下架/更新资料；
- **不实现**：本阶段不建表、不写接口、不写定时任务、不接模型；实现前需单独确认迁移编号与
  阈值。

## 后果

- 巡检是“发现与提示”，不是自动发布/自动下架；
- 与 L1/L2 共用 `business_date`、披露过滤与 `tenant_agent_review` 最新开关，不改变既有守卫强度；
- 需要新增迁移时只追加，不改 0001–0031。

## 配套方案

- [0017 M6-L1 导入预检](0017-m6-l1-import-precheck.md)
- [0018 M6-L2 审核建议](0018-m6-l2-review-suggestion.md)
