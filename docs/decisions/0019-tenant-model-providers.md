# 0019 商家自选模型供应商

- 状态：提议
- 日期：2026-10-27
- 范围：P3（先设计，不实现）。

## 背景

当前部署只有一套全局 Provider 配置（`/etc/ecommerce-rag/providers.env`）。商家无法为自己的
租户选择模型供应商；价格也只覆盖百炼一个价格表。P3 需要让商家在不暴露密钥、不改变共享预算
账本和 agent 评审规则的前提下，接入自选供应商。

## 数据模型

迁移编号在实现前另定；只新增表，不改现有 Provider 配置表。

```sql
CREATE TABLE provider (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          BIGINT NOT NULL REFERENCES tenant(id),
    name               TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('openai_compatible','bailian','mock')),
    base_url           TEXT NOT NULL,
    api_key_ciphertext TEXT NOT NULL,
    key_last4          TEXT NOT NULL,
    key_version        INTEGER NOT NULL DEFAULT 1,
    enabled            BOOLEAN NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE provider_model (
    id            BIGSERIAL PRIMARY KEY,
    provider_id   BIGINT NOT NULL REFERENCES provider(id) ON DELETE CASCADE,
    slot          TEXT NOT NULL CHECK (slot IN ('control','embedding','rerank','generation')),
    model_id      TEXT NOT NULL,
    price_input   NUMERIC(12,6) NOT NULL CHECK (price_input >= 0),
    price_output  NUMERIC(12,6) NOT NULL CHECK (price_output >= 0),
    price_version TEXT NOT NULL,
    embedding_dim INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_id, slot)
);
```

`price_input/price_output` 按人民币/百万 token 计，和 `pricing.json` 的 `per=1e6` 对齐；
价格表随 provider 走，预算窗口仍按人民币计。`provider_id` 进入每次调用的预留/观测记录，
支持按供应商拆账；共享账本结构仍由 0006 的 `BudgetLedger` 管理。

## 统一适配层

- **优先只做 OpenAI 兼容协议**：`/v1/chat/completions`、`/v1/embeddings`、`/v1/rerank`
  （或兼容网关的 rerank 路径）。覆盖 OpenAI、通义兼容模式、vLLM、Ollama、大多数聚合网关。
- **bailian 原生协议作为首个特例**：保留现有 DashScope 适配器，只对 kind=`bailian` 使用。
- 不自建每家适配器；新供应商优先要求提供 OpenAI 兼容端点。
- **不静默回退**：商家配置的 provider 不可用时直接报错（`provider_unavailable`/`provider_misconfigured`），
  不自动切回平台默认供应商；否则引用来源、价格与预算归属都会不一致。平台默认只在租户尚未配置时使用，
  并由部署配置显式决定。协议差异用能力标记与最小探测解决，
  不做按厂商分叉的业务逻辑。
- 适配层统一负责：超时、重试一次、usage 解析、错误分类、provider_id 记账；现有
  `BudgetLedger.reserve/record` 接口保持不变。

## 密钥安全

- `api_key_ciphertext` 使用 AES-GCM：主密钥 0600 放仓库外，密文格式 `v1:<nonce>:<ciphertext>`；
  解密只发生在调用瞬间，不进入日志、错误消息、审计或前端。
- 接口只回 `key_last4`；创建/轮换后完整密钥只显示一次（或仅接受写入）。
- 日志与 `admin_audit` 只记录 `provider_id`、`kind`、`slot`、`model_id`、指纹/版本，不记录明文。
- 每次付费调用记录 `provider_id`；预算账本的 attempt 增列或旁表关联，迁移时只追加。
- 数据出域审计：每次调用在 `admin_audit.metadata_json` 记录 `provider_id` 与 `kind`；
  `kind=mock` 或本地部署（Ollama/vLLM）的调用标记 `no_egress=true`，云端调用标记 `no_egress=false`；
  审计只记标识与分类，不记 prompt/响应正文或密钥。

## UI 流程

1. 填 `base_url` + `api_key`；
2. 测试连接（最小请求，不生成业务答案）；
3. 拉取/填写模型列表；
4. 绑定 `control/embedding/rerank/generation` 四个槽位；
5. 保存并启用。

测试失败必须可区分：网络不可达、DNS 失败、401/403、模型不存在、embedding 维度不符、
超时、限流。错误只显示分类与 request id，不显示密钥或供应商原始报文。

## 备选方案

| 方案 | 链接 | 评估 | 不采用理由 |
| --- | --- | --- | --- |
| 薄适配层 + OpenAI 兼容（本提案） | [OpenAI API](https://platform.openai.com/docs/api-reference) | 覆盖大多数供应商与自建推理；代码面小；密钥留在本工程 | 需要自己维护错误分类与模型探测 |
| LiteLLM proxy | [LiteLLM](https://docs.litellm.ai/) | 模型覆盖广、成本表成熟、统一 OpenAI 接口 | 多一个常驻服务与配置面；密钥与预算账本出现第二处权威；与 0006 的单一账本冲突 |
| one-api / new-api | [one-api](https://github.com/songquanpeng/one-api) · [new-api](https://github.com/Calcium-Ion/new-api) | 多供应商聚合、额度与密钥管理成熟 | 引入外部网关与账号体系；租户隔离、审计字段和人民币预算需要二次改造 |
| 直接内置每家适配器 | 各供应商官方文档 | 可精确控制每家能力 | 维护成本随供应商数量线性增长，单人开发不可持续 |

选择“薄适配层 + OpenAI 兼容”：不引入新的常驻网关，不产生第二套密钥/额度权威；bailian 原生
只作为首个特例，后续供应商优先要求兼容端点。

## 与 0006 / 0015 的关系

- **0006 共享预算账本**：不换账本、不新建窗口；价格表随 provider 走，预算仍按人民币计；
  `committed_cny`/`published_cny` 口径不变。
- **0015 agent 把关**：换供应商不改变评审规则、阈值或 `human_review_completed=false`；
  agent 评审仍由开发期质量门承担。
- 换供应商不改变发布、审核、必要人工清单（0013）与冻结评测集。

## 非目标

- 不做多供应商自动故障转移；首版由商家显式选择并启用；供应商故障时直接报错，不静默回退。
- 不做按 token 向商家计费/结算；只做成本可见与按 provider 拆账。
- 不引入 LiteLLM/one-api 等常驻网关；若未来需要，另写决策记录。
