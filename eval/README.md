# Evaluation Sources

## V2 approved-corpus live lifecycle

Run `python/.venv/bin/python eval/run_v2_live_lifecycle.py` from the repository
root for an offline PLAN. It parses controlled Markdown, CSV and reproducible
DOCX fixtures, records their origins/hashes and estimates the complete bounded
batch using reviewed pricing. Planning makes no HTTP or Provider request.

Live acceptance requires an empty independent V2 index, a non-superuser without
BYPASSRLS, running V2 services, and their protected online configuration. The
live path uses the approved 37-document corpus outside Git:

```sh
python/.venv/bin/python eval/run_v2_live_lifecycle.py --live \
  --runtime-env /path/to/protected-runtime.env \
  --base-url http://127.0.0.1:18081 \
  --corpus /var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/documents.jsonl \
  --report /path/to/private/new-lifecycle-report.json
```

Use the already authorized cumulative budget ledger; never initialize a new
window to rerun acceptance. The batch must fit the remaining reservation budget.
The HTTP address and identity must match the runtime. The runner submits through
Go, waits for actual Python ingestion of all 37 documents, reviews/publishes
through Go commands, then checks version-bound retrieval and generation
citations for representative external documents and refusal against the
initial empty index. Unknown command outcomes are polled without resubmission.
PASS requires successful request metadata for control, document/query
Embedding, Rerank and generation in their proper phases, plus all 37 ingested
document identities and the source corpus hash. Frozen classifications permit
36 publications; the one `unclassified` source receives a rejected review and
stays unpublished. Changing its classification to pass acceptance is forbidden.

Reports are new `0600` files containing metadata/hashes, source revision,
migration checksums and budget reservations. They exclude source text, answers,
vectors, credentials and DSNs. Existing reports cannot be overwritten. The
index contains published versions after the run, so the same runtime cannot be
used for another empty-index run. Development identity and synthetic fixtures
keep `real_service_acceptance=false`; this is V2 development acceptance rather
than real OIDC, merchant API, M2/M3 or production acceptance.

The offline plan still uses three deterministic Markdown/CSV/DOCX fixtures so
it can run without the external corpus. The 37-document live runner rebuilds
uploadable Markdown from immutable chunk metadata; it records original
per-document hashes separately from the hash of the submitted bytes. It keeps
`internal_only` and `unclassified` disclosure classes. Internal sources may be
published for authorized internal use; unclassified sources stay unpublished.
Neither class becomes customer-copyable evidence.
The Markdown upload manifest carries the frozen `effective_from`/`effective_to`
values; chunk scope/classification/dates must agree with document metadata.
PASS checks that persisted chunk dates equal the source values. Earlier reports
without dates only prove their recorded scope and cannot prove temporal filters.

If ingestion completed but review/publication failed, `--resume-report` accepts
the original failed report only for the same database, corpus hash, uploaded
hashes, disclosure classes, job and versions. Use a new `--report` path and the
same runtime and ledger. It rechecks live review states and completes remaining
Go commands without resubmitting documents or repeating document Embedding.
The new report binds the original report hash and source revision and retains
its Provider phase evidence; the failed report is never changed. Query failures
require diagnosis and an explicit rerun: previous query phases/results remain
as superseded diagnostics, and all intervening ledger attempts are recorded
separately and do not count toward successful lifecycle slot coverage.

This directory contains versioned evaluation definitions, not customer data or
credentials. The first 60-case set uses a deterministic synthetic tenant set
for policy, FAQ, factual mock, and access-control scenarios. The frozen 60
cases, 37 synthetic documents, approved answer points, and evidence sidecars
remain a separate baseline; public-source candidates do not rewrite or supply
their business truth.

Current aligned synthetic status is revision-1: all 60 source mappings have
been reviewed and approved for this synthetic corpus, validation is `PASS`, and
the deterministic retrieval report is `RUNTIME_PASS`. The reports remain
synthetic-only and carry `real_service_acceptance=false`; this does not change
the immutable 60-case input or claim real merchant, model, or customer-service
quality.

`public-data-sources.manifest.json` is the versioned source admission register
for public datasets, open-source service targets, and unresolved source gaps.
Each record declares category, record type, language, jurisdiction, license
and privacy state, permitted/prohibited use, acquisition state, and provenance
notes. `validate_public_sources.py` checks it offline. A dataset cannot be
marked admitted or downloaded until its source terms and license scope are
verified, privacy/field review is complete, a revision is pinned, and restricted
storage is declared. Service targets and source gaps cannot claim downloaded
datasets. No raw public data is checked into Git.

The current source rails are deliberately distinct:

- Keycloak has local synthetic-account OIDC protocol integration. Its pinned
  distribution, Apache-2.0 license and third-party notice hashes are recorded
  in `docker/identity/images.lock.json`; browser login and production identity
  are not accepted by this evidence.
- Medusa 2.21.2 is the target local REST service. The recorded MIT scope
  excludes Enterprise-only content. It is not a real merchant API, and its
  local catalog/orders are test records.
- Open Food Facts is a candidate food-product catalog. Its official terms
  distinguish ODbL database, DbCL individual content, and CC BY-SA product
  images; the first candidate excludes images and still needs field/scope
  review.
- UCI Online Retail II remains blocked: its page displays CC BY 4.0 while the
  UCI API license metadata is blank. Do not download it until that conflict,
  selected fields, and privacy handling are resolved. Any derived local
  inventory/order fixtures must remove customer identifiers and bind source
  revision, input hash, and transformation version.
- Amazon ESCI and Amazon Reviews remain separate product retrieval/parsing
  candidates pending license and privacy review. Hugging Face distribution is
  not itself a license grant.
- Chinese official FAQ/regulation sources and Chinese community Q&A remain
  explicit source gaps. English community QA may be evaluated on its own track
  after license, attribution, and privacy review; it is never merchant-policy
  or customer-reply truth.

Publicly sourced data may be called real public-source data for its documented
scope. Open-source local service integration and records derived into a local
Medusa test store are separate evidence classes. None represents real
merchant-production data, and every record retains
`real_service_acceptance=false`.

