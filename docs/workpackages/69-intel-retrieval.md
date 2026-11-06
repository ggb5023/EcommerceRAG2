# 69 intelligence 阶段 3：情报向量检索与阶段收尾

- 状态：见 INDEX.md
- 依据：决策 0032 §6.2（数值问题双路：向量负责"谁"，SQL 负责"多少"）；
  WP62/63 已建共享层、worker、Tavily 与情报接口
- 前置：WP68 已关

## 目标

让情报域形成可用闭环：抓取 → 存储 → **语义检索** → 展示。本包只做 0032 承诺的
"向量负责谁"这一路；**直连网页抓取与代理出口明确不做**（记录为后续待决，理由见下）。

## 范围

1. **迁移 0057（只追加；先登记再写库）**：
   - `market_observation` 加 `embedding vector(1024)` 可空列（1024 与既有嵌入维度一致）；
   - 同迁移 GRANT：列归属 `rag_v2_intel`（写）不变，无需新授权面；
   - 不建新索引（先观察行量；HNSW 索引留给真实规模再定，写入决策备注）。
2. **worker 嵌入**：抓取写入 observation 时，对 `payload_json` 的标题/摘要文本
   调用既有 embedding provider（复用 answer 域同一 provider 客户端，**不得**新建
   适配器），写 `embedding`；嵌入失败不阻塞抓取（embedding=NULL，检索时跳过该行），
   usage_log 照常记 embedding 调用（slot='embedding'，tokens 如实）。
3. **语义检索**：`IntelService.ListObservations` 请求加可选 `query`（文本）：
   - 有 query → 用 query 嵌入做余弦相似度（SQL `<->` 或 `<=>` 算子），返回前 K
     （`limit` 上限 100）带 `similarity` 字段；
   - 无 query → 现有时间序行为不变；
   - 空 query 嵌入失败 → 回退时间序（fail-open 返回可解释的 `query_degraded=true`）。
4. **前端**：情报页加搜索框（语义查询模式），结果展示 similarity 与 observed_at；
   无货币（0021）。
5. **契约**：contracts.md「intel 观测读取」行扩展 query/similarity 语义（改该行
   文字，两侧测试补充）；proto 变更走 buf 三连 + 重新生成 pb。
6. **阶段收尾文档**：0032 补「实现状态」小节（阶段 1/2/3 完成项 + 直连抓取/代理
   出口明确为后续待决）；refactor-input 登记该待决项。

## 明确不做（本包边界）

- 直连网页抓取与 `V2_INTEL_PROXY_URL` 实际接线（0032 §6.5）：需要真实商家场景与
  出口策略再设计，现仅 Tavily 一个数据源足以闭环；
- HNSW 索引、自动下架、情报告警、多数据源。

## 验收命令

```bash
bash scripts/check.sh            # 44 项 FAILED 0（buf 三连/openapi/ownership/migration-grants 全绿）
cd go && go test ./internal/ports/ ./internal/admin/ -run "Intel" -v
cd python && uv run --frozen pytest -q tests/test_intel_worker.py tests/test_intel_rpc.py
bash scripts/check.sh --smoke    # 47 项五条基线逐字节一致（intel 默认关，dev 无 intel 进程）
# 演练栈：起 intel 服务（V2_INTEL_ENABLED=1），塞 watch_config → 一个 poll 周期 →
# ListObservations?query=<语义查询> 返回带 similarity 的结果；无 query 行为不变
```

## 不要做

- 不新建 provider 适配器；不直连抓取网页；不改 Tavily 适配器；
- 不改 0054/0055/0056；不加 HNSW 索引；
- 不改 CONTRACTS_* 棘轮（NOTE 由主 agent 单独提交）；不 push；提交身份固定；
  不用 `git add .`。

## 汇报

五节格式；附迁移 GRANT、嵌入调用 usage_log 行、语义查询真实响应（含 similarity
排序）、无 query 回退对比、0032 状态补记 diff。
