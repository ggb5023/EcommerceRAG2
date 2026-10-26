# Evaluation

Versioned evaluation definitions and runners. No customer data or credentials
live here. Raw captures, answers, reviews and reports stay outside Git
(`0700` directories, `0600` files) and are never overwritten; each rerun writes
a new report.

```bash
bash eval/run.sh offline   # free; also runs inside scripts/check.sh
bash eval/run.sh live      # lists the paid real-model steps below
```

## Frozen inputs

These files define quality and must not change to make a new run pass.

| Input | Content |
| --- | --- |
| `synthetic_cases.jsonl` (+ `.metadata.json`, `.review.json`) | 60 staff cases: query, expected documents, answer points, authorization context, business date |
| `data/synthetic/ecommerce-m2-aligned-v1/` | 37 synthetic source documents |
| `/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/` | approved revision-1 alignment, corpus `documents.jsonl`, answer-point evidence sidecars (outside Git) |
| `quality-scenarios.v1.json` | multi-turn prerequisites bound to frozen case hashes |
| `evidence-boundary-contract.v1.json` | per-case evidence-boundary contract for the 53 eligible cases |
| `customer-service-contract.v1.json`, `customer-service-cases.v1.json` | 15 merchant customer cases and their decision contract |
| `m2-external-input-gate.json` | external-input prerequisites; all `ready=false` |

Seven of the 60 cases (five scope denials, one revoked and one expired source)
must be rejected before any Provider request. The remaining 53 are scored.

## Offline checks (`run.sh offline`)

- `pytest` over `eval/test_*.py`.
- `validate_eval.py`: fields, coverage, dates, source versions and SHA-256 of
  the 60 cases; the review checklist must be `VERIFIED_BASELINE`.
- `run_mock_retrieval.py`: deterministic classification counts; no model.
- `run_v2_live_lifecycle.py` without `--live`: cost PLAN, no requests.
- `run_aligned_retrieval.py --revision-root <approved dir>`: deterministic
  Recall@5, MRR and nDCG@5 over the approved alignment. Unapproved or drifted
  mappings stay `NOT_RUN`. `validate_alignment.py` prechecks an alignment.

`ECR_EVAL_REVISION` overrides the approved directory; if it is absent the
retrieval baseline is skipped.

## Real-model runs

All paid calls go through the shared budget ledger
([decision 0006](../docs/decisions/0006-provider-usage.md)). Each runner needs a
protected runtime env (`0600`), refuses an existing output path and stops on a
new quota, authentication or budget error, keeping partial results as
`NOT_PASSED`.

1. **Lifecycle** – `run_v2_live_lifecycle.py --live --runtime-env … --base-url …
   --corpus …/documents.jsonl --report …`. Needs an empty independent V2 index.
   Uploads the 37 documents through Go, waits for Python ingestion, reviews and
   publishes through Go commands, then checks version-bound retrieval and
   citations. 36 documents publish; the one `unclassified` source is rejected.
   `--resume-report` completes review/publication after a failure without
   re-embedding.
2. **Component batch** – `run_bailian_batch.py --plan`, then `--live` with
   `--max-requests`, `--max-requests-per-minute` and `--max-cost-units`. 53
   cases in 11 batches (about 236 requests). Checkpoints after each batch;
   `--resume` requires identical inputs; `--report-checkpoint` exports a
   stopped run.
3. **HTTP/SSE** – `run_v2_quality_e2e.py --runtime-env … --capture-dir …`.
   Requires a passing lifecycle report, a loopback URL and the versioned facts
   fake (`go/cmd/facts-fake`). Repeats idempotency keys, replays SSE from a
   midpoint, and records candidates, reranked evidence and delivered context.
   `--case-id` runs a targeted regression; the gate still needs all 53.
4. **Safety** – `run_v2_quality_safety_http.py --live`: the seven excluded cases
   through Go, with zero Provider requests, no restricted evidence and
   revocation/permission-version checks on old results.
5. **Customer channel** – `run_customer_service.py` (guest Cookie, CSRF,
   idempotency, persisted SSE) then `customer_service_quality.py`.

`run_evidence_boundary_diagnostics.py --live` adds supplemental reference and
clarification scenarios; it never replaces frozen cases.
`run_keycloak_protocol.py --live` checks the Keycloak → Go → gRPC → Python
identity boundary with synthetic accounts.
`diagnose_customer_execution.py` reads one customer execution's stage trace
through restricted read-only connections; it never calls a model.

## Review and gates

Answers are judged against delivered evidence, not lexical overlap.

- `build_human_review_package.py` builds an unreviewed package from HTTP
  captures; `build_human_review_sidecar.py` converts completed reviews into a
  metadata-only sidecar. With explicit user delegation the reviewer may be
  `reviewer_kind=agent`; `human_review_completed` stays `false`.
- `quality_gate.py` scores the original answer points. `evidence_boundary.py
  package | sidecar | gate` scores the evidence-boundary contract. The two
  scores are separate: correctly stating a source gap can pass the boundary
  while the original answer point stays omitted.
- Gate thresholds (both tracks): every case executed and reviewed, zero severe
  safety errors, all high-risk cases pass, ordinary cases ≥ 90%, required
  evidence delivery and applicable citation validity ≥ 95%. Failed, cancelled
  or unreviewed cases cannot pass.
- The customer channel uses the same thresholds over its 15 cases.
- 证据抽检必须在 chunk 全文上做关键词匹配，并对 content 重算 sha256 与 sidecar 声明比对；
  不得基于截断文本判断匹配度。关键词按实际用词检索（文档写“已获批准”时检索“已批准”会漏判）。

## Rules

- Never edit frozen cases, answer points, alignments or contracts to fit an
  answer; a new truth needs a new version and hash.
- Reports hold IDs, ranks, scores, hashes and counts only: no source text,
  answers, prompts, vectors, credentials or DSNs.
- Historical reports are immutable; reruns use new paths.
- Every report keeps `real_service_acceptance=false`. Synthetic results do not
  prove real merchant data, production identity or customer-service quality.
- Provider retries: at most one, for transient 429/5xx/connection/timeout
  failures, within the original deadline and ledger. Schema, truncation,
  authorization and TLS failures are not retried.