`run_public_data_baseline.py` is a separate public product-data track. It does
not read `synthetic_cases.jsonl`, call a model, or contact the network. Until a
source is downloaded outside the repository and its revision, license, and
original terms are verified, it prints `status=NOT_RUN` with null metrics. For
an approved restricted JSONL/CSV input it performs field mapping, whitespace
cleaning, duplicate removal, and label/rank based Recall@K, MRR, and NDCG
calculations. Reports include `source_id`, `revision`, `license_status`,
`input_sha256`, `pipeline_version`, and `real_service_acceptance=false`.
ESCI is restricted to product retrieval/ranking metrics. Amazon Reviews is
restricted to product metadata/text parsing, mapping, deduplication, and text
retrieval experiments. The runner remains a product-ranking tool; other source
categories need their own purpose-specific evaluator and admission checks.
None of these sources is merchant policy, current price/inventory/order,
permission, or customer-reply truth.

The public-source manifest, mapping, JSONL, and CSV inputs use strict UTF-8
parsing. Duplicate JSON keys, malformed JSON, non-object JSONL rows, invalid
encoding, duplicate CSV headers, and CSV rows with extra fields fail as
`FAIL input_integrity` before a report is written. A pending source remains
`NOT_RUN` only when its manifest is structurally valid but its revision,
license/terms, or restricted download is not verified.

The authoritative source and responsibility matrix is in
`.local/dev-docs/docs/数据来源与评测输入.md`. Codex owns synthetic dataset
construction, evaluation review, public-source/license checks, heuristic UX
review, and synthetic tenant/role fixtures for the prototype. These are
engineering outputs; they do not assert real merchant policy, real identity
integration, or production authorization. Real merchant inputs are deferred
until the prototype is runnable and a real trial is planned.

`build_synthetic_eval.py` creates 60 deterministic cases with concrete product,
policy, factual, freshness, refusal, multi-turn, and authorization scenarios.
`validate_eval.py` is read-only by default: it checks fields, coverage,
non-placeholder semantics, dates, source versions, and SHA-256. Passing
JSONL rows must be objects with unique keys; duplicate keys, non-object rows,
invalid UTF-8, malformed JSON, and malformed metadata fail closed with
input-integrity errors. `--review-output` explicitly writes a separate checklist whose 60 rows start
with `review_status=pending`; the checklist never changes the JSONL hash. The
default `--review` check verifies that checklist IDs and copied evidence fields
still match the immutable JSONL and that statuses are one of `pending`,
`approved`, `needs_revision`, or `rejected`. Defaults resolve relative to this
script, so the command can be run from any working directory. It reports
status counts, missing notes, duplicate IDs, evidence drift, and either
`PENDING_REVIEW` or `VERIFIED_BASELINE`.

The generated set is a development fixture and cannot be used to claim M2
real-service acceptance. `esci-lite.manifest.json` remains the compatibility
manifest for the ESCI-lite subset. `public-data-sources.manifest.json` records
the current dataset, service-target, and source-gap decisions. No public raw
data is downloaded or committed until its revision, terms/license, privacy
review, and SHA-256 are recorded. A public page or distribution channel does
not by itself grant permission; the dataset card and original source terms
control use.

Each case includes a query, expected document IDs, answer points, intent,
information source, tags, authorization context, business date, and source
metadata. A real evaluation run must add human verification, retrieval and
answer metrics, usage, latency, and cost without writing secrets or customer
content.

The review checklist is parsed as strict UTF-8 JSON: duplicate object keys,
non-array roots, non-object rows, non-string case IDs, non-string or unknown
review statuses, duplicate case IDs, and evidence drift are rejected. Allowed
review statuses are `pending`, `approved`, `needs_revision`, and `rejected`;
revision or rejection requires a non-empty note. The immutable JSONL is never
modified by review validation.

`run_mock_retrieval.py` produces a read-only deterministic baseline summary,
validates the input SHA-256, and reports retrieval, evidence coverage, refusal,
unauthorized, and multi-turn classification counts. It rejects duplicate JSON
keys, non-object JSONL rows, invalid UTF-8, malformed metadata, duplicate
fixture document IDs, and malformed fixture/gate files before producing a
report. Input failures are reported as `FAIL input_integrity` without a
traceback or partial report. `--include-cases` adds
metadata-only per-case results (case ID, classification, tags, authorization
scope, and expected evidence count); it never emits query or answer text.
By default no synthetic document index is assumed, so retrieval hit rate and
document coverage are explicitly `NOT_RUN`. Use `--fixture-doc-ids` only with
an approved metadata-only JSON document ID fixture to calculate those metrics;
document bodies are never accepted by this tool. Its output is not M2
real-service acceptance. Reports include the input hash, pipeline version, a
stable local run ID, and a fixture hash when supplied; the timestamp is run
metadata and is not used as an evaluation value. `expected_evidence_rate`
means only that cases declare expected documents; it is not runtime evidence
coverage. Source type/version drift is reported as input integrity failure.

`m2-external-input-gate.json` is the checked-in, non-secret responsibility
record for the seven P7 external-input prerequisites. Every requirement is
currently `ready=false`; it records the accountable role and required evidence
without inventing an identity source, merchant authorization, Provider
credential, or license. Passing the machine check with this manifest reports
`m2_gate.status=BLOCKED` and never changes `real_service_acceptance`.
The manifest uses a strict versioned schema: unknown root fields and unknown
fields inside a requirement are rejected, so credentials, endpoints, or
unreviewed readiness metadata cannot be smuggled into the responsibility
record. Only the declared `ready`, `owner`, and `evidence` fields are accepted
for each requirement.
The optional document-ID fixture measures document presence only: this tool
does not execute Search or Generate, so those values are not query retrieval
recall or runtime answer quality. The current demo corpus uses different IDs
and tenant/shop mappings from the immutable 60-case set. The gated aligned
track is implemented by `build_aligned_fixture.py` and
`run_aligned_retrieval.py`: the builder parses the local
`data/synthetic/ecommerce-demo-v1/manifest.yaml` into actual
document/version/chunk metadata and never copies query or answer text from the
60 cases. It creates a pending `alignment.json`; mappings must use the exact
immutable `expected_doc_ids` as keys and actual corpus document IDs as values.
Only an explicitly reviewed mapping with `status=APPROVED` allows Recall@5,
MRR, and nDCG@5. Missing, drifted, unauthorized, or date-incompatible
mappings remain `NOT_RUN`; an ID alias or derived document is never accepted
as a hit.

