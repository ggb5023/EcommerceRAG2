import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from './api'
import { validityBody, submitValidity, validityError } from './validity'

const detail = { version_id: '12', effective_from: null, effective_to: null, fencing_epoch: 4 }
const session = { permission_revision: 'rev-1' }

afterEach(() => vi.unstubAllGlobals())

describe('validity form contract', () => {
  it('builds the exact command body and sends an idempotency key', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"status":"accepted"}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await submitValidity(detail, session, '2026-11-01', '2026-12-31', '资料过期后下架')
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(init.body)).toEqual({
      effective_from: '2026-11-01', effective_to: '2026-12-31', reason: '资料过期后下架',
      confirm: true, expected_fencing_epoch: 4, permission_revision: 'rev-1',
    })
    expect(new Headers(init.headers).get('Idempotency-Key')).toMatch(/^validity-12-/)
  })

  it('supports clearing both dates and explains a conflict', async () => {
    expect(validityBody(detail, session, '', '', '恢复不限有效期'))
      .toMatchObject({ effective_from: null, effective_to: null, confirm: true })
    expect(validityError(new ApiError(409, 'conflict'))).toContain('重新加载')
  })
})
