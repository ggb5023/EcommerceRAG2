import { afterEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import {
  createIntelWatchConfig,
  getIntelObservations,
  getIntelWatchConfigs,
  updateIntelWatchConfig,
} from './api'
import IntelPage from './pages/IntelPage.vue'

afterEach(() => vi.unstubAllGlobals())

describe('intel api', () => {
  it('passes market, category, platform and since filters to observations', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"items":[]}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await getIntelObservations({
      market: '电商', category: '保温杯', platform: '竞品', since: '2026-11-01T00:00:00Z',
    })
    expect(fetchMock).toHaveBeenCalledWith(
      '/admin/v1/intel/observations?limit=50&market=%E7%94%B5%E5%95%86&category=%E4%BF%9D%E6%B8%A9%E6%9D%AF&platform=%E7%AB%9E%E5%93%81&since=2026-11-01T00%3A00%3A00Z',
      { credentials: 'same-origin' },
    )
  })

  it('reads and creates watch configs', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('{"items":[]}', { status: 200 }))
      .mockResolvedValueOnce(new Response('{"id":1,"enabled":true}', { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)
    await getIntelWatchConfigs()
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/admin/v1/intel/watch-configs', {
      credentials: 'same-origin',
    })
    await createIntelWatchConfig({ market: 'm', category: 'c', platform: 'p', enabled: true })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/admin/v1/intel/watch-configs', expect.objectContaining({
      method: 'POST', credentials: 'same-origin',
      body: JSON.stringify({ market: 'm', category: 'c', platform: 'p', enabled: true }),
    }))
  })

  it('patches a watch config with PATCH', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"id":1,"enabled":false}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await updateIntelWatchConfig(1, { enabled: false })
    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/intel/watch-configs/1', expect.objectContaining({
      method: 'PATCH', credentials: 'same-origin', body: JSON.stringify({ enabled: false }),
    }))
  })
})

describe('intel page', () => {
  it('renders both sections and no currency symbols', async () => {
    const html = await renderToString(h(IntelPage))
    expect(html).toContain('情报观察')
    expect(html).toContain('观测列表')
    expect(html).toContain('监控配置')
    expect(html).toContain('暂无观测')
    expect(html).toContain('暂无监控配置')
    const visible = html.replace(/<!--[\s\S]*?-->/g, '')
    expect(visible).not.toMatch(/[¥$€£]/)
    expect(visible).not.toContain('金额')
    expect(visible).not.toContain('汇率')
  })
})