`validate_alignment.py` is the read-only precheck for that mapping. It accepts
explicit paths from any working directory, verifies the immutable 60-case
input hash, corpus document and chunk identity, mapping keys and source IDs,
tenant/shop scope, and effective-date fields. It reports
`PASS`, `PENDING_REVIEW`, or `FAIL` and never changes `alignment.json`, approves
a mapping, copies case text into the corpus, or calculates retrieval metrics.
The independent `ecommerce-m2-aligned-v1` corpus contains 37 synthetic,
provisional documents. Its proposal and review history are kept outside the
repository under
`/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/`. The current
controlled artifacts are `alignment-revision-1.json`, its `PASS` validation
report, and its deterministic retrieval report; earlier pending proposals are
retained only as historical evidence.

To reproduce the approved revision without accidentally selecting the older
pending directory, pass its root explicitly:

```bash
python3 eval/run_aligned_retrieval.py \
  --revision-root /var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional \
  --output /var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/retrieval-report-current.json
```

`--revision-root` selects the standard revision-1 cases, metadata, corpus, and
approved alignment filenames as a single set. Explicit `--cases`, `--metadata`,
`--corpus`, or `--alignment` arguments still override those defaults. Without
the option, the runner retains the immutable baseline cases and the historical
pending alignment path, so an unapproved mapping remains `NOT_RUN`.

`alignment_policy.py` is the shared disclosure and effective-date policy
check. Each source chunk must declare `external_allowed`, `internal_only`, or
`unclassified`, plus tenant/shop scope and effective dates. Operator access to
internal or unclassified material, expired material, and not-yet-effective
material is reported separately. `run_aligned_retrieval.py` counts a policy
refusal only when the case explicitly expects an unauthorized or unanswerable
result; ordinary Recall/MRR/nDCG never includes a refused case, and an
unexpected policy block keeps the run `NOT_RUN`.

`review_alignment.py` creates a separate 60-row source-mapping checklist with
`pending`, `approved`, `needs_revision`, or `rejected` states. It verifies the
proposal hash and every copied evidence field, detects tampering, and reports
`REVIEWED` only when all rows are approved. `REVIEWED` is still not an
`alignment.json` approval; the retrieval runner continues to require a
separately controlled `status=APPROVED` mapping.

`build_alignment_revision_proposal.py` creates a read-only decision aid for
unresolved mapping rows. It records immutable case, review, and source hashes,
the current evidence wording, and explicit options to narrow the claim, add
traceable source evidence, or keep the row unresolved. It never edits the
evaluation JSONL, review checklist, source corpus, or `alignment.json`; a
revised evaluation input must receive a new version and SHA-256.

`approve_alignment.py` is the separate explicit approval gate. It requires a
`PENDING_REVIEW` proposal, an exact proposal hash in the review checklist,
zero `pending`/`needs_revision`/`rejected` rows, and unchanged evidence fields.
Only `--approve` writes a new artifact, atomically and without overwriting an
existing file. It refuses any checklist with unresolved rows and never
modifies `alignment.json` or the immutable evaluation input. The historical
provisional 59/1 review is not the current baseline.
Authorization fields are checked for tenant, shop, and role completeness;
unauthorized cases must retain a target evidence/document reference. The
synthetic set models an unauthorized request with a normal operator context,
so the evaluator does not invent a `denied` shop or role.
Answer points must be non-empty strings. Refusal/unauthorized cases require
guidance points, and multi-turn cases require both the `multi_turn` tag and a
target document/routing reference.
With `--include-cases`, the report also includes status counts and a list of
failed case IDs only; it does not include query or answer text.
Every report also contains an `m2_gate` object. It remains `BLOCKED` until
identity/role revocation, tenant/shop mapping, material authorization,
business-date rules, Provider contract, and material version/license/redaction
inputs are separately recorded. The evaluator never infers readiness from
local fixtures. Each gate item includes its accountable owner and the evidence
needed to mark it ready; readiness values remain false until reviewed inputs
are recorded in the project process.
Reports include `report_sha256`, calculated from canonical stable content while
excluding the run timestamp and the hash field itself, so archived reports can
be compared without treating timestamps as evaluation changes.
The default report contains no runtime timestamp and is byte-identical across
repeated runs. A reviewed external-input gate manifest may be supplied with
`--gate-manifest`; it must match the owner/evidence contract for all seven
requirements. Even a `READY` gate does not claim real-service acceptance or
invoke a provider.

The checklist is an evidence record, not an approval mechanism. Only the
business reviewer may change a row to `approved`; engineering must not infer
approval from a passing machine precheck. The synthetic set is a verified
baseline only when all 60 rows are `approved` with no unresolved notes.

## Isolated Bailian evaluation

## V2 quality baseline and gate

`run_bailian_batch.py --live` produces the bounded, metadata-only baseline for
the approved revision-1/context-v2 inputs.  The report binds the immutable
case, corpus, alignment and evidence-review hashes to the Git commit,
generation-instruction hash and redacted four-slot Provider configuration.
The current plan is 53 `WITHIN_BUDGET` cases in 11 batches (236 request
equivalents); seven scope, revoked or expired cases are excluded before any
Provider request.  A completed Provider batch is transport and retrieval
evidence only and keeps `answer_quality_status=NOT_RUN`.

`quality_gate.py` consumes that report and separates Recall@5/MRR/nDCG from
approved-support delivery.  It reports `NOT_PASSED` until a separately
protected `v2-quality-review-v1` sidecar covers every case.  The sidecar stores
only case IDs, review outcomes, severity, citation validity and answer-point
counts; it must bind to the exact baseline report SHA-256.  The gate requires
zero security errors, all high-risk cases reviewed successfully, at least 90%
ordinary-case passes, and at least 95% approved-support delivery and citation
validity.  Lexical overlap and structured-output success never approve semantic
quality.  Both the gate and all reports retain `real_service_acceptance=false`.

