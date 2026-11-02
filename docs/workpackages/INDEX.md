# 工作包索引

> 状态取值：`pending` / `in-progress` / `active`（等同 in-progress，当前进行中）/ `implemented` / `done`。
> `done` 仅当验收命令已实跑并通过；`implemented` 表示实现完成但验收或复核未闭合。
> `superseded-by` 表示被后续工作包取代，不等于未完成。
> 本表是工作包状态的唯一出处；`CURRENT` 只表示当前指针。

| 编号 | 标题 | 状态 | 阻塞 | 替代关系 | 最后提交 |
| --- | --- | --- | --- | --- | --- |
| 01 | 稀疏向量写入链路（0029 第二步） | implemented | 真实 provider 的 sparse token_id 超出 `sparsevec(65536)`，真实写入全 NULL（WP12 探针） | — | `9ca35ec` |
| 02 | 决策理由落库（B6） | done | — | superseded-by 09 | `97717a7` |
| 03 | 结果度量：自动解决率（B1） | done | — | — | `f91282b` |
| 04 | 内容缺口闭环（B3） | done | — | — | `6a53936` |
| 05 | Python 长函数拆分（P-C） | done | — | — | `907977d` |
| 06 | 一致性收尾与机械护栏（P-D） | done | — | — | `7316674` |
| 07 | 测试分层剩余项 | done | — | — | `b7ef570` |
| 08 | 前端设计系统（M9 阶段 1） | done | — | — | `bad1225` |
| 09 | 决策账本语义（重开 02） | done | — | — | `7675ad2` |
| 10 | D2 前置代理示例（nginx/caddy） | done | — | — | `f65b6c4` |
| 11 | 稀疏查询 A/B（0029 第三步） | implemented | 真实语义 A/B 阻塞：Bailian sparse token_id 超 65536；WP12 机制 A/B 53/53 已跑 | — | `1408d53` |
| 12 | 检索 A/B harness（补齐 0029 证据） | implemented | 机制 A/B 已跑；真实语义 A/B 被 sparsevec 维度阻塞 | — | 待提交 |

## 同步规则

- 工作包文件头的「状态：xxx」统一写「状态：见 INDEX.md」，不在两处各写一份。
- 状态变化与 INDEX 更新必须在同一次提交内完成。
- `CURRENT` 只指向当前工作包，不承载状态。
