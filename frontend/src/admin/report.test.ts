import { afterEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { getReport, type EvaluationReport } from './api'
import ReportView from './ReportView.vue'

afterEach(() => vi.unstubAllGlobals())

const notMeasured: EvaluationReport = {
  input: {
    status: 'NOT_MEASURED', git_commit: 'abc123', package_sha256: 'a'.repeat(64),
    manifest_sha256: 'b'.repeat(64), questions_sha256: 'c'.repeat(64),
    business_date: null, cohorts: ['workspace', 'customer'], version_ids: null,
    window_start: '2026-11-01T00:00:00+00:00', window_end: '2026-11-06T00:00:00+00:00',
    quality_report_sha256: null, reason: 'demo package unavailable',
  },
  operations: { status: 'NOT_MEASURED', reason: 'outcomes unavailable' },
  handoff: { status: 'NOT_MEASURED', handoff_conversations: null, customer_conversations: null, handoff_rate: null },
  quality: { status: 'NOT_MEASURED', wrong_answer_rate: null, case_count: null, reviewer_kind: 'agent', human_review_completed: false, source_sha256: null },
  tokens: { status: 'NOT_MEASURED', input_tokens: null, input_tokens_per_1000: null },
}

const measured: EvaluationReport = {
  input: {
    status: 'MEASURED', git_commit: 'abc123', package_sha256: 'a'.repeat(64),
    manifest_sha256: 'b'.repeat(64), questions_sha256: 'c'.repeat(64),
    business_date: '2026-09-29', cohorts: ['workspace', 'customer'],
    version_ids: { 'doc-a': '7' }, window_start: '2026-11-01T00:00:00+00:00',
    window_end: '2026-11-06T00:00:00+00:00', quality_report_sha256: 'd'.repeat(64),
  },
  operations: {
    status: 'MEASURED', turns_total: 0, scored_total: 0, solved: 0, clarified: 0,
    abandoned: 0, failed: 0, cancelled: 0, pending: 0, unknown: 0,
    auto_rate: 0, assisted_rate: 0,
  },
  handoff: {
    status: 'MEASURED', handoff_conversations: 0, customer_conversations: 0, handoff_rate: null,
  },
  quality: {
    status: 'MEASURED', wrong_answer_rate: 0.132075, case_count: 53, reviewer_kind: 'agent',
    human_review_completed: false, source_sha256: 'e'.repeat(64),
  },
  tokens: {
    status: 'MEASURED', question_count: 10, input_tokens: 0, output_tokens: 0,
    calls: 65, failed_calls: 0, input_tokens_per_1000: null, output_tokens_per_1000: null,
    calls_per_1000: 6500, failed_calls_per_1000: 0,
    by_slot: [{ slot: 'generation', input_tokens: 0, output_tokens: 0, calls: 65, failed_calls: 0,
      input_tokens_per_1000: null, output_tokens_per_1000: null, calls_per_1000: 6500, failed_calls_per_1000: 0 }],
    by_model: [],
  },
}

function noMoney(html: string) {
  expect(html).not.toMatch(/[¥$€£]/)
}

describe('report api', () => {
  it('reads the read-only report endpoint', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(measured), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await getReport()
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/report', { credentials: 'same-origin' })
  })
})

describe('report view', () => {
  it('renders all five sections and never turns NOT_MEASURED/null into 0', async () => {
    const html = await renderToString(h(ReportView, { report: notMeasured }))
    for (const title of ['输入', '运营结果', '质量', '转人工', 'token']) expect(html).toContain(title)
    expect(html).toContain('未测量')
    expect(html).not.toContain('0%')
    expect(html).toContain('demo package unavailable')
    expect(html).not.toContain('未生成报告')
    noMoney(html)
  })

  it('renders measured zeros and rates without treating null as zero', async () => {
    const html = await renderToString(h(ReportView, { report: measured }))
    expect(html).toContain('0.0%')
    expect(html).toContain('13.2%')
    expect(html).toContain('65')
    expect(html).toContain('6500.00')
    expect(html).toContain('未测量')
    expect(html).toContain('否')
    expect(html).toContain('generation')
    noMoney(html)
  })

  it('shows the missing-report fallback with a pointer to the CLI docs', async () => {
    const html = await renderToString(h(ReportView, { report: null, notFound: true }))
    expect(html).toContain('未生成报告')
    expect(html).toContain('demo-report.py')
    expect(html).toContain('36-m9-report.md')
    expect(html).toContain('data-testid="report-missing"')
    noMoney(html)
  })
})