After the controlled ingestion lifecycle has passed, `run_v2_quality_e2e.py`
can submit the same cases through Go HTTP/SSE and record only request, status,
answer-hash, evidence-ID and citation metadata.  It refuses to run without a
passing lifecycle report and a loopback URL.  The existing lifecycle runner
remains the authoritative test for upload, review, publication, persistence,
replay and cross-process recovery; this driver does not bypass those contracts.
The driver now repeats the turn idempotency key, compares refreshed results and
replays SSE from a midpoint. FAILED/CANCELLED results prevent PASS; every case
remains in the new report. `--capture-dir` writes immutable raw turn results in
an owned directory outside Git (0700 directory, 0600 files).
Live runs require `--runtime-env`: the report binds the protected configuration,
model/profile/schema/instruction hashes, implementation hashes, Git commit and
existing budget-window identity, plus request-count boundaries and case latency.
Repeated `--case-id` selects a targeted regression; the final gate still requires
all 53 cases. `quality-scenarios.v1.json` separately binds controlled conversation
prerequisites to the frozen case/query hashes. Each scenario submits its prior
turns through Go in the same conversation and requires DONE, idempotency and
replay before querying the unchanged evaluation case; failed prerequisites fail
the case. Reports and restricted captures bind prerequisite captures and
persisted `fact_read` metadata (tool/resource, observation time, value hash,
mock marker). No history or final result is inserted directly into SQL.
The earbud fake fact is a separate test record, deliberately different from
the historical document snapshot; it cannot become merchant truth.
Go validates a unique explicit user SKU and shop ownership before rereading
inventory/price. Multiple candidates and assistant-invented identifiers clarify.
Python receives history as untrusted user data, never a system instruction.
Catalog suitability recommendations reach document retrieval before selecting a
SKU; explicit live inventory and price requests retain the resource checks.
Generation retains a bound fact's resource ID, observation time and mock marker.
Clarification workflow questions require a validated `clarification_plan` with
separate object, question-type, time, region and source-scope questions; Python renders the actual model
questions before subsequent advice. Schema validity never approves their meaning.
Each question is bounded to 40 characters and the subsequent answer to 120;
the workflow prompt targets 30–60 characters and does not include the ordinary
answer-length target. Ordinary answers have no minimum length. The generation slot and its 512-token parameter
remain fixed; an overlong or invalid answer fails without automatic retry.
Successful Go facts answer the already-bound resource without asking for the
same identifier again or mixing in uncited historical snapshot values. Logistics
status access does not provide a delivery-promise capability, even with an order ID.
Workflow citations must support the clarification prerequisites as well as any
search-count policy; a retrieved topic does not identify the user's missing object.
Workflow context retains the scoped and disclosure-filtered top eight reranked
chunks together, so a later topical selector cannot discard prerequisite rules.
Document answers require a nonempty citation list drawn from the supplied IDs;
facts-only answers remain separate. Missing citations fail rather than being
filled from evaluation truth. Semantic support is still reviewed independently.
Facts and clarification workflows use focused generation instructions so unrelated
capability metadata or the ordinary answer-length target cannot displace their
requirements. Re-reading a fact does not prove its underlying observation is fresh.
An actual unresolved prior reference with no user history requires a control
clarification and zero extracted resources/tools before retrieval; workflow
quotations and explicit resource IDs retain their respective routing.
Control reference resolution receives prior user messages only. Assistant
mentions of additional sources cannot undo the user's unique product or
standard selection. Missing versions or publication dates are evidence gaps
for the selected resource, rather than unresolved resource identity.
Document storage versions are provenance, not external standard revisions or
publication dates. Missing source dates/versions must remain explicit gaps;
the evaluator never repairs the frozen truth to make them pass.

Build a separate HTTP answer review package with `build_human_review_package.py`
using the HTTP report and capture directory. It shows actual answers, evidence,
citations, customer reply and frozen expected points, and starts unreviewed.
The original component review cannot approve different HTTP answer hashes.
The HTTP batch stops after a new shared-ledger account/configuration error
(`allocation_quota`, authentication or budget blockage). It preserves the failed
capture and reports planned, executed and unrun cases separately; a partial
batch cannot satisfy the 53-case gate. Historical ledger errors do not stop a
new authorized run, and transient failures retain their ordinary diagnostics.
For HTTP captures, `build_human_review_sidecar.py` requires completed per-case
review fields and explicit delivered support IDs; it never assigns support
automatically. The quality gate accepts these via `--e2e-review` and
`--e2e-capture-dir`, alongside the original component review.
After explicit user delegation, both review tools accept `--reviewer-kind agent`.
Complete every case's `review_fields` by reading the answer and delivered chunks,
then write a protected `agent-attestation.json` with version
`v2-agent-review-attestation-v1`, `reviewer_kind=agent`, `review_completed=true`,
`human_review_completed=false`, `delegation_authorization=explicit_user_instruction`,
`review_method=delegated_agent_semantic_review_v1`, the package/report/cases hashes,
reviewed count and `real_service_acceptance=false`. The converter rejects missing
delegation, unreviewed cases, changed review inputs and support outside the capture.
Agent provenance remains explicit in the gate; thresholds are unchanged. A request
to approve every case never overrides an observed omission or contradiction.
Free-text review notes and point annotations stay in the restricted package;
the sidecar exports only check flags, point states, support IDs, hashes and
bounded lowercase issue codes. Notes never become report fields.
The gate scores component `semantic` and HTTP `e2e_semantic` independently,
with reviewed/passed counts, failed/high-risk case IDs, answer-point support,
omission, contradiction and distortion rates. Refusal/clarification accuracy
uses only frozen contracts that explicitly require it; an empty subset is
`null`, not perfect accuracy. Metadata metrics never include private notes or
answer text. Component support delivery/ranking metrics describe the component
context; they cannot establish delivery or answer quality in the HTTP layer.
HTTP packages also accept a complete captured `NOT_PASSED` batch so failed
executions can be reviewed and preserved. A failed/cancelled turn must receive a
failed review; this never changes the batch status or the gate's execution checks.

