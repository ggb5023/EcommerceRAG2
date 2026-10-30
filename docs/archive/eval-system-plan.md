# 评测体系（重构方案 · 质量与测量）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充总纲未覆盖的**评测可信度**维度。执行细节（runner 分层）见总纲 W4 与
`refactor-assessment.md` R4；本文件负责"评测结论能不能被信"。

---

## 0. 现状（2026-10-27 实测）

| 项 | 现状 |
| --- | --- |
| eval 代码量 | ≈12.7k 行（超过应用本体 10.4k） |
| Runner | 4 个：batch / retrieval / lifecycle / quality-e2e，各自加载、校验、写报告 |
| 冻结用例 | 60 例；`metadata.sha256` 与实际文件一致（`check-eval-corpus.py` PASS） |
| 语料位置 | `/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional`（**provisional**，本地构建） |
| 语料来源 | `manifest.source_manifest` 指向仓库内 `data/synthetic/ecommerce-m2-aligned-v1/manifest.yaml` |
| 基线 | `aligned-retrieval-baseline-v1.json`：53 例，recall@5=0.981、mrr=0.981，`model_version=deterministic-keyword-v1` |
| 外部输入门禁 | `eval/m2-external-input-gate.json` → **BLOCKED**（7 项 `ready:false`） |
| 评审 attestation | 3 份（agent） |
| 门禁开关 | `ECR_REQUIRE_EVAL` 仅两态：`0/unset`（缺件 skip）、`1`（缺件 fail） |
| 成本口径 | 0021 之后记录 token 用量（`usage_log`），不再统计货币 |

---

## 1. 问题清单

### E1 provisional 与 approved 只有一个布尔

`metadata.real_service_acceptance` 为 false 时仍能通过 `ECR_REQUIRE_EVAL=1`——它只能证明"文件在"，
不能证明"语料被批准过"。P1 验收需要的正是后者。

### E2 语料 provenance 没有台账

`eval/build/` 里有 proposal / decisions / revision-map，但没有一份"谁在什么时候用什么命令、从哪个 commit
生成了这份 revision"的记录。半年后没人能确认它是怎么来的。

### E3 agent 评审是自证循环

0015 让 agent 承担质量门禁，而这份语料本身也是 agent 生成 + agent 评审。代码已强制
`real_service_acceptance=false`（正确），但缺少"生成方与评审方分离"的约束。

### E4 评测成本与时长没有计量

每个 track 跑多久、耗多少 token，现在只能靠人记。`usage_log` 已经有了，但评测不写也不读。

### E5 报告结构不统一

`aligned-retrieval-v2`、`v2-quality-e2e-v2` 等各写各的字段，无法横向比较，也无法自动判断"这一轮比上一轮差"。

---

## 2. 工作包

### E-W1　门禁三态（半天，最高优先级）

```bash
ECR_REQUIRE_EVAL=0          # 缺件 skip（日常开发）
ECR_REQUIRE_EVAL=1          # 缺件 fail（仍接受 provisional）
ECR_REQUIRE_EVAL=approved   # 缺件 fail + 要求 real_service_acceptance==true + attestation 存在 + 批准记录存在
```

`approved` 档下额外断言：

```
metadata.real_service_acceptance is True
manifest.approval.reviewer_kind in {human, agent} 且 attestation 文件存在
gate.status == UNBLOCKED（7 项 ready 全为 true）
```

验收：

```bash
ECR_REQUIRE_EVAL=approved bash scripts/check.sh   # 当前应失败，且失败原因写明"语料未批准"
```

### E-W2　provenance 台账（2 小时）

新增 `eval/build/PROVENANCE.jsonl`（仓库外，`/var/lib/ecommerce-rag/eval/`），每构建一次追加一行：

```json
{"at":"2026-10-27T10:26:00+08:00","by":"dsh@ecs","commit":"2055561",
 "command":"python eval/build_eval_revision.py ...","source_manifest_sha256":"…",
 "out_revision_sha256":"db5d27dc…","reviewer_kind":"agent","approved":false}
```

验收：`tail -1 PROVENANCE.jsonl` 的 `out_revision_sha256` 与 `metadata.sha256` 一致。

### E-W3　Runner 分层（与总纲 W4 同一件事）

本文件只钉住接口（`lib` 暴露什么），具体拆分步骤见总纲 W4：

```
eval/lib/load.py      load_revision(root) -> (cases, corpus, alignment, metadata)
eval/lib/bind.py      bind_evidence(case, corpus) -> evidence[]
eval/lib/report.py    write_report(path, envelope) -> None   # 统一 envelope
eval/lib/gate.py      judge(report) -> {status, failed, not_measured}
eval/runners/*.py     ≤300 行，只做参数解析与编排
```

### E-W4　统一报告 schema（半天）

所有 track 写同一层外壳：

```json
{"report_version":"ecr-eval-v3","eval_set_version":"…","corpus_sha256":"…",
 "model":{"name":"…","version":"…"},"params":{},"started_at":"…","duration_s":0,
 "usage":{"tokens_in":0,"tokens_out":0},"status":"PASS|FAIL|NOT_MEASURED",
 "metrics":{},"cases":[…]}
```

验收：新跑一次 batch 与 retrieval，两个报告的键集合相等（`cases` 内部可比对）。

### E-W5　成本与时长计量（2 小时）

每个 runner 结束时写 `usage` 与 `duration_s`，并从 `usage_log` 汇总本 track 的 token 量。

验收：报告里有非零 `duration_s`；有 `usage.tokens_*` 或显式 `null` + 原因。

---

## 3. 红线

1. 不改冻结用例、真值、gate 判定逻辑与门槛
2. 不把 `real_service_acceptance` 改成 true 来让门禁通过
3. 不用 provisional 语料的数字对外声称"质量达标"
4. agent 评审必须保留 `human_review_completed=false` 标注

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 三态门禁 | `ECR_REQUIRE_EVAL=approved bash scripts/check.sh` | 明确报"语料未批准" |
| provenance | `tail -1 /var/lib/ecommerce-rag/eval/PROVENANCE.jsonl` | 哈希与 metadata 一致 |
| 报告结构 | 两次 runner 产物 | 键集合相等 |
| 计量 | 报告 `duration_s` | 非零 |

## 5. 与其它文档的边界

- runner 拆分步骤 → `refactor-assessment.md` W4
- 语料备份到 OSS → `ops-and-release-plan.md` O-W5
- `usage_log` 表结构 → `data-and-migrations-plan.md`
