import { afterEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { ApiError, get, post } from './api'
import OutcomesSummary from './OutcomesSummary.vue'

afterEach(() => vi.unstubAllGlobals())

describe('admin API boundary', () => {
  it('uses same-origin credentials for authorized reads', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"items":[]}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await get('/admin/v1/merchant/shops')

    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/merchant/shops', {
      credentials: 'same-origin',
    })
  })

  it('does not hide server authorization failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
      '{"error":{"code":"admin_scope_denied"}}', { status: 403 },
    )))

    await expect(get('/admin/v1/merchant/reviews')).rejects.toMatchObject({
      status: 403,
      message: expect.stringContaining('没有该操作的授权范围'),
    } satisfies Partial<ApiError>)
  })

  it('sends scoped version confirmation as a JSON command', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"status":"active"}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const body = { confirm: true, shop_id: 'shop-a', expected_fencing_epoch: 4 }

    await post('/admin/v1/merchant/versions/12/publish', body)

    expect(fetchMock).toHaveBeenCalledWith('/admin/v1/merchant/versions/12/publish', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify(body),
    }))
  })
})

describe('outcomes panel rendering', () => {
  it('renders pending, cancelled and both rate labels', async () => {
    const html = await renderToString(h(OutcomesSummary, {
      summary: {
        turns_total: 40, scored_total: 25, solved: 8, clarified: 4, abandoned: 6,
        failed: 4, cancelled: 3, unknown: 5, pending: 10, reasked_within_5m: 2,
        auto_rate: 8 / 21, assisted_rate: 12 / 25, truncated: false, metric_gap: false,
      },
    }))
    expect(html).toContain('进行中')
    expect(html).toContain('>10<')
    expect(html).toContain('已取消')
    expect(html).toContain('>3<')
    expect(html).toContain('直接解决率')
    expect(html).toContain('8 / 25 轮')
    expect(html).toContain('38.1%')
    expect(html).toContain('无需人工介入率')
    expect(html).toContain('12 / 25 轮')
    expect(html).toContain('48.0%')
    expect(html).toContain('两个比率的基数都是')
    expect(html).toContain('轮次；一次澄清会同时产生父轮（澄清）与子轮（直接解决）。')
    expect(html).toContain('表示未转人工的比例，不代表答案正确；正确率见离线评测')
  })
})