Overall PASS also needs seven-case Go HTTP/SSE safety evidence. The existing
`run_v2_quality_safety.py` context-audit report proves pre-Provider exclusion;
it does not establish entrypoint authorization, revocation or expiry. Missing
HTTP semantic review or entrypoint safety keeps the overall gate NOT_PASSED.
`run_v2_quality_safety_http.py --live` exercises the seven frozen safety queries
against Go and checks the original shared ledger before/after each query. It
requires matching lifecycle/E2E hashes, protected runtime/authority configurations,
an explicitly named independent database, non-superuser/no-BYPASSRLS roles and
matching migration checksums. Only reversible identity/temporary ACL fixtures are
changed; source and result tables are never written. PASS requires actual local
authorization denial, zero Provider requests, no restricted evidence, persisted
terminal/SSE replay where a turn exists, and four old-result/replay revocation or
permission-version checks. Offline exclusion evidence cannot replace this report.
Safety can be measured against a complete captured `NOT_PASSED` entrypoint batch;
case identity and a persisted result for revocation replay remain required. Its
safety PASS never changes execution or semantic status in the final gate.

`run_bailian_retrieval.py` is an explicit, metadata-only provider experiment.
Without `--live` it performs input checks and reports `NOT_RUN`; with
`--live --limit N` it reads the approved synthetic aligned corpus, calls the
Alibaba Bailian embedding, rerank, and generation slots, and writes only
case/document IDs, ranks, scores, latency, redacted model/request-ID
fingerprints, usage presence, and structured-output counts. It never writes
vectors, prompts, generated text, customer data, or database/M1 state. The
default limit is three cases to keep the experiment within the reviewed quota.

The prior revision-1 v2 report is outside the repository at
`/var/lib/ecommerce-rag/real-docs/reports/bailian-retrieval-revision-1-current.json`.
The new five-case v3 report is
`/var/lib/ecommerce-rag/real-docs/reports/bailian-retrieval-revision-1-context-v3.json`.
It verifies actual delivery of the reviewed source bodies for all five cases.
Reports retain `real_service_acceptance=false`,
`model_quality_claim=false`, and `answer_quality_status=NOT_RUN`. A
successful transport and retrieval ranking run does not approve model quality,
answer quality, real merchant data, identity, or online customer service.

### Answer-point evidence sidecar

`answer_point_evidence.py` validates a separate, metadata-only review sidecar
that binds each expected answer point to chunks already present in the
approved alignment. It verifies the immutable case, alignment, and corpus
hashes, rejects duplicate keys and cross-alignment chunks, and never modifies
`synthetic_cases.jsonl` or source artifacts. The current approved revision-1
sidecar is outside the repository at
`/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional/answer-point-evidence-review-revision-1.json`;
all 60 points are approved, `unresolved_point_count=0`, and the sidecar status
is `APPROVED`. The older v1 sidecar with 59 of 60 points and `syn-005` marked
`needs_revision` remains historical evidence and is not overwritten.

When supplied with `--answer-evidence`, the v3 evaluator distinguishes approved
source support from approved chunks actually delivered to generation. Every
case ranks only its own tenant/shop, `external_allowed`, and business-date
eligible chunks. Effective end dates are exclusive. Document embeddings may
be batched across cases, but ranking, rerank candidates, and generation
contexts stay within each case's scope. Generation uses only chunks returned
by rerank; an empty rerank result fails without calling generation.

The metadata-only `generation_evidence` records chunk/document/version IDs and
chunk hashes. `approved_support_chunk_coverage` compares each answer point's
sidecar bindings against that actual context. The conservative
`all_bound_chunks_v1` method requires every bound chunk to be delivered;
approved source support alone cannot count as delivered support. Missing
bound chunks produce `approved_evidence_not_delivered`, separately from
`evidence_unsupported`, `generation_empty`, and `generation_rewrite_mismatch`.
Lexical exact/token matches do not assess semantic answer quality.

The prior revision-1 v2 report recorded source approval for all five selected
cases and `generation_rewrite_mismatch` with exact/token match 0/5. It did not
prove that all approved support reached the generation context. The original
sidecar includes frontmatter/headings, and four points bind six chunks each.
Separate `answer-point-evidence-review-revision-1-context-v1.json` and
`answer-point-evidence-review-revision-1-context-v2.json` artifacts retain
lineage to that review. Eight points were re-reviewed against source bodies;
the other 52 bindings remain unchanged. The first five cases have the same
body bindings in both new artifacts. Their v3 live report uses context-v1 and
verifies actual support delivery for 5/5; exact/token matching remains 0/5.
Neither lexical mismatch nor delivered evidence establishes semantic answer
quality. Retain prior reports and immutable inputs unchanged.

### Offline Context Audit

`--audit-context` is mutually exclusive with `--live`. It checks approved
bindings against tenant/shop, disclosure, business dates, and the union of
required chunks across answer points. It never loads Provider configuration,
selects ranking evidence, approves a review, or calls a model. Use `--limit 60`
for the complete input; results distinguish `WITHIN_BUDGET`, `BUDGET_EXCEEDED`,
`INELIGIBLE_REQUIRED_CHUNKS`, and `UNREVIEWED`. A feasible binding does not
prove that retrieval will deliver it.

The context-v2 audit has 53 `WITHIN_BUDGET`, zero `BUDGET_EXCEEDED`, and seven
`INELIGIBLE_REQUIRED_CHUNKS`. Those seven are the five scoped denials and two
revoked/expired cases; their evidence must not enter generation. The audit
retains `generation_context_selection=NOT_RUN` and
`answer_quality_status=NOT_RUN`.

### Bounded context-v2 batch runner

`run_bailian_batch.py` is the next step after the offline audit. It selects
only the 53 `WITHIN_BUDGET` revision-1 cases and keeps all seven
`INELIGIBLE_REQUIRED_CHUNKS` cases out of provider calls. It uses the same
scope, disclosure, business-date, alignment and evidence-sidecar checks as
the single-window evaluator, then groups cases deterministically by `case_id`.

