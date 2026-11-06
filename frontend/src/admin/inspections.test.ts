import { afterEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { getInspection, getInspections, runInspections, setInspectionStatus, type InspectionItem } from './api'
import InspectionsView from './InspectionsView.vue'

afterEach(() => vi.unstubAllGlobals())

const high: InspectionItem = {
  id: 1, shop_id: 'shop-a', kind: 'expired_not_removed', severity: 'high', status: 'open',
  subject_type: 'document_version', subject_id: '9',
  evidence: { title: '过期资料', effective_to: '2026-11-05', business_date: '2026-11-06' },
  detected_at: '2026-11-06T01:00:00Z', resolved_at: null,
}
const medium: InspectionItem = {
  id: 2, shop_id: 'shop-a', kind: 'unanswered_high_frequency', severity: 'medium', status: 'acknowledged',
  subject_type: 'gap', subject_id: 'shop-a:退款要多久',
  evidence: { query: '退款要多久？', count: 3 },
  detected_at: '2026-11-06T02:00:00Z', resolved_at: null,
}

describe('inspection api boundary', () => {
  it('reads the open list with the selected shop', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"items":[]}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await getInspections('open', 'shop-a')
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/inspections?status=open&shop_id=shop-a', {
      credentials: 'same-origin',
    })
  })

  it('reads one evidence panel', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"id":1}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await getInspection(1, 'shop-a')
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/inspections/1?shop_id=shop-a', {
      credentials: 'same-origin',
    })
  })

  it('runs the inspection for the selected shop', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"run_id":"r1","by_kind":{}}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await runInspections('shop-a')
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/inspections/run?shop_id=shop-a', expect.objectContaining({
      method: 'POST', credentials: 'same-origin', body: '{}',
    }))
  })

  it('posts a triage action', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"id":1,"status":"resolved"}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await setInspectionStatus(1, 'resolved', 'shop-a')
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/inspections/1/status?shop_id=shop-a', expect.objectContaining({
      method: 'POST', credentials: 'same-origin', body: JSON.stringify({ status: 'resolved' }),
    }))
  })
})

describe('inspection workbench rendering', () => {
  it('groups high and medium findings and exposes the three actions', async () => {
    const html = await renderToString(h(InspectionsView, { items: [high, medium] }))
    expect(html).toContain('高优先级')
    expect(html).toContain('中优先级')
    expect(html).toContain('过期未下架')
    expect(html).toContain('高频未答')
    expect(html).toContain('过期资料')
    expect(html).toContain('退款要多久？')
    expect(html).toContain('证据面板')
    expect(html).toContain('data-testid="inspection-acknowledge"')
    expect(html).toContain('data-testid="inspection-resolve"')
    expect(html).toContain('data-testid="inspection-dismiss"')
    expect(html).toContain('data-testid="inspection-group-high"')
    expect(html).toContain('data-testid="inspection-group-medium"')
  })

  it('renders the empty state', async () => {
    const html = await renderToString(h(InspectionsView, { items: [] }))
    expect(html).toContain('暂无待办巡检项')
    expect(html).toContain('data-testid="inspections-empty"')
  })
})
