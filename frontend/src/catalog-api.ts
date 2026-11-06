export type CatalogItem = {
  sku_code: string
  spu_code?: string | null
  title: string
  category?: string | null
  brand?: string | null
  color?: string | null
  size_label?: string | null
  size_chart_code?: string | null
  attributes?: Record<string, unknown> | null
  shop_id: string
}

export type CatalogPage = {
  items: CatalogItem[]
  total: number
  page: number
  page_size: number
  business_date: string
  notice: string
}

export class CatalogError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
  }
}

export async function searchCatalog(params: {
  q?: string
  category?: string
  sku?: string
  attributeKey?: string
  attributeValue?: string
  page?: number
  pageSize?: number
}): Promise<CatalogPage> {
  const query = new URLSearchParams()
  if (params.q) query.set('q', params.q)
  if (params.category) query.set('category', params.category)
  if (params.sku) query.set('sku', params.sku)
  if (params.attributeKey && params.attributeValue) {
    query.set('attribute_key', params.attributeKey)
    query.set('attribute_value', params.attributeValue)
  }
  query.set('page', String(params.page ?? 1))
  query.set('page_size', String(params.pageSize ?? 24))
  const response = await fetch(`/v1/catalog/skus?${query.toString()}`, { credentials: 'same-origin' })
  if (!response.ok) {
    throw new CatalogError(
      response.status === 403 ? '当前账号无权查看该店铺的商品目录。' : '商品目录暂不可用，请稍后重试。',
      response.status,
    )
  }
  return response.json() as Promise<CatalogPage>
}