The runner has explicit `--max-requests`, `--max-requests-per-minute`, and
`--max-cost-units` gates. A cost unit is one provider request equivalent, not
a currency estimate. Document embedding calls are counted by document batch;
each eligible case additionally reserves one query embedding, one rerank and
one generation request. The default plan is 53 cases in batches of five, an
estimated 236 request equivalents, and a 240 request/cost ceiling. The
rolling RPM gate waits before making a request; total or cost exhaustion
fails closed.

Without `--live`, the command prints the partition and estimate and makes no
network request or credential read:

```bash
python3 eval/run_bailian_batch.py --plan
```

Live execution is an independently approved, metadata-only experiment. It
stores a restricted checkpoint after each successful batch and stops on the
first provider, budget, or case failure, without calling later cases in that
batch. New failed checkpoints retain only case IDs, known error classifications,
and consumed request counts; exception messages and responses are discarded.
`--resume` requires an exact input
hash and plan identity; a changed case, corpus, alignment, review, or batch
size cannot reuse the checkpoint. Reports and checkpoints contain IDs,
counts, redacted metadata and hashes only, with
`real_service_acceptance=false`, `m1_connected=false`, and
`answer_quality_status=NOT_RUN`. A passing batch run still does not mean
semantic answer quality, real merchant data, or M2 customer-service
acceptance.

For a stopped run, export its verified progress without reading Provider
configuration or making requests:

```bash
python3 eval/run_bailian_batch.py --report-checkpoint \
  --checkpoint /restricted/path/run.checkpoint.json \
  --output /restricted/path/run-failed.json
```

This does not modify the checkpoint. The report distinguishes completed cases,
unmeasured cases, excluded cases, and missing legacy failure details. It retains
the failed status and reports the estimated requests required to rerun the
unfinished batch and finish the plan. Already reserved requests remain spent;
resume can exceed the original window even when the original plan fit it.
The exporter checks input identity and completed payload hashes, uses mode
`0600`, and refuses to overwrite existing evidence. A successful export is
not a successful evaluation or authorization to raise the request limit.

### Answer Composition Diagnostics

Each case also reports `answer-point-composition-v1`, a metadata-only heuristic
that checks whether expected-point token overlap appears only after combining
multiple generated entries, whether a generated entry overlaps multiple
expected points, script relation (`same_script`, `opposite_script`, `mixed`,
or `unclassified`), and counts for qualifier markers, numeric literals, and
unit categories. Reports retain short fingerprints and counts, never generated
or expected text. Fake-provider tests exercise split, combined, Chinese/English,
and qualifier cases, including an existing immutable synthetic answer point.

These signals do not establish entailment, contradiction, correct negation, or
semantic answer quality. A unit-category match does not prove that the unit is
attached to the right value. They do not change the existing exact/token
diagnostics or `answer_quality_status=NOT_RUN`. The isolated v4 diagnostic run
used five approved synthetic cases and context-v2 evidence review; its
metadata-only report is outside the repository at
`/var/lib/ecommerce-rag/real-docs/reports/bailian-retrieval-revision-1-context-diagnostic-v4.json`.

Without `--output` the audit only prints metadata. Explicit output creates a
new report atomically with mode `0600`; both audit and live mode refuse an
existing output path before making Provider calls, preserving historical
evidence. Choose a new report filename for every controlled rerun.

## Isolated Bailian pgvector evaluation

`run_bailian_pgvector.py` is an explicit embedding-to-database experiment.
Without `--live` it reports `NOT_RUN` and makes no network or database request.
With `--live`, it requires `/etc/ecommerce-rag/m1-review.env` (or an explicit
`--db-env`) to name an `ecr_m1_test_*` database and the non-superuser
`rag_app`. It embeds only the approved synthetic aligned corpus, inserts
vectors into PostgreSQL TEMP tables, applies tenant/shop, `external_allowed`,
and business-date filters, emits metadata-only top-5 results, and rolls back
the transaction. The report never stores vectors, document text, prompts,
credentials, or customer data. This proves an isolated provider/database
contract only; it is not persistent vector quality, M1 integration, or real
M2 service acceptance.

The current report is outside the repository at
`/var/lib/ecommerce-rag/real-docs/reports/bailian-pgvector-current.json` and
has `real_service_acceptance=false`.

Both isolated Provider evaluators now require an explicit `document_version_id`
for every corpus document and chunk. They reject duplicate chunk IDs, parent
document/version drift, and tenant/shop mismatches before sending embeddings.
The retrieval and pgvector reports retain document/version/chunk IDs in their
metadata-only result records. This binds ranking evidence to an immutable
document version; it does not publish the version, persist production vectors,
or make the experiment M2 real-service acceptance.

Quality repair runs can select eligible cases with repeated `--case-id` options;
excluded safety cases are never admitted to the component Provider runner.
Context selection expands a ranked hit by up to two source neighbors within the
same authorized document version, with an eight-chunk total limit; it does not
read answer-point bindings. The frozen bindings remain scoring truth.

For semantic engineering review, explicitly supply `--capture-dir` pointing to
an owned `0700` directory outside the repository. Each generated answer and its
actual evidence is stored once in a new `0600` file. Metadata reports retain only
its opaque ID and SHA-256. Capture is optional for diagnostics, but a semantic
quality claim requires review bound to the captured answer and report hashes.
Delivery-promise questions require a model-generated capability-limit field
alongside the answer. The returned statement is rendered verbatim and must
explain that logistics-status lookup does not supply an arrival promise;
having complete order details does not remove that capability gap. Schema
validity still requires separate semantic review of the actual statement.
Document-only answers also return `evidence_limits` and `next_inputs`. Their
actual model text is rendered separately so applicable conditions, source
limits and missing original records are not discarded in a short summary.
These fields can be empty when inapplicable; their presence does not establish
correctness. Facts and clarification workflows retain their separate contracts.
Review the complete applicability conditions and the trigger for any default
workflow count. A source label or an uncertainty statement cannot substitute
for a missing condition, and lack of proof does not establish a negative fact.
Rule/standard questions return model-selected applicability dimensions found
literally in the supplied source bodies. The adapter presents those dimensions
without inventing their values or asserting that a particular rule applies.
Unknown dimensions fail closed; complete and relevant selection still needs
semantic review.
Why/reason questions require an actual model statement about source support
for the reason; a specification difference cannot substitute for a missing
purpose. Uncertain use questions must preserve uncertainty, and comparisons
must retain source-backed numerical differences relevant to the choice.

