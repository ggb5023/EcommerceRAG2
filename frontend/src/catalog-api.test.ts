import { afterEach, describe, expect, it, vi } from 'vitest'
import { CatalogError, searchCatalog } from './catalog-api'

afterEach(() => vi.unstubAllGlobals())

describe('catalog api', () => {
  it('builds a paged filter query and returns the page', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ items: [], total: 0, page: 2, page_size: 10, business_date: '2026-11-06', notice: 'n' }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const page = await searchCatalog({ q: '保温杯', category: '日用品', page: 2, pageSize: 10 })
    expect(page.total).toBe(0)
    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('/v1/catalog/skus?')
    expect(url).toContain('q=%E4%BF%9D%E6%B8%A9%E6%9D%AF')
    expect(url).toContain('page=2')
    expect(url).toContain('page_size=10')
  })

  it('surfaces a forbidden response as a catalog error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 403 }))
    await expect(searchCatalog({})).rejects.toBeInstanceOf(CatalogError)
  })
})
