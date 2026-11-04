import { describe, expect, it } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import ReportPanels from './ReportPanels.vue'

describe('report panels', () => {
  it('renders token and handoff data without currency', async () => {
    const html = await renderToString(h(ReportPanels, {
      usage: {
        input_tokens: 12, output_tokens: 4, calls: 3, failed_calls: 1,
        by_slot: [{ slot: 'generation', input_tokens: 12, output_tokens: 4, calls: 3, failed_calls: 1 }],
        by_model: [],
      },
      handoff: { range: '30d', window_start: '', window_end: '', handoff_conversations: 2, customer_conversations: 4, handoff_rate: 0.5 },
    }))
    expect(html).toContain('data-testid="report-usage"')
    expect(html).toContain('generation')
    expect(html).toContain('50.00%')
    expect(html).not.toMatch(/[$￥€£]|currency|价格|金额/)
  })

  it('renders null handoff as no data', async () => {
    const html = await renderToString(h(ReportPanels, {
      usage: { input_tokens: 0, output_tokens: 0, calls: 0, failed_calls: 0, by_slot: [], by_model: [] },
      handoff: { range: '30d', window_start: '', window_end: '', handoff_conversations: 0, customer_conversations: 0, handoff_rate: null },
    }))
    expect(html).toContain('无数据')
  })
})