Model-output review is distinct from the previously approved source bindings.
The run identity now includes source hashes, retrieval/context parameters,
generation adapter parameters, Provider configuration and budget window.

`build_engineering_review.py` reads the restricted capture directory and emits
an explicitly non-human `reviewer_kind=engineering` sidecar. It binds each
point to the frozen case hash and delivered evidence; token overlap is only a
conservative diagnostic. The gate keeps `human_review_completed=false` and
cannot turn this report into human approval. `run_v2_quality_safety.py` records
all seven excluded cases and verifies zero Provider-attempt delta. The HTTP/SSE
quality driver requires this safety report, a real passing lifecycle report,
53 eligible cases, terminal persistence and monotonic SSE event IDs; it cannot
silently send excluded cases.

`build_human_review_package.py` creates a new, owned `0700` package outside the
repository for the business reviewer. It binds all 53 eligible cases to the
baseline report hash, frozen case hash, answer capture hash, generated answer
points and the exact evidence shown to the model. Package case files are
`0600`; the manifest starts with `human_review_completed=false` and contains
review fields only as an unapproved template. The reviewer must not edit the
query, expected points, evidence, or hashes. A later human sidecar must bind to
the same baseline and captures before `quality_gate.py` can evaluate it.

Chat HTTP 200 alone is insufficient: incomplete finish reasons (`length`,
`content_filter`, `tool_calls`), invalid envelopes/request identity, structured
JSON/schema or usage fail before the shared ledger records success. Failed chat validation
keeps the request fingerprint, HTTP status, valid usage and full reservation;
response bodies remain outside the ledger. Historical attempts are not
rewritten. This diagnostic contract is not semantic approval or a retry policy.
Schema failures log only the slot, failed constraint, numeric schema position and
response hash. Property names and model text stay out of logs; a successful later
diagnostic cannot establish the cause of an uncaptured historical failure.

## Independent V2 evidence-boundary acceptance

`evidence-boundary-contract.v1.json` freezes all 53 eligible cases before new
answers are generated. It binds case/query hashes, the 37-document corpus,
alignment, original answer points, approved evidence sidecar and separate
conversation scenarios. Risk levels and the seven excluded safety cases remain
unchanged. Original answer-point scoring in `quality_gate.py` runs independently;
correctly explaining a source gap can pass the boundary contract while leaving
an original answer point omitted. Neither contract nor historical evidence is
rewritten to fit a response.

The historical track uses business date `2026-09-29` in a protected evaluation
configuration. The current track uses current business time to test stale
stock, price, promotion, notice, standard and reporting claims. Identity/token/
scope expiry, permission revision, revocation, deadlines and the 60 RPM shared
quota always use real time. Models, parameters and 1024-dimensional embeddings
remain fixed; online work uses the existing shared budget ledger.

Start with read-only integrity and plan checks:

```bash
python3 eval/evidence_boundary.py validate
python3 eval/run_evidence_boundary_diagnostics.py
```

`run_v2_quality_e2e.py` uses the existing HTTP/SSE driver, real Go prerequisites
from `quality-scenarios.v1.json`, restricted captures, and read-only database
trace inspection. A new run records candidates, reranked evidence and delivered
context bindings. Database `chunk_hash` is a pipeline fingerprint, not a SHA-256
of plain body text: verify document/version/source hash and chunk fingerprint
against lifecycle evidence, and separately verify `snippet_sha256` against the
actual captured body. Missing persisted evidence fails acceptance; a diagnostic
read error is redacted and retained without discarding earlier case results.
Older reports without these bindings stay unchanged and cannot satisfy the new
proof requirement.

After a complete 53-case batch, create a new restricted package, review each
actual answer against its delivered evidence and both contracts, then create
two separate sidecars. For example, with absolute restricted paths supplied:

```bash
python3 eval/evidence_boundary.py package \
  --report "$BOUNDARY_REPORT" --capture-dir "$BOUNDARY_CAPTURES" \
  --package-dir "$BOUNDARY_PACKAGE"
python3 eval/evidence_boundary.py sidecar \
  --report "$BOUNDARY_REPORT" --capture-dir "$BOUNDARY_CAPTURES" \
  --package-dir "$BOUNDARY_PACKAGE" --output "$BOUNDARY_REVIEW" \
  --original-output "$ORIGINAL_REVIEW"
python3 eval/evidence_boundary.py gate \
  --report "$BOUNDARY_REPORT" --capture-dir "$BOUNDARY_CAPTURES" \
  --review "$BOUNDARY_REVIEW" --original-review "$ORIGINAL_REVIEW" \
  --attestation "$BOUNDARY_PACKAGE/agent-attestation.json" \
  --manifest "$BOUNDARY_PACKAGE/manifest.json" \
  --safety "$BOUNDARY_SAFETY" --lifecycle "$BOUNDARY_LIFECYCLE" \
  --output "$BOUNDARY_GATE"
```

The package starts unreviewed. Delegated semantic review uses
`reviewer_kind=agent`, `human_review_completed=false`, explicit delegation and
actual capture/manifest/report hashes; it never preapproves all cases. A pass
requires all 53 cases executed and reviewed, zero severe safety errors, every
high-risk case passing, ordinary pass rate at least 90%, and required evidence
delivery plus applicable citation validity at least 95%. Clarification without
an applicable citation is reported separately. FAILED, CANCELLED, unreviewed,
invented assertions or an unexplained source gap cannot pass. Run the original
gate with `--e2e-review "$ORIGINAL_REVIEW"` as well, retaining its failures.

`run_evidence_boundary_diagnostics.py --live` adds separate missing/multiple/
incorrect reference scenarios and identifier clarification followups. It
requires a protected runtime, new report and capture paths; `--temporal-mode`
must match runtime time configuration. Followups bind the real captured parent
turn in the same conversation and require a new authorized `fact_read` for the
specified resource. These supplemental scenarios never replace frozen cases,
and their report remains UNREVIEWED until separately reviewed. A failed
prerequisite or missing fact read is retained as failure.

Provider retries are limited to one for transient 429/5xx/connection or timeout
failures, within the original ledger, quota, cancellation and deadline.
Malformed/truncated output, schema bounds and authorization failures do not
retry. `403 AllocationQuota.FreeTierOnly` is a nonretryable `allocation_quota`
failure, distinct from invalid credentials; enabling paid usage is an account
setting prerequisite, not permission to replace the fixed model or ledger.

Only a passing independent gate permits the next Keycloak → Medusa → Open Food
Facts milestone. The allowed conclusion is “合成资料上的 V2 证据边界验收通过”;
`real_service_acceptance=false` remains mandatory. This work introduces no
public HTTP/SSE fields, browser API or DDL changes.


`run_keycloak_protocol.py` validates the local Keycloak → Go → gRPC → Python
boundary with protected synthetic accounts. It checks signed-token identity,
Go database role/shop/revision comparisons, live logout and expiration, and
rejection of old result replay. An explicit non-superuser authority fixture
performs reversible synthetic identity changes only; business results are never
written by SQL. Use `--live --accounts-file … --oidc-config … --runtime-env …
--authority-env … --capture-dir … --report …`; default invocation only prints a
plan. Captures remain outside Git with 0700/0600 permissions, tokens are never
captured, and an interrupted run records NOT_PASSED with partial observations.
Local offline-AI protocol evidence does not establish model quality, browser
login, production identity, or `real_service_acceptance`.

Open-source service rows may become `local_integration_passed` only with a
pinned version, artifact/license hashes, reviewed synthetic accounts, and a
passing non-production report hash. This service state is independent of
dataset download/admission and cannot authorize public or production data.

## Merchant customer-service evaluation

The merchant milestone defers Medusa/OFF/live facts; the historical candidate audits above remain independent. Frozen staff cases, answer points and evidence-boundary scores are unchanged. `customer-service-contract.v1.json` and `customer-service-cases.v1.json` freeze the separate customer decision contract and 15 queries/source hashes before generation.

`run_customer_service.py` uses Go HTTPS, an opaque guest Cookie, CSRF/idempotency and persisted SSE decisions; it records a partial NOT_PASSED report after each capture. Restricted captures and free-form semantic review remain outside Git. `customer_service_quality.py` binds agent reviews to the exact contract/capture hashes, checks high-risk behavior, evidence/citations and ordinary thresholds, and refuses missing reviews, provider failures or changed outputs. Clarification without citations is reported separately from applicable answer citations. Passing transport checks never preapproves semantics.

Browser drivers `customer-service.browser.mjs`, `customer-control.browser.mjs` and `customer-source.browser.mjs` use actual Keycloak, Go and ingestion. They exercise customer answers/sources, human claim/reply, lease/restart controls and source revoke/rollback/replay. Results only prove their recorded scopes; self-signed TLS is an explicit local test, and all reports retain real_service_acceptance=false, reviewer_kind=agent and human_review_completed=false where semantic review applies.

Staff generation binds supplementary qualification claims through `limit_citation_ids`; only actually delivered source IDs can support these statements. Customer replies use their separate decision schema. A transient Provider failure permits exactly one retry within the original deadline, with each attempt recorded in the shared ledger; schema, truncation, authorization and TLS failures are not retried. The first attempt reserves half of the remaining deadline for this recovery.

The safety HTTP driver revalidates every current migration checksum against the repository and requires the frozen lifecycle migration identity to remain an unchanged prefix. New append-only migrations are recorded in the new safety report; the original lifecycle report is never rewritten.


## Read-only customer execution diagnosis

Run `python/.venv/bin/python eval/diagnose_customer_execution.py
--runtime-config /protected/diagnostic.env --tenant TENANT_ID --shop SHOP_ID
--conversation CONVERSATION_ID --execution EXECUTION_ID
--output-dir /protected/new-diagnosis` as one shell command. The owned regular
0600 config supplies `V2_GATEWAY_DSN` and `V2_AI_DSN` for the same database,
using the existing restricted gateway and AI roles. Both connections use
repeatable-read, read-only transactions and bounded statements. They do not
share a database snapshot; diagnose terminal executions for a stable result.
No retrieval, model call, current ACL substitution or business write occurs.

The new owned 0700 output directory must be outside Git. JSON and Markdown
contain IDs, ranks, scores, timestamps and hashes only. Generation-input trace
records the final constructed request before Provider invocation, including
when invocation fails; it is not a receipt. Final citations cannot reconstruct
the full generation context. Old/missing/duplicate stage records remain
UNKNOWN. Candidate/re-rank/context loss and final citation membership are
observations, never automatic semantic judgments. Uncited context can be
legitimately unused. Historical authorization remains UNKNOWN without its
own historical evidence; present permission state is not a substitute.

Optional `--contract`, `--evidence-sidecar`, `--capture` and `--review` accept
owned regular 0600 JSON files. Each is an execution-specific envelope with
`identity` containing string `tenant_id`, `shop_id`, `conversation_id`, and
`execution_id`. The contract envelope binds the frozen evaluation contract;
the sidecar's `required_evidence` lists actual `id`, `document_id`, `version_id`
and positive `rank`, as bound by that contract. These are not generated from
the observed answer. Free text remains in the protected files.

The capture envelope must contain `answer_sha256` matching the persisted
execution answer and `final_citations` matching the persisted citation
metadata. To display an existing agent semantic verdict, the review envelope
must bind `answer_sha256`, the report's `trace_sha256`, and exact file hashes
`contract_sha256`, `evidence_sidecar_sha256`, `capture_sha256`; it also requires
`reviewer_kind=agent`, `human_review_completed=false`, and
`verdict=PASS|FAIL|UNKNOWN`. Missing or mismatched bindings fail closed.
Without such a review, semantic status is UNKNOWN regardless of ID coverage.
The tool does not modify frozen answer-point scoring or
`real_service_acceptance=false`.
